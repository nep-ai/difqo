#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -lt 2 ] || [ "$#" -gt 3 ] || { [ "$#" -eq 3 ] && [ "$3" != --provision ]; }; then
  printf 'Usage: bash scripts/install-macos-smoke.sh VERSION BUNDLE_DIR [--provision]\n' >&2
  exit 2
fi

version=$1
bundle="difqo-${version}-darwin-arm64"
bundle_dir=$(cd "$2" && pwd)
archive="$bundle_dir/$bundle.tar.gz"
manifest="$bundle_dir/SHA256SUMS"
installer="$(cd "$(dirname "$0")" && pwd)/install-macos.sh"
scratch=$(mktemp -d "${TMPDIR:-/tmp}/difqo-install-smoke.XXXXXX")
trap 'rm -rf "$scratch"' EXIT

if [ "$(uname -s)" != Darwin ] || [ "$(uname -m)" != arm64 ]; then
  printf 'Installer smoke requires native macOS on Apple silicon.\n' >&2
  exit 1
fi

# Override detection only for the unsupported-platform assertion.
mkdir "$scratch/platform"
cat > "$scratch/platform/uname" <<'PLATFORM'
#!/bin/sh
case "$1" in
  -s) printf 'Darwin\n' ;;
  -m) printf '%s\n' x86_64 ;;
  *) exit 1 ;;
esac
PLATFORM
chmod +x "$scratch/platform/uname"
expect_failure() {
  local message=$1
  shift
  if "$@" > "$scratch/output" 2>&1; then
    printf 'Expected failure: %s\n' "$message" >&2
    cat "$scratch/output" >&2
    exit 1
  fi
  grep -F "$message" "$scratch/output"
}

export HOME="$scratch/Synthetic Home"
mkdir "$HOME" "$scratch/no-extraction"
export TMPDIR="$scratch"
expect_failure 'requires macOS on Apple silicon' env PATH="$scratch/platform:$PATH" \
  /bin/bash "$installer" "$version" "$archive" "$manifest"

# Make tar fail loudly if a rejected input reaches extraction.
printf '#!/bin/sh\ntouch "$TMPDIR/tar-ran"\nexit 99\n' > "$scratch/no-extraction/tar"
chmod +x "$scratch/no-extraction/tar"
cp "$archive" "$scratch/corrupt.tar.gz"
printf 'corrupted\n' >> "$scratch/corrupt.tar.gz"
expect_failure 'Checksum verification failed' env PATH="$scratch/no-extraction:$PATH" \
  /bin/bash "$installer" "$version" "$scratch/corrupt.tar.gz" "$manifest"
sed 's/darwin-arm64/darwin-amd64/' "$manifest" > "$scratch/wrong-manifest"
expect_failure 'Expected one SHA-256 entry' env PATH="$scratch/no-extraction:$PATH" \
  /bin/bash "$installer" "$version" "$archive" "$scratch/wrong-manifest"
test ! -e "$scratch/tar-ran"
test ! -e "$HOME/Library"

# A verified archive still has to match the bundle layout and pinned version.
mkdir "$scratch/incomplete" "$scratch/verified"
printf '%s\n' "$version" > "$scratch/incomplete/VERSION"
(
  cd "$scratch/verified"
  tar -czf "$bundle.tar.gz" -C "$scratch/incomplete" VERSION
  shasum -a 256 "$bundle.tar.gz" > SHA256SUMS
)
expect_failure 'does not contain the expected release payload' \
  /bin/bash "$installer" "$version" "$scratch/verified/$bundle.tar.gz" "$scratch/verified/SHA256SUMS"
test ! -e "$HOME/Library"

root="$HOME/Library/Application Support/Difqo"
destination="$root/releases/$bundle"
/bin/bash "$installer" "$version" "$archive" "$manifest"
test -x "$destination/bin/difqo"
test -f "$destination/web/index.html"
test -f "$destination/migrations/0001_tasks.sql"
test "$(cat "$destination/VERSION")" = "$version"
if [ "${3:-}" = --provision ]; then
  python3 "$(dirname "$installer")/provision-postgres-smoke.py" "$destination" "$destination/postgresql/bin"
fi
mkdir -p "$root/releases/older-version"
printf 'keep settings\n' > "$root/settings.env"
printf 'keep older release\n' > "$root/releases/older-version/sentinel"
expect_preserved() {
  test "$(cat "$root/settings.env")" = 'keep settings'
  test "$(cat "$root/releases/older-version/sentinel")" = 'keep older release'
}

/bin/bash "$installer" "$version" "$archive" "$manifest" > "$scratch/rerun"
grep -F 'is already installed' "$scratch/rerun"
expect_preserved

printf 'local changes\n' >> "$destination/web/index.html"
expect_failure 'Install path already exists with different contents' \
  /bin/bash "$installer" "$version" "$archive" "$manifest"
grep -F 'local changes' "$destination/web/index.html"
expect_preserved
test -z "$(find "$root/releases" -maxdepth 1 -name '.difqo-*' -print)"
printf 'Installer smoke passed.\n'
