"""Run the installed macOS provisioning adapter in disposable homes."""
from contextlib import ExitStack
from pathlib import Path
from tempfile import TemporaryDirectory
import os
import socket
import subprocess
import sys


if len(sys.argv) != 3:
    raise SystemExit('Usage: python3 scripts/provision-postgres-smoke.py RELEASE_DIR POSTGRES_BIN_DIR')
adapter = Path(sys.argv[1]).resolve(strict=True) / 'bin' / 'provision-postgres-macos'
pgbin = Path(sys.argv[2]).resolve(strict=True)
bundled_bin = adapter.parent.parent / 'postgresql' / 'bin'
runtime_args = () if pgbin == bundled_bin else (pgbin,)

with TemporaryDirectory(prefix='difqo-provision-') as scratch, ExitStack() as cleanup:
    work = Path(scratch)
    home = work / 'Synthetic Home'
    external_home = work / 'External Home'
    home.mkdir()
    external_home.mkdir()
    with socket.socket() as address:
        address.bind(('127.0.0.1', 0))
        port = address.getsockname()[1]
    env = {**os.environ, 'HOME': str(home), 'DIFQO_DATABASE_URL': '', 'DIFQO_POSTGRES_PORT': str(port)}
    root = home / 'Library' / 'Application Support' / 'Difqo' / 'postgres-18'
    ctl = [pgbin / 'pg_ctl', '-D', root / 'data']
    pg_env = {'HOME': str(home), 'PATH': '/usr/bin:/bin', 'LC_ALL': 'C'}

    def provision(*args, extra_env=None, code=0):
        result = subprocess.run(
            ['/bin/bash', adapter, *args], cwd=work, env={**env, **(extra_env or {})},
            capture_output=True, text=True, timeout=75,
        )
        if result.returncode != code:
            raise AssertionError(f'Provisioning exited {result.returncode}, expected {code}')
        return result.stdout + result.stderr

    def stopped():
        return subprocess.run([*ctl, 'status'], env=pg_env, capture_output=True).returncode == 3

    def start():
        subprocess.run([*ctl, '-l', root / 'fixture.log', '-w', 'start'], env=pg_env, check=True, timeout=35,
                       stdout=subprocess.DEVNULL)

    def stop():
        subprocess.run([*ctl, '-m', 'fast', '-w', 'stop'], env=pg_env, check=True, timeout=35,
                       stdout=subprocess.DEVNULL)

    def stop_if_running():
        status = subprocess.run([*ctl, 'status'], env=pg_env, capture_output=True, timeout=10)
        if status.returncode == 0:
            stop()

    # subprocess timeouts can kill the adapter before its EXIT trap runs.
    cleanup.callback(stop_if_running)

    migration_count = len(list((adapter.parent.parent / 'migrations').glob('*.sql')))
    assert migration_count > 0
    with socket.socket() as blocker:
        blocker.bind(('127.0.0.1', port))
        blocker.listen(1)
        assert 'Could not start the private cluster' in provision(*runtime_args, code=1)
        assert stopped()
    assert f'Applied {migration_count} migration(s)' in provision(*runtime_args)
    assert stopped()
    secret_file = root / 'password'
    assert secret_file.stat().st_mode & 0o777 == 0o600
    secret = secret_file.read_text().strip()
    dsn = f'postgres://difqo:{secret}@127.0.0.1:{port}/difqo?sslmode=disable'

    def query(sql):
        proc = subprocess.run(
            [pgbin / 'psql', '-XqAt', '-w', '-h', '127.0.0.1', '-p', str(port), '-U', 'difqo',
             '-d', 'difqo', '-v', 'ON_ERROR_STOP=1', '-c', sql],
            env={**pg_env, 'PGPASSWORD': secret}, capture_output=True, text=True, check=True, timeout=10,
        )
        return proc.stdout.strip()

    try:
        start()
        assert query("SELECT rolsuper FROM pg_roles WHERE rolname = current_user") == 'f'
        assert query("SHOW listen_addresses") == '127.0.0.1'
        assert query("INSERT INTO tasks(goal) VALUES ('keep fixture') RETURNING goal") == 'keep fixture'
        assert 'Stop the Difqo-managed' in provision(*runtime_args, code=1)
        output = provision(extra_env={'HOME': str(external_home), 'DIFQO_DATABASE_URL': dsn})
        assert 'Applied 0 migration(s)' in output and secret not in output
        assert not list(external_home.iterdir())
    finally:
        stop_if_running()

    assert 'differs from the existing cluster port' in provision(
        *runtime_args, extra_env={'DIFQO_POSTGRES_PORT': str(55432 if port != 55432 else 55433)}, code=1,
    )
    output = provision(*runtime_args, extra_env={'DIFQO_POSTGRES_PORT': ''})
    assert 'Applied 0 migration(s)' in output and secret not in output
    assert stopped() and secret_file.read_text().strip() == secret
    try:
        start()
        assert query('SELECT goal FROM tasks') == 'keep fixture'
    finally:
        stop_if_running()
print('PostgreSQL provisioning smoke passed: loopback, non-superuser role, private credential, existing DB bypass, occupied-port recovery, and safe rerun.')
