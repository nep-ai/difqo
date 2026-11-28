"""Exercise the packaged helper without starting Docker or signing in to Codex."""

import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


def executable(path, body):
    path.write_text("#!/bin/bash\nset -euo pipefail\n" + body)
    path.chmod(0o700)


with tempfile.TemporaryDirectory(prefix="difqo-first-goal-") as temporary:
    scratch = Path(temporary)
    home = scratch / "Pilot Home"
    support = home / "Library/Application Support/Difqo"
    auth_home = support / "codex-home"
    bin_dir = scratch / "bin"
    bin_dir.mkdir()
    auth_home.mkdir(parents=True, mode=0o700)
    helper = bin_dir / "first-goal-macos"
    shutil.copy2(sys.argv[1], helper)
    calls = scratch / "worker-args"
    docker_calls = scratch / "docker-args"
    env = {
        **os.environ,
        "HOME": str(home),
        "PATH": f"{bin_dir}:/usr/bin:/bin",
        "WORKER_CALLS": str(calls),
        "DOCKER_CALLS": str(docker_calls),
    }
    executable(bin_dir / "docker", """
printf '%s\n' "$*" >> "$DOCKER_CALLS"
case "$1" in
  info|build|image) exit 0 ;;
  run)
    if [ "${LOGIN_FAIL:-0}" = 1 ]; then exit 34; fi
    printf 'Logged in using ChatGPT\n'
    ;;
  *) exit 99 ;;
esac
""")
    executable(bin_dir / "difqo", """
printf '%s\n' "$@" > "$WORKER_CALLS"
[[ "$1" = worker ]]
case "$2" in
  -preflight) fixture=$4 ;;
  -task) fixture=$5 ;;
  *) exit 99 ;;
esac
test "$(git -C "$fixture" show HEAD:greeting.txt)" = before
exit "${WORKER_EXIT:-0}"
""")

    def run(*args, **overrides):
        return subprocess.run(
            ["/bin/bash", str(helper), *args], env={**env, **overrides},
            capture_output=True, text=True, timeout=15,
        )

    denied_login = run("prepare", LOGIN_FAIL="1")
    assert denied_login.returncode == 34, denied_login
    assert "--device-auth" in docker_calls.read_text()
    assert not calls.exists(), "worker ran despite failed device sign-in"

    auth_home.chmod(0o755)
    insecure = run("prepare")
    assert insecure.returncode == 1, insecure
    assert "must be a private directory" in insecure.stderr, insecure
    assert not calls.exists(), "worker ran with an insecure credential directory"
    auth_home.chmod(0o700)

    preflight = run("prepare", WORKER_EXIT="1")
    assert preflight.returncode == 1, preflight
    assert "Read the worker error above" in preflight.stderr, preflight
    assert calls.read_text().splitlines()[:3] == ["worker", "-preflight", "-repo"]
    assert not list(support.glob("first-goal.*")), "preflight left a fixture"

    prepared = run("prepare")
    assert prepared.returncode == 0, prepared
    assert "Worker preflight passed" in prepared.stdout, prepared

    selected = run("run", "37")
    assert selected.returncode == 0, selected
    args = calls.read_text().splitlines()
    assert args[:4] == ["worker", "-task", "37", "-repo"], args
    assert Path(args[4]).resolve().parent == support.resolve(), args
    assert not Path(args[4]).exists(), "worker left a fixture"
    assert args[-2:] == ["-check", 'test "$(cat greeting.txt)" = after'], args
    assert "Return to Chief of Staff" in selected.stdout, selected

print("Packaged first-goal helper passed offline smoke checks.")
