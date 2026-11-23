"""Exercise the local Slack lifecycle API against PostgreSQL and a loopback relay."""
from contextlib import closing
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from threading import Thread
from urllib.error import HTTPError
from urllib.request import Request, build_opener, ProxyHandler
from uuid import uuid4
import json
import os
import socket
import subprocess
import sys
import time

binary = str(Path(sys.argv[1]).resolve())
database = os.environ['DIFQO_DATABASE_URL']
schema = 'slack_smoke_' + uuid4().hex
scoped_database = database + ('&' if '?' in database else '?') + 'options=-csearch_path%3D' + schema
opener = build_opener(ProxyHandler({}))
binding = {'app_id': 'A22', 'workspace_id': 'T22', 'operator_id': 'U22', 'channel_id': 'D22'}
private = {'app_id': 'A11', 'workspace_id': 'T11', 'operator_id': 'U11', 'channel_id': 'D11'}
proofs = {}
stage = {}


def sql(command, dsn=scoped_database):
    subprocess.run(['psql', '-X', dsn, '-v', 'ON_ERROR_STOP=1', *command], check=True, stdout=subprocess.DEVNULL)


class Relay(BaseHTTPRequestHandler):
    def do_PUT(self):
        pair = self.path.removeprefix('/pairings/')
        proof = self.headers.get('Authorization', '')
        if not self.path.startswith('/pairings/') or len(pair) != 32 or len(proof) != 71:
            self.send_error(400)
            return
        if proofs.setdefault(pair, proof) != proof:
            self.send_error(403)
            return
        if not stage:
            self.send_error(503)
            return
        body = json.dumps(stage).encode()
        self.send_response(200)
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_):
        pass


def api(action=None, **fields):
    data = json.dumps({'action': action, **fields}).encode() if action else None
    request = Request(f'http://127.0.0.1:{app_port}/slack/connection', data=data,
                      headers={'Content-Type': 'application/json'})
    try:
        with opener.open(request, timeout=4) as res:
            body = res.read()
            view = json.loads(body)
            assert 'proof' not in body.decode() and 'relay' not in view
            return res.status, view
    except HTTPError as err:
        return err.code, json.loads(err.read()) if err.code == 502 else None


def start():
    server = subprocess.Popen([binary, 'server', '-port', str(app_port), '-slack-relay-fixture',
                               f'http://127.0.0.1:{relay.server_port}'],
                              env=os.environ | {'DIFQO_DATABASE_URL': scoped_database})
    for _ in range(75):
        try:
            if api()[0] == 200:
                return server
        except OSError:
            if server.poll() is not None:
                break
        time.sleep(0.05)
    server.terminate()
    server.wait(timeout=5)
    raise RuntimeError('Slack fixture service did not start')


def private_replay():
    event = private | {'event_id': 'EvMigrated', 'type': 'message', 'channel_type': 'im',
                       'bot': False, 'text': 'goal Preserved private task'}
    return subprocess.run([binary, 'slack-import', '-app-id', 'A11', '-workspace-id', 'T11',
                           '-operator-id', 'U11', '-dm-id', 'D11'],
                          env=os.environ | {'DIFQO_DATABASE_URL': scoped_database},
                          input=json.dumps(event), text=True, capture_output=True, timeout=5)


