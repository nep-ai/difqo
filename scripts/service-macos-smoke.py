"""Check LaunchAgent staging and startup control without registering with launchd."""
import json
import os
from pathlib import Path
import plistlib
import subprocess
import sys
from tempfile import TemporaryDirectory


if len(sys.argv) != 2:
    raise SystemExit('Usage: python3 scripts/service-macos-smoke.py SERVICE_SCRIPT')
service = Path(sys.argv[1]).resolve(strict=True)


def executable(path, text):
    path.write_text(text)
    path.chmod(0o755)


with TemporaryDirectory(prefix='difqo-service-') as scratch:
    root = Path(scratch)
    tools = root / 'tools'
    tools.mkdir()
    executable(tools / 'launchctl', '''#!/bin/bash
set -eu
case "$1" in
  print)
    case "$2" in */com.difqo.*) [ -e "$FIXTURE/${2##*/}" ] ;; *) exit 0 ;; esac
    ;;
  bootstrap)
    label=${3##*/}
    label=${label%.plist}
    printf '%s\n' "$label" >> "$FIXTURE/starts"
    touch "$FIXTURE/$label"
    ;;
  bootout)
    printf '%s\n' "${2##*/}" >> "$FIXTURE/stops"
    rm "$FIXTURE/${2##*/}"
    ;;
  *) exit 1 ;;
esac
''')
    executable(tools / 'curl', '''#!/bin/bash
[ -e "$FIXTURE/com.difqo.server" ] && [ "${NO_HEALTH:-}" != 1 ] && printf 200
''')
    executable(tools / 'nc', '#!/bin/bash\n[ "${PORT_TAKEN:-}" = 1 ]\n')
    executable(tools / 'sleep', '#!/bin/bash\nexit 0\n')

    def fixture(name):
        work = root / name
        home = work / 'Synthetic Home'
        release = work / 'Installed & Verified'
        bin_dir = release / 'bin'
        home.mkdir(parents=True)
        bin_dir.mkdir(parents=True)
        adapter = bin_dir / 'service-macos'
        adapter.write_text(service.read_text())
        executable(bin_dir / 'difqo', '''#!/bin/bash
set -eu
test -n "$DIFQO_DATABASE_URL"
printf '%s\n' "$1" >> "$FIXTURE/cli"
''')
        env = {**os.environ, 'HOME': str(home), 'PATH': f'{tools}:/usr/bin:/bin:/usr/sbin:/sbin',
               'FIXTURE': str(work), 'DIFQO_DATABASE_URL': ''}
        return env, home, release, adapter

    def run(env, adapter, action, ok=True, **overrides):
        result = subprocess.run(['/bin/bash', adapter, action], env={**env, **overrides},
                                text=True, capture_output=True, timeout=15)
        assert (result.returncode == 0) == ok, (result.returncode, result.stdout, result.stderr)
        return result.stdout + result.stderr

    env, home, release, adapter = fixture('external')
    state = home / 'Library/Application Support/Difqo/service'
    agents = home / 'Library/LaunchAgents'
    work = Path(env['FIXTURE'])
    dsn = 'postgres://fixture:synthetic-only@127.0.0.1:5432/fixture'
    assert 'already occupied' in run(env, adapter, 'start', ok=False, PORT_TAKEN='1', DIFQO_DATABASE_URL=dsn)
    assert not (state / 'database-url').exists()
    assert 'ready' in run(env, adapter, 'start', DIFQO_DATABASE_URL=dsn)
    config = state / 'database-url'
    assert config.stat().st_mode & 0o777 == 0o600
    assert config.read_text().strip() == dsn
    server_plist = agents / 'com.difqo.server.plist'
    plist = plistlib.loads(server_plist.read_bytes())
    assert plist['ProgramArguments'] == ['/bin/bash', str(adapter), 'run']
    assert plist['RunAtLoad'] and plist['KeepAlive']
    assert plist['EnvironmentVariables']['HOME'] == str(home)
    search_path = plist['EnvironmentVariables']['PATH'].split(':')
    assert {'/opt/homebrew/bin', '/usr/local/bin', '/Applications/Docker.app/Contents/Resources/bin'} <= set(search_path)
    assert str(tools) not in search_path
    assert dsn not in json.dumps(plist)
    assert not (agents / 'com.difqo.postgresql.plist').exists()
    assert 'ready' in run(env, adapter, 'start')
    assert 'ready' in run(env, adapter, 'status')
    assert (work / 'starts').read_text().splitlines() == ['com.difqo.server']
    assert 'conflicts' in run(env, adapter, 'start', ok=False, DIFQO_DATABASE_URL=dsn + 'other')
    run(env, adapter, 'run')
    assert (work / 'cli').read_text().splitlines() == ['setup-database', 'server']
    lock = state / '.service.lock'
    lock.mkdir()
    assert 'Another service operation' in run(env, adapter, 'stop', ok=False)
    assert 'ready' in run(env, adapter, 'status')
    assert not (work / 'stops').exists()
    lock.rmdir()
    run(env, adapter, 'stop')
    run(env, adapter, 'status', ok=False)
    assert config.read_text().strip() == dsn

    env, home, release, adapter = fixture('managed')
    work = Path(env['FIXTURE'])
    pg = home / 'Library/Application Support/Difqo/postgres-18'
    (pg / 'data').mkdir(parents=True)
    (pg / 'data/PG_VERSION').write_text('18\n')
    (pg / 'password').write_text('a' * 64 + '\n')
    (pg / 'password').chmod(0o600)
    (pg / 'port').write_text('55432\n')
    pg_bin = release / 'postgresql/bin'
    pg_bin.mkdir(parents=True)
    executable(pg_bin / 'postgres', '#!/bin/bash\nexit 0\n')
    executable(pg_bin / 'pg_ctl', '#!/bin/bash\n[ "${MANUAL_PG:-}" = 1 ]\n')
    assert 'manually started' in run(env, adapter, 'start', ok=False, MANUAL_PG='1')
    assert not (work / 'starts').exists()
    assert 'did not become healthy' in run(env, adapter, 'start', ok=False, NO_HEALTH='1')
    assert (work / 'starts').read_text().splitlines() == ['com.difqo.postgresql', 'com.difqo.server']
    assert (work / 'stops').read_text().splitlines() == ['com.difqo.server', 'com.difqo.postgresql']
    assert not list((home / 'Library/LaunchAgents').glob('*.plist'))
    assert 'ready' in run(env, adapter, 'start')
    run(env, adapter, 'run')
    run(env, adapter, 'stop')
    assert (pg / 'password').read_text().strip() == 'a' * 64
    assert not list(work.glob('com.difqo.*'))

print('LaunchAgent smoke passed: private and external startup, path quoting, persisted settings, timeout rollback, and safe rerun.')
