"""Exercise the installed setup command against two fresh disposable databases."""
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from shutil import copy2, copytree
from tempfile import TemporaryDirectory
import os
import subprocess
import sys


if len(sys.argv) != 3:
    raise SystemExit('Usage: python3 scripts/database-setup-smoke.py DIFQO_BINARY MIGRATIONS_DIR')
executable = Path(sys.argv[1]).resolve(strict=True)
migrations = sorted(Path(sys.argv[2]).resolve(strict=True).glob('*.sql'))
assert len(migrations) >= 2
fresh = os.environ['DIFQO_FRESH_DATABASE_URL']
legacy = os.environ['DIFQO_LEGACY_DATABASE_URL']
assert fresh != legacy


def query(dsn, sql):
    return subprocess.check_output(
        ['psql', '-XqAt', '-v', 'ON_ERROR_STOP=1', dsn, '-c', sql], text=True, timeout=15,
    ).strip()


for dsn in (fresh, legacy):
    assert query(dsn, "SELECT count(*) FROM pg_class JOIN pg_namespace ON relnamespace = pg_namespace.oid WHERE nspname = 'public'") == '0'

with TemporaryDirectory(prefix='difqo-db-setup-') as scratch:
    root = Path(scratch)
    installed = root / 'release with spaces'
    (installed / 'bin').mkdir(parents=True)
    binary = installed / 'bin' / executable.name
    copy2(executable, binary)
    copytree(migrations[0].parent, installed / 'migrations')

    def setup(dsn, *args, expected=0):
        result = subprocess.run(
            [binary, 'setup-database', *args],
            env={**os.environ, 'DIFQO_DATABASE_URL': dsn},
            cwd=root, text=True, capture_output=True, timeout=30,
        )
        assert result.returncode == expected, (result.returncode, result.stdout, result.stderr)
        return result.stdout + result.stderr

    # The full pending batch and its ledger roll back even if a supplied file
    # tries to end the transaction. The next setup can safely retry.
    failing = root / 'failing'
    copytree(installed / 'migrations', failing)
    (failing / f'{len(migrations) + 1:04d}_fixture.sql').write_text(
        'BEGIN;\nCREATE TABLE would_be_partial (id integer);\nCOMMIT;\nCOMMIT;\n',
    )
    assert 'apply ' in setup(fresh, '-migrations', str(failing), expected=1)
    assert query(fresh, "SELECT to_regclass('tasks') IS NULL AND to_regclass('difqo_schema_migrations') IS NULL AND to_regclass('would_be_partial') IS NULL") == 't'

    with ThreadPoolExecutor(max_workers=2) as clients:
        results = list(clients.map(setup, (fresh, fresh)))
    assert sorted(results) == sorted([
        f'PostgreSQL is ready. Applied {len(migrations)} migration(s).\n',
        'PostgreSQL is ready. Applied 0 migration(s).\n',
    ]), results
    assert query(fresh, 'SELECT count(*) FROM difqo_schema_migrations') == str(len(migrations))
    assert query(fresh, "INSERT INTO tasks (goal) VALUES ('preserve fresh goal') RETURNING goal") == 'preserve fresh goal'
    assert 'Applied 0 migration(s)' in setup(fresh)
    assert query(fresh, 'SELECT goal FROM tasks') == 'preserve fresh goal'

    tampered = installed / 'migrations' / migrations[0].name
    tampered.write_text(tampered.read_text().replace('\nCOMMIT;', '\n-- change to an applied migration\nCOMMIT;'))
    assert 'history does not match' in setup(fresh, expected=1)
    copy2(migrations[0], tampered)
    assert query(fresh, 'SELECT goal FROM tasks') == 'preserve fresh goal'

    subprocess.run(
        ['psql', '-Xq', '-v', 'ON_ERROR_STOP=1', legacy, '-f', migrations[0]],
        check=True, timeout=15,
    )
    assert query(legacy, "INSERT INTO tasks (goal) VALUES ('preserve legacy goal') RETURNING goal") == 'preserve legacy goal'
    assert query(legacy, "INSERT INTO attempts (task_id, completed_at, exit_code, output) VALUES (1, now(), 0, 'preserve result') RETURNING output") == 'preserve result'
    assert 'no Difqo migration history' in setup(legacy, expected=1)
    assert query(legacy, "SELECT to_regclass('difqo_schema_migrations') IS NULL") == 't'

    assert 'apply ' in setup(legacy, '-baseline', migrations[0].name, '-migrations', str(failing), expected=1)
    assert query(legacy, "SELECT to_regclass('difqo_schema_migrations') IS NULL AND to_regclass('project_decisions') IS NULL") == 't'
    assert query(legacy, 'SELECT goal FROM tasks') == 'preserve legacy goal'
    assert query(legacy, 'SELECT output FROM attempts') == 'preserve result'

    assert f'Applied {len(migrations) - 1} migration(s)' in setup(legacy, '-baseline', migrations[0].name)
    assert 'Applied 0 migration(s)' in setup(legacy, '-baseline', migrations[0].name)
    assert query(legacy, 'SELECT count(*) FROM difqo_schema_migrations') == str(len(migrations))
    assert query(legacy, 'SELECT goal FROM tasks') == 'preserve legacy goal'
    assert query(legacy, 'SELECT output FROM attempts') == 'preserve result'

print('Database setup smoke passed: fresh setup, concurrent reruns, legacy adoption, data preservation, checksum drift, rollback, and retry.')
