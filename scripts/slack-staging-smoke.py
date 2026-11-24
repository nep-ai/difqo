"""Rehearse the pilot retention and retirement commands in a disposable schema."""
from pathlib import Path
from tempfile import NamedTemporaryFile
from urllib.parse import quote, unquote, urlsplit
from uuid import uuid4
import os
import subprocess

root = Path(__file__).resolve().parent.parent
dsn = os.environ["DIFQO_SLACK_RELAY_TEST_DATABASE_URL"]
schema = "slack_staging_" + uuid4().hex


def database_urls(dsn, password_file):
    url = urlsplit(dsn)
    authority = url.netloc
    password = url.password
    if password is not None:
        userinfo, _, host = authority.rpartition("@")
        authority = userinfo.split(":", 1)[0] + "@" + host

    params = []
    for param in filter(None, url.query.split("&")):
        key, _, value = param.partition("=")
        if unquote(key) == "password":
            password = value
        else:
            params.append(param)
    if password:
        # The temporary file belongs to this rehearsal alone. Let libpq resolve
        # its host, port, database and user from the original URI and defaults.
        password = unquote(password)
        if "\n" in password or "\r" in password:
            raise ValueError("Use a libpq password source for passwords containing line breaks")
        escaped = password.replace("\\", "\\\\").replace(":", "\\:")
        password_file.write(f"*:*:*:*:{escaped}\n")
        password_file.flush()
        params = [p for p in params if unquote(p.partition("=")[0]) != "passfile"]
        params.append("passfile=" + quote(password_file.name))

    # Keep the // even with an empty authority, as in postgresql:///dbname.
    base = f"{url.scheme}://{authority}{url.path}?"
    scoped = [p for p in params if unquote(p.partition("=")[0]) != "options"]
    scoped.append("options=" + quote("-csearch_path=" + schema))
    return base + "&".join(params), base + "&".join(scoped), password is not None


def rehearse(password_file):
    database, scoped, inline_password = database_urls(dsn, password_file)
    env = os.environ.copy()
    env.pop("DIFQO_SLACK_RELAY_TEST_DATABASE_URL", None)
    if inline_password:
        env.pop("PGPASSWORD", None)

    def sql(*args, target=scoped):
        return subprocess.run(
            ["psql", "-XqAt", "--no-password", "--dbname", target, "-v", "ON_ERROR_STOP=1", *args],
            env=env, capture_output=True, text=True, check=True,
        ).stdout.strip()

    def operation(name, *args):
        sql("-v", "app_id=ATEST", "-v", "pilot_scope=TTEST:UTEST", *args,
            "-f", str(root / "relay" / "operations" / name))

    sql("-c", "CREATE SCHEMA " + schema, target=database)
    try:
        for migration in sorted((root / "relay/migrations").glob("*.sql")):
            sql("-f", str(migration))
        sql("-c", """
            INSERT INTO slack_relay_pairings
                (app_id, id, proof_hash, expires_at, pilot_scope, claimed,
                 verified_workspace, verified_operator, installed_workspace, dm_channel)
            VALUES
                ('ATEST', repeat('a', 32), decode(repeat('ab', 32), 'hex'), now(),
                 'TTEST:UTEST', true, 'TTEST', 'UTEST', 'TTEST', 'DTEST'),
                ('ATEST', repeat('b', 32), decode(repeat('ab', 32), 'hex'), now(),
                 'TOTHER:UOTHER', true, 'TOTHER', 'UOTHER', 'TOTHER', 'DOTHER'),
                ('AOTHER', repeat('c', 32), decode(repeat('ab', 32), 'hex'), now(),
                 'TTEST:UTEST', true, 'TTEST', 'UTEST', 'TTEST', 'DTEST');
            INSERT INTO slack_relay_pairings
                (app_id, id, proof_hash, expires_at, pilot_scope, installed_workspace, sealed_bot_token)
            VALUES ('ATEST', repeat('d', 32), decode(repeat('ab', 32), 'hex'), now(),
                    'TTEST:UTEST', 'TTEST', decode('012345', 'hex'));
            INSERT INTO slack_relay_oauth_flows (app_id, pairing_id, purpose, state_hash, nonce)
            VALUES
                ('ATEST', repeat('a', 32), 'identity', decode(repeat('cd', 32), 'hex'), 'fixture'),
                ('ATEST', repeat('b', 32), 'identity', decode(repeat('ef', 32), 'hex'), 'fixture'),
                ('AOTHER', repeat('c', 32), 'identity', decode(repeat('cd', 32), 'hex'), 'fixture');
            INSERT INTO slack_relay_events (app_id, workspace_id, event_id, pairing_id, goal, queued_at)
            VALUES
                ('ATEST', 'TTEST', 'Eold', repeat('a', 32), 'fixture goal', now() - interval '25 hours'),
                ('ATEST', 'TTEST', 'Efresh', repeat('a', 32), 'fixture goal', now()),
                ('ATEST', 'TOTHER', 'Eother', repeat('b', 32), 'fixture goal', now() - interval '25 hours'),
                ('AOTHER', 'TTEST', 'Eotherapp', repeat('c', 32), 'fixture goal', now() - interval '25 hours');
        """)
        operation("expire-slack-goals.sql", "-v", "retention_hours=24")
        assert sql("-c", "SELECT event_id FROM slack_relay_events WHERE goal IS NULL") == "Eold"
        for _ in range(2):
            operation("retire-slack-pilot.sql")
        assert sql("-c", "SELECT event_id FROM slack_relay_events ORDER BY event_id") == "Efresh\nEold\nEother\nEotherapp"
        assert sql("-c", "SELECT event_id FROM slack_relay_events WHERE goal IS NOT NULL ORDER BY event_id") == "Eother\nEotherapp"
        assert sql("-c", """
            SELECT count(*) FROM slack_relay_pairings
            WHERE app_id = 'ATEST' AND pilot_scope = 'TTEST:UTEST'
              AND revoked AND NOT claimed AND NOT approval_pending
              AND verified_workspace = '' AND verified_operator = ''
              AND installed_workspace = '' AND dm_channel = '' AND sealed_bot_token IS NULL
        """) == "2"
        assert sql("-c", """
            SELECT app_id || ':' || left(pairing_id, 1) FROM slack_relay_oauth_flows ORDER BY app_id
        """) == "AOTHER:c\nATEST:b"
        assert sql("-c", """
            SELECT app_id || ':' || pilot_scope FROM slack_relay_pairings WHERE claimed AND NOT revoked ORDER BY app_id
        """) == "AOTHER:TTEST:UTEST\nATEST:TOTHER:UOTHER"
    finally:
        sql("-c", "DROP SCHEMA " + schema + " CASCADE", target=database)


with NamedTemporaryFile(mode="w") as password_file:
    rehearse(password_file)

print("Slack staging smoke passed: scoped expiry, retirement, replay IDs, and repeated rollback.")
