#!/usr/bin/env bash
set -euo pipefail

# Run only on a disposable GitHub-hosted Mac, with its own launchd session.
[ "${GITHUB_ACTIONS:-}" = true ] && [ "${RUNNER_ENVIRONMENT:-}" = github-hosted ] ||
  { printf 'Live smoke requires a disposable GitHub-hosted runner.\n' >&2; exit 1; }
if ! launchctl print "gui/$(id -u)" >/dev/null 2>&1; then
  if [ "${REQUIRE_LIVE_SMOKE:-false}" = true ]; then
    printf '::error::A graphical login domain is required to verify a staging candidate.\n'
    exit 1
  fi
  printf '::warning::No graphical login domain. Live LaunchAgent and verified-goal acceptance still need a clean Mac.\n'
  exit 0
fi
for label in com.difqo.server com.difqo.postgresql; do
  if launchctl print "gui/$(id -u)/$label" >/dev/null 2>&1; then
    printf 'Unexpected pre-existing LaunchAgent: %s\n' "$label" >&2
    exit 1
  fi
done

artifact=$(cd "$1" && pwd)
version=$2
scratch=$(mktemp -d "${RUNNER_TEMP:-/tmp}/difqo-pilot-live.XXXXXX")
export HOME="$scratch/Synthetic Home" DIFQO_PILOT_NO_OPEN=1
mkdir "$HOME"
unset DIFQO_DATABASE_URL
release="$HOME/Library/Application Support/Difqo/releases/difqo-$version-darwin-arm64"
service="$release/bin/service-macos"
cleanup() {
  local result=$?
  if [ -x "$service" ]; then
    "$service" stop || result=1
    for label in com.difqo.server com.difqo.postgresql; do
      launchctl enable "gui/$(id -u)/$label" || result=1
    done
  fi
  rm -rf "$scratch"
  exit "$result"
}
trap cleanup EXIT

/bin/bash "$artifact/start-pilot.sh"
"$service" status
url=http://127.0.0.1:8765
curl --noproxy '*' -fsS --max-time 8 "$url/" | grep -F 'id="root"' >/dev/null
curl --noproxy '*' -fsS --max-time 8 "$url/tasks" \
  -H 'Content-Type: application/json' -d '{"goal":"Leave this earlier goal queued"}' > "$scratch/earlier-goal"
curl --noproxy '*' -fsS --max-time 8 "$url/tasks" \
  -H 'Content-Type: application/json' -d '{"goal":"Verify the synthetic pilot repository"}' > "$scratch/goal"
task_id=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])' < "$scratch/goal")
git init -q "$scratch/repo"
git -C "$scratch/repo" -c user.name=Fixture -c user.email=fixture@example.invalid \
  -c commit.gpgSign=false commit --allow-empty -qm 'Synthetic pilot'
"$release/bin/difqo" worker -repo "$scratch/repo" -task "$task_id"
earlier_id=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])' < "$scratch/earlier-goal")
curl --noproxy '*' -fsS --max-time 8 "$url/tasks/$earlier_id" |
  python3 -c 'import json,sys; assert json.load(sys.stdin)["status"] == "queued"'
pidfile="$HOME/Library/Application Support/Difqo/postgres-18/data/postmaster.pid"
old_pid=$(head -n 1 "$pidfile")
"$service" restart
test "$old_pid" != "$(head -n 1 "$pidfile")"
"$service" pause
if "$service" status; then exit 1; fi
"$service" restart
/bin/bash "$artifact/start-pilot.sh"
curl --noproxy '*' -fsS --max-time 8 "$url/tasks/$task_id" > "$scratch/result"
expected=$(git -C "$scratch/repo" rev-parse HEAD)
python3 -c 'import json,sys; r=json.load(sys.stdin)["attempt"]["result"]; assert r["exit_code"] == 0 and r["output"] == sys.argv[1]' \
  "$expected" < "$scratch/result"
printf 'Live startup, pause, restart, and fixed repository check passed.\n'
