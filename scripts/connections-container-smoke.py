"""Exercise the Connections image and migrations using private synthetic PostgreSQL."""
from base64 import b64encode
from hashlib import sha256
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.request import ProxyHandler, Request, build_opener
from uuid import uuid4
import hmac
import json
import os
import subprocess
import sys
import time

engine = os.environ.get("CONTAINER_RUNTIME", "docker")
image = sys.argv[1]
network = "connections-smoke-" + uuid4().hex[:10]
database, relay = network + "-db", network + "-relay"
proof, pairing, enrollment = "cd" * 32, "ab" * 16, "ef" * 32
signing_secret = "fixture-slack-signing-secret"
env = os.environ | {
    "PORT": "8080",
    "DIFQO_RELAY_DATABASE_URL": "postgres://postgres:fixture@postgres:5432/postgres?sslmode=disable",
    "DIFQO_CONNECTIONS_ORIGIN": "https://relay.example.test",
    "DIFQO_SLACK_APP_ID": "AFIXTURE",
    "DIFQO_SLACK_WORKSPACE_ID": "TFIXTURE",
    "DIFQO_SLACK_OPERATOR_ID": "UFIXTURE",
    "DIFQO_SLACK_CLIENT_ID": "fixture",
    "DIFQO_SLACK_CLIENT_SECRET": "fixture-slack-client-secret",
    "DIFQO_SLACK_SIGNING_SECRET": signing_secret,
    "DIFQO_SLACK_ENROLLMENT_TOKEN": enrollment,
    "DIFQO_SLACK_TOKEN_KEY": b64encode(bytes([1]) * 32).decode(),
}
flags = [part for key in (
    "PORT", "DIFQO_RELAY_DATABASE_URL", "DIFQO_CONNECTIONS_ORIGIN", "DIFQO_SLACK_APP_ID",
    "DIFQO_SLACK_WORKSPACE_ID", "DIFQO_SLACK_OPERATOR_ID", "DIFQO_SLACK_CLIENT_ID",
    "DIFQO_SLACK_CLIENT_SECRET", "DIFQO_SLACK_SIGNING_SECRET", "DIFQO_SLACK_ENROLLMENT_TOKEN",
    "DIFQO_SLACK_TOKEN_KEY",
) for part in ("--env", key)]
opener = build_opener(ProxyHandler({}))


def container(*args, check=True):
    return subprocess.run([engine, *args], env=env, stdout=subprocess.PIPE, text=True,
                          check=check, timeout=90)


def wait_for(check, reason):
    deadline = time.monotonic() + 25
    while time.monotonic() < deadline:
        if check():
            return
        time.sleep(0.2)
    raise RuntimeError(reason)


def http(path, method="GET", body=None, headers=None):
    # Docker can allocate a different host port after restarting a container.
    published = container("port", relay, "8080/tcp", check=False)
    if published.returncode != 0 or not published.stdout.strip():
        return 0, b""
    origin = "http://" + published.stdout.strip()
    req = Request(origin + path, data=body, headers=headers or {}, method=method)
    try:
        with opener.open(req, timeout=2) as response:
            return response.status, response.read()
    except HTTPError as error:
        return error.code, error.read()
    except URLError:
        return 0, b""


container("network", "create", network)
try:
    container("run", "-d", "--network", network, "--network-alias", "postgres",
              "--name", database, "-e", "POSTGRES_PASSWORD=fixture", "postgres:18.6")
    wait_for(lambda: container("exec", database, "pg_isready", "-h", "127.0.0.1",
                               "-U", "postgres", check=False).returncode == 0,
             "PostgreSQL did not become ready")
    for _ in range(2):
        container("run", "--rm", "--network", network, *flags, image, "/connections", "-migrate")
    count = len(list((Path(__file__).resolve().parents[1] / "relay/migrations").glob("*.sql")))
    assert container("exec", database, "psql", "-U", "postgres", "-Atc",
                     "SELECT count(*) FROM difqo_relay_migrations").stdout.strip() == str(count)
    container("run", "-d", "--network", network, "--name", relay,
              "-p", "127.0.0.1::8080", *flags, image)
    wait_for(lambda: http("/readyz")[0] == 200, "Railway ingress did not become ready")
    assert http("/healthz")[0] == 200

    stamp = str(int(time.time()))
    body = '{ "type" : "url_verification",\n "challenge" : "signé" }'.encode()
    signature = hmac.new(signing_secret.encode(), b"v0:" + stamp.encode() + b":" + body, sha256)
    headers = {"Content-Type": "application/json", "X-Slack-Request-Timestamp": stamp,
               "X-Slack-Signature": "v0=" + signature.hexdigest()}
    assert http("/slack/events", "POST", body + b" ", headers)[0] == 401
    assert http("/slack/events", "POST", body, headers) == (200, "signé".encode())

    enrollment_body = json.dumps({"pairing_id": pairing, "proof": proof}).encode()
    assert http("/slack/enroll", "POST", enrollment_body)[0] == 401
    assert http("/slack/enroll", "POST", enrollment_body, {"Authorization": "Bearer " + enrollment})[0] == 204
    path = "/pairings/" + pairing
    bearer = {"Authorization": "Bearer " + proof}
    assert http(path, "PUT", headers=bearer)[0] == 200
    container("restart", relay)
    wait_for(lambda: http("/readyz")[0] == 200, "Railway ingress did not recover after restart")
    assert http(path, "PUT", headers=bearer)[0] == 200
    assert http(path, "DELETE", headers=bearer)[0] == 204
finally:
    container("rm", "-fv", relay, database, check=False)
    container("network", "rm", network, check=False)

print("Connections image passed migration replay, signed ingress, enrollment, restart, and revocation.")
