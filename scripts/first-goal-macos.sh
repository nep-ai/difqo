#!/bin/bash
set -euo pipefail
umask 077

fail() {
  printf 'Difqo worker setup: %s\n' "$*" >&2
  exit 1
}

case "${1:-}" in
  prepare) [ "$#" -eq 1 ] || fail 'Usage: first-goal-macos prepare' ;;
  run) [ "$#" -eq 2 ] && [[ "$2" =~ ^[1-9][0-9]*$ ]] || fail 'Usage: first-goal-macos run GOAL_ID' ;;
  *) fail 'Use first-goal-macos prepare or first-goal-macos run GOAL_ID.' ;;
esac

[ "$(uname -s)" = Darwin ] && [ "$(uname -m)" = arm64 ] || fail 'Use a native Apple silicon Mac Terminal.'
[ "$(id -u)" -ne 0 ] || fail 'Use the account that installed Difqo.'
bin=$(cd "$(dirname "$0")" && pwd)
support="$HOME/Library/Application Support/Difqo"
auth_home="$support/codex-home"
image=difqo-codex:0.171.0

# Use the same default Docker configuration the LaunchAgent can see for reruns.
unset DOCKER_HOST DOCKER_CONTEXT DOCKER_CONFIG
export PATH="$PATH:/Applications/Docker.app/Contents/Resources/bin"
command -v docker >/dev/null || fail 'Install Docker Desktop and open it, then retry.'
docker info >/dev/null 2>&1 || fail 'Start Docker Desktop, wait for its engine, then retry.'
git --version >/dev/null || fail 'Install the macOS Command Line Tools with xcode-select --install, then retry.'
[ -d "$support" ] || fail 'Start the installed Difqo app before setting up the worker.'
mkdir -p "$auth_home"
[ ! -L "$auth_home" ] && [ "$(stat -f '%u:%Lp' "$auth_home")" = "$(id -u):700" ] ||
  fail 'The dedicated codex-home must be a private directory owned by your account.'

codex_login() {
  docker run --rm --pull=never --user "$(id -u):$(id -g)" \
    --env HOME=/tmp --env CODEX_HOME=/codex-home \
    --mount "type=bind,src=$auth_home,dst=/codex-home" \
    "$image" codex login "$@"
}

if [ "$1" = prepare ]; then
  docker build -t "$image" "$bin/../containers/codex"
  if ! codex_login status; then
    codex_login -c 'cli_auth_credentials_store="file"' --device-auth
    codex_login status
  fi
  selector=(-preflight)
else
  docker image inspect "$image" >/dev/null 2>&1 || fail 'Run the setup command in Chief of Staff to build the Codex image.'
  codex_login status || fail 'Run the setup command in Chief of Staff to sign in to Codex.'
  selector=(-task "$2")
fi

fixture=$(mktemp -d "$support/first-goal.XXXXXX")
trap 'rm -rf "$fixture"' EXIT
git -C "$fixture" init -q --template=
printf 'before\n' > "$fixture/greeting.txt"
git -C "$fixture" add greeting.txt
git -C "$fixture" -c user.name='Difqo Pilot' -c user.email=pilot@example.invalid \
  -c commit.gpgSign=false -c core.hooksPath=/dev/null commit -qm 'Synthetic pilot repository'

"$bin/difqo" worker "${selector[@]}" -repo "$fixture" -container-runtime docker \
  -codex-image "$image" -codex-home "$auth_home" -artifacts "$support/artifacts" \
  -check 'test "$(cat greeting.txt)" = after' ||
  fail 'Read the worker error above. For sandbox errors, follow the Docker sandbox link in Chief of Staff before retrying.'
if [ "$1" = prepare ]; then
  printf 'Worker preflight passed. Return to Chief of Staff to queue the sample goal.\n'
else
  printf 'Return to Chief of Staff to see the result and rerun the check.\n'
fi
