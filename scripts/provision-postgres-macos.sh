#!/usr/bin/env bash
set +x
set -euo pipefail
umask 077

fail() {
  printf 'Difqo PostgreSQL: %s\n' "$*" >&2
  exit 1
}

difqo="$(cd "$(dirname "$0")" && pwd)/difqo"
if [ -n "${DIFQO_DATABASE_URL:-}" ]; then
  exec "$difqo" setup-database "$@"
fi
[ "$#" -eq 1 ] || fail 'Supply the trusted PostgreSQL 18 bin directory, or set DIFQO_DATABASE_URL for an existing database.'
[ "$(uname -s)" = Darwin ] && [ "$(uname -m)" = arm64 ] || fail 'Local provisioning requires Apple silicon macOS.'
[[ "${HOME:-}" == /* && "$HOME" != / && -d "$HOME" ]] || fail 'HOME must be an existing per-user directory.'
[ "$(id -u)" -ne 0 ] || fail 'Run provisioning as the current user without sudo.'
pgbin=$(cd "$1" && pwd -P) || fail 'Could not open the PostgreSQL bin directory.'
for tool in postgres initdb pg_ctl psql; do
  [ -x "$pgbin/$tool" ] || fail "Expected executable: $pgbin/$tool"
done
[[ "$("$pgbin/postgres" --version)" =~ ^postgres\ \(PostgreSQL\)\ 18\.[0-9]+([[:space:]]|$) ]] ||
  fail 'Local provisioning requires PostgreSQL major version 18.'

support="$HOME/Library/Application Support/Difqo"
for directory in "$HOME/Library" "$HOME/Library/Application Support" "$support"; do
  [ ! -L "$directory" ] || fail "Directory is a symbolic link: $directory"
  mkdir -p "$directory"
done

root="$support/postgres-18"
lock="$support/.postgres-18.lock"
mkdir "$lock" || fail 'Another provisioning run holds the lock. Remove the empty lock only after confirming provisioning has stopped.'
stage=
socket=
started=false
pg() {
  env -i HOME="$lock" PATH=/usr/bin:/bin LC_ALL=C "$pgbin/$1" "${@:2}"
}
cleanup() {
  local result=$?
  if "$started" && pg pg_ctl -D "$root/data" status >/dev/null; then
    pg pg_ctl -D "$root/data" -m fast -w -t 30 stop >/dev/null || result=1
  fi
  if [ -n "$socket" ]; then rmdir "$socket" || result=1; fi
  if [ -n "$stage" ]; then rm -rf "$stage" || result=1; fi
  rmdir "$lock" || result=1
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

port=${DIFQO_POSTGRES_PORT:-55432}
valid_port() {
  [[ "$1" =~ ^[1-9][0-9]{0,4}$ ]] && [ "$1" -ge 1024 ] && [ "$1" -le 65535 ]
}
valid_port "$port" || fail 'DIFQO_POSTGRES_PORT must be between 1024 and 65535.'
[ ! -L "$root" ] || fail "Database directory is a symbolic link: $root"
if [ ! -e "$root" ]; then
  stage=$(mktemp -d "$support/.postgres-18.XXXXXX")
  /usr/bin/openssl rand -hex 32 > "$stage/password"
  printf '%s\n' "$port" > "$stage/port"
  pg initdb -D "$stage/data" -U difqo_admin --encoding=UTF8 --locale=C --auth-local=trust --auth-host=scram-sha-256 >/dev/null
  cat > "$stage/data/pg_hba.conf" <<'HBA'
local all difqo_admin trust
host difqo difqo 127.0.0.1/32 scram-sha-256
HBA
  cat >> "$stage/data/postgresql.conf" <<CONF
listen_addresses = '127.0.0.1'
port = $port
unix_socket_directories = ''
password_encryption = 'scram-sha-256'
CONF
  mv "$stage" "$root"
  stage=
fi

for file in password port data/PG_VERSION; do
  [ -f "$root/$file" ] && [ ! -L "$root/$file" ] || fail "Existing directory is missing its managed $file file."
done
[ ! -L "$root/data" ] && [ "$(cat "$root/data/PG_VERSION")" = 18 ] || fail 'Expected a managed PostgreSQL 18 data directory.'
# The credential and data must remain accessible only to the current user.
for directory in "$root" "$root/data"; do
  [ "$(stat -f '%u:%Lp' "$directory")" = "$(id -u):700" ] || fail "Expected current-user ownership and mode 0700: $directory"
done
[ "$(stat -f '%u:%Lp' "$root/password")" = "$(id -u):600" ] || fail 'Expected a private PostgreSQL password file.'
password=$(cat "$root/password")
[[ "$password" =~ ^[0-9a-f]{64}$ ]] || fail 'Managed PostgreSQL password file is invalid.'
saved_port=$(cat "$root/port")
valid_port "$saved_port" || fail 'Managed PostgreSQL port file is invalid.'
if [ -n "${DIFQO_POSTGRES_PORT:-}" ] && [ "$port" != "$saved_port" ]; then
  fail 'The requested port differs from the existing cluster port.'
fi
port=$saved_port

if pg pg_ctl -D "$root/data" status >/dev/null; then
  fail 'Stop the Difqo-managed PostgreSQL cluster before running provisioning.'
fi
socket=$(mktemp -d /tmp/difqo-pg.XXXXXX)
started=true
pg pg_ctl -D "$root/data" -l "$root/postgres.log" -o "-c unix_socket_directories=$socket" -w -t 30 start >/dev/null ||
  fail "Could not start the private cluster. Check $root/postgres.log and confirm port $port is available."

# The administrator is available only over the private provisioning socket.
# Send the generated password over stdin instead of putting it on the command line.
{
  printf "SELECT format('CREATE ROLE difqo LOGIN PASSWORD %%L', '%s') WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'difqo')\n\\\\gexec\n" "$password"
  printf '%s\n' "SELECT 'CREATE DATABASE difqo OWNER difqo' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'difqo')" '\gexec'
} | pg psql -Xq -v ON_ERROR_STOP=1 -h "$socket" -p "$port" -U difqo_admin -d postgres >/dev/null

DIFQO_DATABASE_URL="postgres://difqo:$password@127.0.0.1:$port/difqo?sslmode=disable" "$difqo" setup-database
printf 'Provisioned PostgreSQL 18 at %s (loopback port %s).\n' "$root" "$port"
