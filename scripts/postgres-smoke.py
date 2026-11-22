from concurrent.futures import ThreadPoolExecutor
from contextlib import closing
from pathlib import Path
from tempfile import TemporaryDirectory
import json
import os
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request

urllib.request.install_opener(urllib.request.build_opener(urllib.request.ProxyHandler({})))

if len(sys.argv) != 2 or not os.environ.get('DIFQO_DATABASE_URL'):
    raise SystemExit('Usage: DIFQO_DATABASE_URL=<fresh migrated database> python3 scripts/postgres-smoke.py <difqo binary>')

binary = str(Path(sys.argv[1]).resolve())

def run(*args):
    return subprocess.run(args, check=True, stdout=subprocess.PIPE, text=True, timeout=15).stdout

with TemporaryDirectory(prefix='difqo-smoke-') as scratch:
    fixture = Path(scratch) / 'fixture'
    fixture.mkdir()
    run('git', '-C', fixture, 'init', '-q', '--template=')
    run('git', '-C', fixture, '-c', 'user.name=Difqo Smoke',
        '-c', 'user.email=smoke@example.invalid', '-c', 'commit.gpgSign=false',
        'commit', '--allow-empty', '-qm', 'Synthetic fixture')
    expected = run('git', '-C', fixture, 'rev-parse', '--verify', 'HEAD').strip()

    with closing(socket.socket()) as reserved:
        reserved.bind(('127.0.0.1', 0))
        port = str(reserved.getsockname()[1])

    def api(path, method='GET', body=None, headers=None):
        request = urllib.request.Request(
            f'http://127.0.0.1:{port}{path}',
            data=None if body is None else json.dumps(body).encode(),
            method=method,
            headers=headers or {'Content-Type': 'application/json'},
        )
        try:
            with urllib.request.urlopen(request, timeout=2) as res:
                body = res.read()
                return res.status, json.loads(body) if body else None
        except urllib.error.HTTPError as error:
            return error.code, None

    def start():
        server = subprocess.Popen([binary, 'server', '-port', port])
        for _ in range(300):
            try:
                if api('/tasks/1')[0] in (200, 404):
                    return server
            except OSError:
                if server.poll() is not None:
                    raise RuntimeError('Server exited early')
                time.sleep(0.05)
        server.terminate()
        server.wait(timeout=5)
        raise RuntimeError('Server readiness timed out')

    server = start()
    try:
        assert api('/tasks', 'POST', {'goal': '  '})[0] == 400
        assert api('/tasks/1', headers={'Host': 'untrusted.invalid'})[0] == 400
        status, task = api('/tasks', 'POST', {'goal': 'Record fixture revision'})
        assert status == 201 and 'attempt' not in task
        run(binary, 'worker', '-port', port, '-repo', fixture)
        status, completed = api(f"/tasks/{task['id']}")
        assert status == 200
        result = {'exit_code': 0, 'output': expected}
        assert completed['attempt']['result'] == result
        path = f"/attempts/{completed['attempt']['id']}/result"
        assert api(path, 'PUT', result)[0] == 204
        assert api(path, 'PUT', {'exit_code': 1, 'output': 'conflict'})[0] == 409
        assert api('/tasks/claim', 'POST', {})[0] == 204

        assert api('/tasks', 'POST', {'goal': 'Concurrent claim'})[0] == 201
        with ThreadPoolExecutor(max_workers=12) as clients:
            claims = list(clients.map(lambda _: api('/tasks/claim', 'POST', {})[0], range(12)))
        assert sorted(claims) == [200] + [204] * 11, claims

        status, failed = api('/tasks', 'POST', {'goal': 'Check an unavailable checkout'})
        assert status == 201
        run(binary, 'worker', '-port', port, '-repo', fixture / 'missing')
        assert api(f"/tasks/{failed['id']}")[1]['attempt']['result']['exit_code'] != 0

        server.terminate()
        server.wait(timeout=5)
        server = start()
        assert api(f"/tasks/{task['id']}")[1] == completed
        print('Local smoke passed: successful and failed checks, duplicate result handling, exclusive concurrent claim, and persistence across server restart')
    finally:
        server.terminate()
        server.wait(timeout=5)
