from concurrent.futures import ThreadPoolExecutor
from contextlib import closing
from datetime import datetime
from pathlib import Path
from tempfile import TemporaryDirectory
import json
import os
import signal
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
    artifacts = Path(scratch) / 'artifacts'
    artifacts.mkdir(mode=0o700)

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
        server = subprocess.Popen([binary, 'server', '-port', port, '-artifacts', artifacts])
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

    def expire(attempt):
        # Advance this fixture past its deadline without waiting on wall time.
        run('psql', '-X', os.environ['DIFQO_DATABASE_URL'], '-v', 'ON_ERROR_STOP=1',
            '-c', 'UPDATE attempts SET lease_expires_at = clock_timestamp() '
            f"WHERE id = {int(attempt['id'])}")

    server = start()
    try:
        assert api('/tasks') == (200, [])
        assert api('/tasks', 'POST', {'goal': '  '})[0] == 400
        assert api('/tasks', 'POST', {'goal': 'Reject cross-origin dispatch'}, headers={
            'Content-Type': 'application/json', 'Sec-Fetch-Site': 'cross-site',
        })[0] == 403
        assert api('/tasks/1', headers={'Host': 'untrusted.invalid'})[0] == 400
        status, task = api('/tasks', 'POST', {'goal': 'Record fixture revision'})
        assert status == 201 and 'attempt' not in task
        run(binary, 'worker', '-port', port, '-repo', fixture)
        status, completed = api(f"/tasks/{task['id']}")
        assert status == 200
        result = {'exit_code': 0, 'output': expected}
        assert completed['attempt']['result'] == result
        assert api('/tasks') == (200, [completed])
        assert api(f"/tasks/{task['id']}/evidence/change.diff")[0] == 404
        path = f"/attempts/{completed['attempt']['id']}/result"
        assert api(path, 'PUT', result)[0] == 204
        assert api(path, 'PUT', {'exit_code': 1, 'output': 'conflict'})[0] == 409
        assert api('/tasks/claim', 'POST', {})[0] == 204

        status, concurrent = api('/tasks', 'POST', {'goal': 'Concurrent claim'})
        assert status == 201
        with ThreadPoolExecutor(max_workers=12) as clients:
            claims = list(clients.map(lambda _: api('/tasks/claim', 'POST', {}), range(12)))
        assert sorted(status for status, _ in claims) == [200] + [204] * 11, claims
        winner = next(task for status, task in claims if status == 200)
        assert winner['id'] == concurrent['id']
        assert api(f"/attempts/{winner['attempt']['id']}/result", 'PUT', result)[0] == 204

        status, failed = api('/tasks', 'POST', {'goal': 'Check an unavailable checkout'})
        assert status == 201
        run(binary, 'worker', '-port', port, '-repo', fixture / 'missing')
        assert api(f"/tasks/{failed['id']}")[1]['attempt']['result']['exit_code'] != 0

        status, interrupted = api('/tasks', 'POST', {'goal': 'Recover an interrupted worker'})
        assert status == 201
        interrupted_path = f"/tasks/{interrupted['id']}"
        slow_git = Path(scratch) / 'git'
        slow_git.write_text('#!/bin/sh\nexec sleep 60\n')
        slow_git.chmod(0o700)
        worker = subprocess.Popen([binary, 'worker', '-port', port, '-repo', fixture],
                                  env={**os.environ, 'PATH': f"{scratch}{os.pathsep}{os.environ['PATH']}"},
                                  start_new_session=True)
        try:
            for _ in range(100):
                status, claimed = api(interrupted_path)
                if status == 200 and claimed['status'] == 'claimed':
                    break
                assert worker.poll() is None, 'Worker exited before claiming'
                time.sleep(0.02)
            else:
                raise AssertionError('Worker did not claim the task')
        finally:
            if worker.poll() is None:
                os.killpg(worker.pid, signal.SIGKILL)
            worker.wait(timeout=5)

        original = claimed['attempt']
        original_path = f"/attempts/{original['id']}"
        assert original['number'] == 1
        assert api('/tasks/claim', 'POST', {})[0] == 204
        assert api(original_path + '/renew', 'POST', {})[0] == 204
        renewed = api(interrupted_path)[1]['attempt']
        assert datetime.fromisoformat(renewed['lease_expires_at']) > datetime.fromisoformat(original['lease_expires_at'])
        expire(renewed)
        assert api(interrupted_path)[1]['status'] == 'queued'
        assert api(original_path + '/renew', 'POST', {})[0] == 409
        assert api(original_path + '/result', 'PUT', result)[0] == 409

        with ThreadPoolExecutor(max_workers=12) as clients:
            reclaims = list(clients.map(lambda _: api('/tasks/claim', 'POST', {}), range(12)))
        assert sorted(status for status, _ in reclaims) == [200] + [204] * 11, reclaims
        reclaimed = next(task for status, task in reclaims if status == 200)
        replacement = reclaimed['attempt']
        assert reclaimed['id'] == interrupted['id']
        assert replacement['number'] == 2 and replacement['id'] != original['id']
        replacement_path = f"/attempts/{replacement['id']}"
        assert api(original_path + '/result', 'PUT', result)[0] == 409
        assert api(replacement_path + '/result', 'PUT', result)[0] == 204
        assert api(original_path + '/result', 'PUT', result)[0] == 409
        recovered = api(interrupted_path)[1]
        assert recovered['status'] == 'completed' and recovered['attempt']['result'] == result

        status, abandoned = api('/tasks', 'POST', {'goal': 'Exhaust the retry budget'})
        assert status == 201
        for number in (1, 2, 3):
            status, attempt_task = api('/tasks/claim', 'POST', {})
            assert status == 200 and attempt_task['id'] == abandoned['id']
            assert attempt_task['attempt']['number'] == number
            expire(attempt_task['attempt'])
        abandoned_path = f"/tasks/{abandoned['id']}"
        exhausted = api(abandoned_path)[1]
        assert exhausted['status'] == 'exhausted' and 'result' not in exhausted['attempt']
        assert api('/tasks/claim', 'POST', {})[0] == 204

        server.terminate()
        server.wait(timeout=5)
        server = start()
        assert api(f"/tasks/{task['id']}")[1] == completed
        assert api(interrupted_path)[1] == recovered
        assert api(abandoned_path)[1] == exhausted
        print('Local smoke passed: fixed checks, exclusive claims, renewable leases, interrupted worker recovery, stale results, retry exhaustion, and restart persistence')

        if os.environ.get('DIFQO_SMOKE_CODEX') == '1':
            runtime = os.environ.get('DIFQO_SMOKE_CONTAINER_RUNTIME', 'docker')
            runtime_options = ['-container-runtime', runtime]
            if os.environ.get('DIFQO_SMOKE_SELINUX_LABEL_DISABLE') == '1':
                runtime_options.append('-container-selinux-label-disable')
            auth_home = Path(scratch) / 'codex-home'
            auth_home.mkdir(mode=0o700)
            greeting = fixture / 'greeting.txt'
            greeting.write_text('before\n')
            run('git', '-C', fixture, 'add', 'greeting.txt')
            run('git', '-C', fixture, '-c', 'user.name=Difqo Smoke',
                '-c', 'user.email=smoke@example.invalid', '-c', 'commit.gpgSign=false',
                'commit', '-qm', 'Add fixture greeting')

            def worker_args(image):
                return [binary, 'worker', '-port', port, '-repo', fixture,
                        '-codex-image', image, '-codex-home', auth_home, '-artifacts', artifacts,
                        '-check', 'test "$(cat greeting.txt)" = after && printf "greeting check passed\\n"']

            def containers():
                return run(runtime, 'ps', '-a', '--filter', 'name=difqo-', '-q').splitlines()

            def run_codex(image, verify_preflight=False):
                status, submitted = api('/tasks', 'POST', {'goal': 'Replace the fixture greeting'})
                assert status == 201
                path = f"/tasks/{submitted['id']}"
                if verify_preflight:
                    rejected = subprocess.run(
                        worker_args('difqo-smoke-image-does-not-exist:missing') + runtime_options,
                        capture_output=True, text=True, timeout=45,
                    )
                    assert rejected.returncode != 0 and 'unavailable locally' in rejected.stderr, rejected.stderr
                    assert api(path) == (200, submitted)
                before = set(containers())
                subprocess.run(worker_args(image) + runtime_options, check=True, timeout=90)
                assert set(containers()) <= before
                status, task = api(path)
                assert status == 200
                return task

            blocked = run_codex('difqo-codex:0.171.0', verify_preflight=True)
            assert blocked['status'] == 'blocked', blocked
            assert blocked['attempt']['result'] == {
                'exit_code': 125, 'output': 'Codex sign-in required', 'blocked': True,
            }
            assert list(artifacts.iterdir()) == []

            executed = run_codex(os.environ.get('DIFQO_SMOKE_FIXTURE_IMAGE', 'difqo-codex-smoke'))
            assert executed['status'] == 'completed'
            outcome = executed['attempt']['result']
            assert outcome['exit_code'] == 0, outcome
            artifact_ref = outcome['artifacts']
            evidence = artifacts / artifact_ref
            patch = (evidence / 'change.diff').read_text()
            assert '-before\n+after\n' in patch and '+new file\n' in patch
            assert (evidence / 'check.log').read_text() == 'greeting check passed\n'
            evidence_url = f"http://127.0.0.1:{port}/tasks/{executed['id']}/evidence/"
            with urllib.request.urlopen(evidence_url + 'change.diff', timeout=2) as response:
                assert response.read().decode() == patch
                assert response.headers.get_content_type() == 'text/plain'
            with urllib.request.urlopen(evidence_url + 'check.log', timeout=2) as response:
                assert response.read().decode() == 'greeting check passed\n'
            assert api(f"/tasks/{executed['id']}/evidence/auth.json")[0] == 404
            assert greeting.read_text() == 'before\n' and not (fixture / 'untracked.txt').exists()
            assert not (evidence / 'workspace').exists()
            print(f'{runtime} smoke passed: pre-claim rejection, real Codex sign-in block, '
                  'synthetic turn, container cleanup, and evidence retrieval')
    finally:
        server.terminate()
        server.wait(timeout=5)
