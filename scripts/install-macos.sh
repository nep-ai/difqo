#!/usr/bin/env bash
set -euo pipefail
umask 077

fail() {
  printf 'Difqo installer: %s\n' "$*" >&2
  exit 1
}

if [ "$#" -ne 3 ] || [[ ! "$1" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9]+([.-][A-Za-z0-9]+)*)?$ ]]; then
  printf 'Usage: bash scripts/install-macos.sh VERSION ARCHIVE TRUSTED_SHA256SUMS\n' >&2
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
  exit 0
fi

stage=$(mktemp -d "$releases/.difqo-install.XXXXXX")
cp -R "$scratch/payload" "$stage/"
mv "$stage/payload" "$destination"
printf 'Installed Difqo %s at %s\n' "$version" "$destination"
