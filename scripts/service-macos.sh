#!/usr/bin/env bash
set +x
set -euo pipefail
umask 077

fail() {
  printf 'Difqo service: %s\n' "$*" >&2
  exit 1
}

bin=$(cd "$(dirname "$0")" && pwd)
[ "$(uname -s)" = Darwin ] && [ "$(uname -m)" = arm64 ] || fail 'Apple silicon macOS is required.'
[[ "${HOME:-}" == /* && "$HOME" != / && -d "$HOME" ]] || fail 'HOME must be an existing per-user directory.'
uid=$(id -u)
[ "$uid" -ne 0 ] || fail 'Run as the current user without sudo.'
support="$HOME/Library/Application Support/Difqo"
state="$support/service"
agents="$HOME/Library/LaunchAgents"
domain="gui/$uid"
server_label=com.difqo.server
database_label=com.difqo.postgresql
url=http://127.0.0.1:8765
database="$support/postgres-18"
paused_file="$state/paused"
private_file() {
  [ -f "$1" ] && [ ! -L "$1" ] && [ "$(stat -f '%u:%Lp' "$1")" = "$uid:600" ]
}

# The launch job receives paths and mode only. Keep credentials out of its plist.
if [ "${1:-}" = run ] && [ "$#" -eq 1 ]; then
  private_file "$state/mode" || fail 'Service database mode is missing or insecure.'
  case "$(cat "$state/mode")" in
    external)
      private_file "$state/database-url" || fail 'External database configuration is missing or insecure.'
      DIFQO_DATABASE_URL=$(cat "$state/database-url")
      ;;
    managed)
      private_file "$database/password" || fail 'Managed database password is missing or insecure.'
      password=$(cat "$database/password")
      port=$(cat "$database/port")
      [[ "$password" =~ ^[a-f0-9]{64}$ && "$port" =~ ^[0-9]{4,5}$ ]] || fail 'Managed database configuration is invalid.'
      DIFQO_DATABASE_URL="postgres://difqo:$password@127.0.0.1:$port/difqo?sslmode=disable"
      ;;
    *) fail 'Unknown service database mode.' ;;
  esac
  export DIFQO_DATABASE_URL
  exec "$bin/difqo" server -web "$bin/../web" -artifacts "$support/artifacts"
fi

[ "$#" -eq 1 ] || fail 'Usage: service-macos start|stop|status|pause|restart'
action=$1
case "$action" in
  start|stop|status|pause|restart) ;;
  *) fail 'Usage: service-macos start|stop|status|pause|restart' ;;
esac

loaded() {
  launchctl print "$domain/$1" >/dev/null 2>&1
}

healthy() {
  [ "$(curl --noproxy '*' -fsS --max-time 1 -o /dev/null -w '%{http_code}' "$url/tasks" 2>/dev/null)" = 200 ]
}

launchctl print "$domain" >/dev/null 2>&1 || fail 'Run from a logged-in macOS graphical session.'
read_paused() {
  paused=false
  if [ -e "$paused_file" ] || [ -L "$paused_file" ]; then
    private_file "$paused_file" || fail 'Service pause marker is insecure.'
    paused=true
  fi
}

if [ "$action" = status ]; then
  read_paused
  if "$paused"; then
    if [ "$(cat "$paused_file")" != paused ] || loaded "$server_label" || loaded "$database_label"; then
      fail 'Unhealthy. Pause is incomplete. Run service-macos pause to finish.'
    fi
    fail 'Paused. Run service-macos start to resume.'
  fi
  if ! loaded "$server_label"; then
    if loaded "$database_label"; then fail 'Unhealthy. PostgreSQL is loaded but the server is unloaded.'; fi
    fail 'Unloaded. Run service-macos start.'
  fi
  private_file "$state/mode" || fail 'Unhealthy. Service database mode is missing or insecure.'
  if [ "$(cat "$state/mode")" = managed ] && ! loaded "$database_label"; then
    fail 'Unhealthy. The managed PostgreSQL agent is unloaded.'
  fi
  if ! healthy; then fail "Unhealthy. Check the logs under $state."; fi
  printf 'Difqo is running and ready at %s\n' "$url"
  exit
fi

for directory in "$HOME/Library" "$HOME/Library/Application Support" "$support" "$state" "$support/artifacts" "$agents"; do
  [ ! -L "$directory" ] || fail "Directory is a symbolic link: $directory"
  mkdir -p "$directory"
done
[ "$(stat -f '%u:%Lp' "$state")" = "$uid:700" ] || fail 'Service configuration requires a private directory owned by the current user.'
lock="$state/.service-operation.lock"
if [ -L "$lock" ] || { [ -e "$lock" ] && [ ! -f "$lock" ]; }; then
  fail 'Service lock must be a regular file.'
fi
# Keep the lock on this shell's descriptor. The kernel releases it when the
# last holder exits, even after a forced stop. Keep the file itself in place.
exec 9>> "$lock"
/usr/bin/lockf -s -t 0 9 || fail 'Another service operation holds the lock. Retry after it finishes.'
resuming=false
stage=
started_server=false
started_database=false
wrote_server=false
wrote_database=false
cleanup() {
  local result=$?
  if [ "$result" -ne 0 ]; then
    if "$started_server" && loaded "$server_label"; then launchctl bootout "$domain/$server_label" || true; fi
    if "$started_database" && loaded "$database_label"; then launchctl bootout "$domain/$database_label" || true; fi
    if "$wrote_server"; then rm -f "$agents/$server_label.plist"; fi
    if "$wrote_database"; then rm -f "$agents/$database_label.plist"; fi
    if "$resuming" && disable_jobs && ! loaded "$server_label" && ! loaded "$database_label"; then
      mark_pause paused
    fi
  fi
  if [ -n "$stage" ]; then rm -rf "$stage"; fi
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

stop_jobs() {
  if loaded "$server_label"; then launchctl bootout "$domain/$server_label"; fi
  if [ "$1" = managed ] && loaded "$database_label"; then launchctl bootout "$domain/$database_label"; fi
}

disable_jobs() {
  launchctl disable "$domain/$server_label" || return
  if [ "$mode" = managed ]; then launchctl disable "$domain/$database_label"; fi
}

mark_pause() {
  printf '%s\n' "$1" > "$stage/pause"
  mv "$stage/pause" "$paused_file"
}

if [ "$action" = stop ]; then
  # Stop both owned jobs even when saved configuration is unavailable.
  stop_jobs managed
  printf 'Difqo services stopped. Settings and data are preserved.\n'
  exit
fi

read_paused
stage=$(mktemp -d "$state/.service-stage.XXXXXX")
if [ "$action" = pause ]; then
  private_file "$state/mode" || fail 'No saved service configuration to pause.'
  mode=$(cat "$state/mode")
  [[ "$mode" = managed || "$mode" = external ]] || fail 'Unknown service database mode.'
  # Save the intent first so an interrupted pause can be retried or resumed.
  mark_pause pending
  disable_jobs
  stop_jobs "$mode"
  mark_pause paused
  printf 'Difqo is paused. Settings and data are preserved.\n'
  exit
fi

if ! loaded "$server_label" && nc -z -w 1 127.0.0.1 8765 >/dev/null 2>&1; then
  fail 'Port 8765 is already occupied.'
fi

mode=managed
if [ -e "$state/mode" ] || [ -L "$state/mode" ]; then
  private_file "$state/mode" || fail 'Service database mode is insecure.'
  mode=$(cat "$state/mode")
  if [ -n "${DIFQO_DATABASE_URL:-}" ] && { [ "$mode" != external ] ||
    ! private_file "$state/database-url" || [ "$(cat "$state/database-url")" != "$DIFQO_DATABASE_URL" ]; }; then
    fail 'The supplied URL conflicts with the saved database configuration.'
  fi
elif [ -n "${DIFQO_DATABASE_URL:-}" ]; then
  # Confirm the explicitly selected database before saving its configuration.
  "$bin/difqo" setup-database
  mode=external
  printf '%s\n' "$DIFQO_DATABASE_URL" > "$stage/database-url"
  if [ -e "$state/database-url" ] || [ -L "$state/database-url" ]; then
    private_file "$state/database-url" && cmp -s "$state/database-url" "$stage/database-url" ||
      fail 'Existing database URL file conflicts with the supplied configuration.'
  else
    mv "$stage/database-url" "$state/database-url"
  fi
elif [ -e "$state/database-url" ] || [ -L "$state/database-url" ]; then
  fail 'An external database URL was saved during an earlier start. Supply that same URL to resume.'
fi
case "$mode" in
  managed)
    postgres="$bin/../postgresql/bin/postgres"
    pg_ctl="$bin/../postgresql/bin/pg_ctl"
    [ -x "$postgres" ] && [ -x "$pg_ctl" ] && [ -f "$database/data/PG_VERSION" ] &&
      private_file "$database/password" || fail 'Provision the bundled PostgreSQL cluster before starting the service.'
    if ! loaded "$database_label" && "$pg_ctl" -D "$database/data" status >/dev/null 2>&1; then
      fail 'Stop the manually started Difqo PostgreSQL cluster before loading its LaunchAgent.'
    fi
    ;;
  external)
    private_file "$state/database-url" || fail 'External database configuration is missing or insecure.'
    ;;
  *) fail 'Unknown service database mode.' ;;
esac
if [ ! -f "$state/mode" ]; then
  printf '%s\n' "$mode" > "$stage/mode"
  mv "$stage/mode" "$state/mode"
fi

write_agent() {
  local label=$1 file target arg index=0
  shift
  file="$stage/$label.plist"
  target="$agents/$label.plist"
  /usr/bin/plutil -create xml1 "$file"
  /usr/bin/plutil -insert Label -string "$label" "$file"
  /usr/bin/plutil -insert ProgramArguments -array "$file"
  for arg in "$@"; do
    /usr/bin/plutil -insert "ProgramArguments.$index" -string "$arg" "$file"
    index=$((index + 1))
  done
  /usr/bin/plutil -insert RunAtLoad -bool YES "$file"
  /usr/bin/plutil -insert KeepAlive -bool YES "$file"
  /usr/bin/plutil -insert StandardOutPath -string "$state/$label.log" "$file"
  /usr/bin/plutil -insert StandardErrorPath -string "$state/$label.log" "$file"
  # launchd does not promise to inherit the shell's HOME.
  /usr/bin/plutil -insert EnvironmentVariables -dictionary "$file"
  /usr/bin/plutil -insert EnvironmentVariables.HOME -string "$HOME" "$file"
  if [ "$label" = "$server_label" ]; then
    /usr/bin/plutil -insert EnvironmentVariables.PATH -string \
      '/usr/bin:/bin:/usr/sbin:/sbin:/opt/homebrew/bin:/usr/local/bin:/Applications/Docker.app/Contents/Resources/bin' "$file"
  fi
  if [ -e "$target" ] || [ -L "$target" ]; then
    private_file "$target" && cmp -s "$file" "$target" || fail "Existing LaunchAgent differs: $target"
  else
    mv "$file" "$target"
    if [ "$label" = "$server_label" ]; then wrote_server=true; else wrote_database=true; fi
  fi
}

write_agent "$server_label" /bin/bash "$bin/service-macos" run
if [ "$mode" = managed ]; then
  write_agent "$database_label" "$postgres" -D "$database/data"
fi
if "$paused"; then
  resuming=true
  mark_pause pending
fi
if [ "$action" = restart ] || "$paused"; then stop_jobs "$mode"; fi
if "$paused"; then
  if [ "$mode" = managed ]; then launchctl enable "$domain/$database_label"; fi
  launchctl enable "$domain/$server_label"
fi
if [ "$mode" = managed ] && ! loaded "$database_label"; then
  started_database=true
  launchctl bootstrap "$domain" "$agents/$database_label.plist"
fi
if ! loaded "$server_label"; then
  started_server=true
  launchctl bootstrap "$domain" "$agents/$server_label.plist"
fi

for ((attempt=0; attempt<40; attempt++)); do
  if healthy; then
    if "$resuming"; then rm "$paused_file"; fi
    printf 'Difqo is running and ready at %s\n' "$url"
    exit 0
  fi
  sleep 1
done
fail "Difqo did not become healthy. Check the logs under $state."
