#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 2 ]; then
  printf 'Usage: bash scripts/install-macos-smoke.sh VERSION BUNDLE_DIR\n' >&2
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

# Linux CI simulates platform detection. macOS hosts exercise their native tools.
real_uname=$(command -v uname)
mkdir "$scratch/platform"
cat > "$scratch/platform/uname" <<'PLATFORM'
#!/bin/sh
case "$1" in
  -s) printf 'Darwin\n' ;;
  -m) printf '%s\n' "${DIFQO_TEST_ARCH:-arm64}" ;;
  *) exit 1 ;;
esac
PLATFORM
chmod +x "$scratch/platform/uname"
platform_path=$PATH
if [ "$("$real_uname" -s)" != Darwin ] || [ "$("$real_uname" -m)" != arm64 ]; then
  platform_path="$scratch/platform:$PATH"
fi

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
expect_failure 'requires macOS on Apple silicon' env PATH="$scratch/platform:$PATH" DIFQO_TEST_ARCH=x86_64 \
  bash "$installer" "$version" "$archive" "$manifest"

# Make tar fail loudly if a rejected input reaches extraction.
printf '#!/bin/sh\ntouch "$TMPDIR/tar-ran"\nexit 99\n' > "$scratch/no-extraction/tar"
chmod +x "$scratch/no-extraction/tar"
cp "$archive" "$scratch/corrupt.tar.gz"
printf 'corrupted\n' >> "$scratch/corrupt.tar.gz"
expect_failure 'Checksum verification failed' env PATH="$scratch/no-extraction:$platform_path" \
  bash "$installer" "$version" "$scratch/corrupt.tar.gz" "$manifest"
sed 's/darwin-arm64/darwin-amd64/' "$manifest" > "$scratch/wrong-manifest"
expect_failure 'Expected one SHA-256 entry' env PATH="$scratch/no-extraction:$platform_path" \
  bash "$installer" "$version" "$archive" "$scratch/wrong-manifest"
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
expect_failure 'does not contain the expected release payload' env PATH="$platform_path" \
  bash "$installer" "$version" "$scratch/verified/$bundle.tar.gz" "$scratch/verified/SHA256SUMS"
test ! -e "$HOME/Library"

root="$HOME/Library/Application Support/Difqo"
destination="$root/releases/$bundle"
env PATH="$platform_path" bash "$installer" "$version" "$archive" "$manifest"
test -x "$destination/bin/difqo"
test -f "$destination/web/index.html"
test -f "$destination/migrations/0001_tasks.sql"
test "$(cat "$destination/VERSION")" = "$version"
mkdir -p "$root/releases/older-version"
printf 'keep settings\n' > "$root/settings.env"
printf 'keep older release\n' > "$root/releases/older-version/sentinel"
expect_preserved() {
  test "$(cat "$root/settings.env")" = 'keep settings'
  test "$(cat "$root/releases/older-version/sentinel")" = 'keep older release'
}

env PATH="$platform_path" bash "$installer" "$version" "$archive" "$manifest" > "$scratch/rerun"
grep -F 'is already installed' "$scratch/rerun"
expect_preserved

printf 'local changes\n' >> "$destination/web/index.html"
expect_failure 'Install path already exists with different contents' env PATH="$platform_path" \
  bash "$installer" "$version" "$archive" "$manifest"
grep -F 'local changes' "$destination/web/index.html"
expect_preserved
test -z "$(find "$root/releases" -maxdepth 1 -name '.difqo-*' -print)"
printf 'Installer smoke passed.\n'