sql(['-c', 'CREATE SCHEMA ' + schema], database)
try:
    for migration in sorted(Path('migrations').glob('*.sql')):
        if migration.name.startswith('0009_'):
            sql(['-c', """INSERT INTO slack_intake_binding (app_id, workspace_id, operator_id, channel_id)
                VALUES ('A11','T11','U11','D11');
                WITH t AS (INSERT INTO tasks(goal) VALUES ('Preserved private task') RETURNING id)
                INSERT INTO slack_task_sources (event_id, task_id) SELECT 'EvMigrated', id FROM t;"""])
        sql(['-f', str(migration)])
    with closing(socket.socket()) as reserved:
        reserved.bind(('127.0.0.1', 0))
        app_port = reserved.getsockname()[1]

    with ThreadingHTTPServer(('127.0.0.1', 0), Relay) as relay:
        Thread(target=relay.serve_forever, daemon=True).start()
        server = start()
        try:
            _, old = api()
            assert old['mode'] == 'private' and old['binding'] == private
            assert private_replay().stdout.strip() == 'Slack event already imported'
            assert api('begin')[0] == 409
            assert api('disconnect', generation=old['generation'])[0] == 200
            assert private_replay().returncode != 0

            _, pending = api('begin')
            pair = pending['pairing_id']
            assert api('begin')[1]['pairing_id'] == pair
            stage.update(state='approval_needed', verified_workspace='T22', verified_operator='U22')
            assert api('refresh', pairing_id=pair)[1]['state'] == 'approval_needed'
            assert api('confirm', pairing_id=pair, binding=binding)[0] == 409
            server.terminate()
            server.wait(timeout=5)
            server = start()
            assert api()[1]['pairing_id'] == pair
            assert api('refresh', pairing_id=pair)[1]['state'] == 'approval_needed'

            stage.clear()
            stage.update(state='installed', installed_workspace='T99')
            assert api('refresh', pairing_id=pair)[0] == 502
            _, retained = api()
            assert retained['state'] == 'approval_needed' and retained['verified_workspace'] == 'T22'
            assert 'installed_workspace' not in retained
            stage.update(installed_workspace='T22')
            _, installed = api('refresh', pairing_id=pair)
            assert installed['state'] == 'installed' and installed['installed_workspace'] == 'T22'
            assert installed['verified_workspace'] == 'T22' and installed['verified_operator'] == 'U22'
            assert api('confirm', pairing_id=pair, binding=binding)[0] == 409
            stage.update(state='ready_to_link', verified_workspace='T22', verified_operator='U99',
                         binding=binding | {'operator_id': 'U99'})
            assert api('refresh', pairing_id=pair)[0] == 502
            stage.update(verified_operator='U22', binding=binding)
            assert api('refresh', pairing_id=pair)[1]['state'] == 'ready_to_link'
            assert api('confirm', pairing_id='stale', binding=binding)[0] == 409
            assert api('confirm', pairing_id=pair, binding=binding | {'channel_id': 'D99'})[0] == 409
            ready = stage.copy()
            stage.clear()
            assert api('confirm', pairing_id=pair, binding=binding)[0] == 502
            assert api()[1]['mode'] == 'disconnected'
            stage.update(ready)
            _, connected = api('confirm', pairing_id=pair, binding=binding)
            assert connected['mode'] == 'official' and connected['state'] == 'connected'
            assert api('confirm', pairing_id=pair, binding=binding)[1]['generation'] == connected['generation']
            assert private_replay().returncode != 0
            stage.clear()
            assert api('refresh', pairing_id=pair)[1]['state'] == 'reconnecting'
            stage.update(state='ready_to_link', installed_workspace='T22', verified_workspace='T22',
                         verified_operator='U22', binding=binding)
            assert api('refresh', pairing_id=pair)[1]['state'] == 'connected'
            stage.clear()
            stage.update(state='reconnect_required')
            _, retired = api('refresh', pairing_id=pair)
            assert retired['mode'] == 'disconnected' and retired['generation'] > connected['generation']
            assert api('confirm', pairing_id=pair, binding=binding)[0] == 409
            _, new = api('begin')
            assert new['pairing_id'] != pair
            assert api('disconnect', pairing_id=pair, generation=retired['generation'])[0] == 409
            sql(['-c', """UPDATE slack_connection SET pairing = jsonb_set(pairing, '{expires}',
                '"2000-01-01T00:00:00Z"'::jsonb)"""])
            assert api()[1]['state'] == 'expired'
            assert api('refresh', pairing_id=new['pairing_id'])[0] == 409
            _, closed = api('disconnect', pairing_id=new['pairing_id'], generation=new['generation'])
            assert api('enable_private', generation=closed['generation'])[1]['binding'] == private
            assert private_replay().stdout.strip() == 'Slack event already imported'
            print('Slack lifecycle smoke passed: private migration, identity vs installation, confirmation, restart, reconnect, revocation, and replay.')
        finally:
            server.terminate()
            server.wait(timeout=5)
            relay.shutdown()
finally:
    sql(['-c', 'SET client_min_messages = warning; DROP SCHEMA ' + schema + ' CASCADE'], database)
