#!/usr/bin/env bash
set -euo pipefail
umask 077

fail() {
  printf 'Difqo installer: %s\n' "$*" >&2
  exit 1
}

if [ "$#" -lt 3 ] || [ "$#" -gt 4 ] || { [ "$#" -eq 4 ] && [ "$4" != --start ]; } || [[ ! "$1" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9]+([.-][A-Za-z0-9]+)*)?$ ]]; then
  printf 'Usage: bash scripts/install-macos.sh VERSION ARCHIVE TRUSTED_SHA256SUMS [--start]\n' >&2
  exit 2
fi

if [ "$(uname -s)" != Darwin ] || [ "$(uname -m)" != arm64 ]; then
  fail 'This installer requires macOS on Apple silicon. Run it from a native arm64 shell.'
fi
[[ "${HOME:-}" == /* && -d "$HOME" && "$HOME" != / ]] || fail 'HOME must be an existing per-user directory.'
for tool in shasum tar mktemp diff find; do
  command -v "$tool" >/dev/null || fail "Required command is missing: $tool"
done

version=$1
bundle="difqo-${version}-darwin-arm64"
archive="$bundle.tar.gz"
[ -f "$2" ] && [ -r "$2" ] || fail 'The release archive is missing or unreadable.'
[ -f "$3" ] && [ -r "$3" ] || fail 'The trusted SHA256SUMS manifest is missing or unreadable.'
entry=$(cat < "$3")
digest=${entry:0:64}
if [[ ! "$digest" =~ ^[a-f0-9]{64}$ ]] || [ "$entry" != "$digest  $archive" ]; then
  fail "Expected one SHA-256 entry for $archive in the trusted manifest."
fi

scratch=$(mktemp -d "${TMPDIR:-/tmp}/difqo-install.XXXXXX")
stage=
lock=
cleanup() {
  if [ -n "$stage" ]; then rm -rf "$stage"; fi
  if [ -n "$lock" ]; then rmdir "$lock" || true; fi
  rm -rf "$scratch"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# Verify and extract the same private copy even if the input archive changes.
cp -- "$2" "$scratch/$archive"
if ! (cd "$scratch" && printf '%s\n' "$entry" | shasum -a 256 -c - >/dev/null); then
  fail "Checksum verification failed for $archive."
fi

validate_payload() {
  local payload=$1
  local special
  special=$(find "$payload" ! -type d ! -type f -print) || return 1
  [ -z "$special" ] &&
    [ -f "$payload/VERSION" ] && [ "$(cat "$payload/VERSION")" = "$version" ] &&
    [ -f "$payload/bin/difqo" ] && [ -x "$payload/bin/difqo" ] &&
    [ -f "$payload/web/index.html" ] && [ -f "$payload/LICENSE" ] &&
    [ -f "$payload/migrations/0001_tasks.sql" ]
}

unset TAR_OPTIONS
mkdir "$scratch/payload"
if ! COPYFILE_DISABLE=1 tar -xzf "$scratch/$archive" --no-same-owner --no-same-permissions -C "$scratch/payload"; then
  fail 'Could not extract the verified archive.'
fi
validate_payload "$scratch/payload" || fail 'The verified archive does not contain the expected release payload.'
if [ "${4:-}" = --start ]; then
  [ -x "$scratch/payload/bin/service-macos" ] && [ -x "$scratch/payload/bin/provision-postgres-macos" ] ||
    fail 'This archive does not contain the macOS pilot adapters.'
  launchctl print "gui/$(id -u)" >/dev/null 2>&1 ||
    fail 'Run the pilot from a logged-in macOS graphical session.'
fi

library="$HOME/Library"
support="$library/Application Support"
root="$support/Difqo"
releases="$root/releases"
for directory in "$library" "$support" "$root" "$releases"; do
  if [ -L "$directory" ]; then
    fail "Install directory is a symbolic link: $directory"
  elif [ ! -d "$directory" ]; then
    if ! mkdir -p "$directory" || [ -L "$directory" ]; then
      fail "Could not create install directory: $directory"
    fi
  fi
done

start_pilot() {
  if [ "${4:-}" != --start ]; then return; fi
  # Service settings can exist after an interrupted start. Let the service
  # adapter reconcile them instead of provisioning a different database.
  local service="$root/service"
  if [ -z "${DIFQO_DATABASE_URL:-}" ] && [ ! -e "$service/mode" ] && [ ! -L "$service/mode" ] &&
     [ ! -e "$service/database-url" ] && [ ! -L "$service/database-url" ]; then
    /bin/bash "$destination/bin/provision-postgres-macos"
  fi
  /bin/bash "$destination/bin/service-macos" start
  if [ "${DIFQO_PILOT_NO_OPEN:-}" != 1 ]; then
    open 'http://127.0.0.1:8765' || printf 'Open http://127.0.0.1:8765 in your browser.\n' >&2
  fi
}

reservation="$releases/.$bundle.lock"
if ! mkdir "$reservation"; then
  fail "Could not reserve $bundle. If no installer is running, check $reservation and remove the empty lock."
fi
lock=$reservation
destination="$releases/$bundle"
if [ -e "$destination" ] || [ -L "$destination" ]; then
  if [ ! -d "$destination" ] || [ -L "$destination" ] ||
     ! validate_payload "$destination" || ! diff -qr "$scratch/payload" "$destination" >/dev/null; then
    fail "Install path already exists with different contents: $destination"
  fi
  printf 'Difqo %s is already installed at %s\n' "$version" "$destination"
  start_pilot "$@"
  exit 0
fi

stage=$(mktemp -d "$releases/.difqo-install.XXXXXX")
cp -R "$scratch/payload" "$stage/"
mv "$stage/payload" "$destination"
printf 'Installed Difqo %s at %s\n' "$version" "$destination"
start_pilot "$@"
