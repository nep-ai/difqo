"""Check the hosted Connections executable using synthetic settings."""
from base64 import b64encode
from contextlib import closing
from pathlib import Path
from urllib.error import URLError
from urllib.parse import parse_qsl, urlencode, urlsplit, urlunsplit
from urllib.request import ProxyHandler, build_opener
from uuid import uuid4
import os
import socket
import subprocess
import sys
import time

binary = str(Path(sys.argv[1]).resolve())
dsn = os.environ["DIFQO_SLACK_RELAY_TEST_DATABASE_URL"]
env = os.environ | {
    "DIFQO_RELAY_DATABASE_URL": dsn,
    "DIFQO_CONNECTIONS_ORIGIN": "https://relay.example.test",
    "DIFQO_SLACK_APP_ID": "AFIXTURE",
    "DIFQO_SLACK_WORKSPACE_ID": "TFIXTURE",
    "DIFQO_SLACK_OPERATOR_ID": "UFIXTURE",
    "DIFQO_SLACK_CLIENT_ID": "fixture-client",
    "DIFQO_SLACK_CLIENT_SECRET": "fixture-client-secret",
    "DIFQO_SLACK_SIGNING_SECRET": "fixture-signing-secret",
    "DIFQO_SLACK_ENROLLMENT_TOKEN": "ab" * 32,
    "DIFQO_SLACK_TOKEN_KEY": b64encode(bytes([1]) * 32).decode(),
}


def expect_startup_failure(address, settings, expected):
    result = subprocess.run([binary, "-listen", address], env=settings,
                            capture_output=True, text=True, timeout=8)
    assert result.returncode != 0 and expected in result.stderr, result.stderr


expect_startup_failure("0.0.0.0:8766", env, "numeric loopback")
expect_startup_failure("localhost:8766", env, "numeric loopback")

# A search path containing only a new, nonexistent schema hides the migrated
# tables without changing or dropping anything in the configured database.
url = urlsplit(dsn)
params = [(k, v) for k, v in parse_qsl(url.query) if k not in ("options", "search_path")]
params.append(("options", "-csearch_path=connections_missing_" + uuid4().hex))
unmigrated = urlunsplit(url._replace(query=urlencode(params)))
expect_startup_failure("127.0.0.1:8766", env | {"DIFQO_RELAY_DATABASE_URL": unmigrated},
                       "relay migrations missing")

with closing(socket.socket()) as reserved:
    reserved.bind(("127.0.0.1", 0))
    address = "127.0.0.1:" + str(reserved.getsockname()[1])

opener = build_opener(ProxyHandler({}))
process = subprocess.Popen([binary, "-listen", address], env=env)
try:
    deadline = time.monotonic() + 8
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError("Connections exited before becoming ready")
        try:
            with opener.open("http://" + address + "/healthz", timeout=1) as reply:
                assert reply.status == 200
                break
        except URLError:
            time.sleep(0.05)
    else:
        raise RuntimeError("Connections liveness probe did not succeed")
    with opener.open("http://" + address + "/readyz", timeout=3) as reply:
        assert reply.status == 200
    process.terminate()
    assert process.wait(timeout=8) == 0, "Connections did not shut down cleanly"
finally:
    if process.poll() is None:
        process.kill()
        process.wait(timeout=5)

print("Connections smoke passed: loopback validation, migration gate, health, and SIGTERM shutdown.")
