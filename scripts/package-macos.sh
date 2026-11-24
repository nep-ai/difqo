#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -lt 2 ] || [ "$#" -gt 3 ] || [[ ! "$1" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9]+([.-][A-Za-z0-9]+)*)?$ ]]; then
  printf 'Usage: bash scripts/package-macos.sh vMAJOR.MINOR.PATCH[-prerelease] OUTPUT_DIR [POSTGRES_RUNTIME]\n' >&2
  exit 2
fi

version=$1
bundle="difqo-${version}-darwin-arm64"
root=$(cd "$(dirname "$0")/.." && pwd)
runtime=
if [ "$#" -eq 3 ]; then
  runtime=$(cd "$3" && pwd)
  for tool in postgres initdb pg_ctl psql; do
    [ -x "$runtime/bin/$tool" ] || { printf 'Missing runtime executable: %s\n' "$tool" >&2; exit 1; }
  done
  for file in VERSION COPYRIGHT SOURCE.sha256; do
    [ -s "$runtime/$file" ] || { printf 'Missing runtime metadata: %s\n' "$file" >&2; exit 1; }
  done
  [ -d "$runtime/lib" ] && [ -d "$runtime/share" ] || exit 1
  special=$(find "$runtime" ! -type d ! -type f -print)
  [ -z "$special" ] || { printf 'Runtime must contain only directories and regular files.\n' >&2; exit 1; }
fi

if [ ! -f "$root/web/dist/index.html" ]; then
  printf 'Build the web UI before packaging: corepack pnpm@10.34.9 --filter difqo-local-ui build\n' >&2
  exit 1
fi

mkdir -p "$2"
output=$(cd "$2" && pwd)
cd "$root"
lock="$output/.$bundle.lock"
# Let Bash reserve the lock file with an exclusive create.
if ! (set -o noclobber; : > "$lock"); then
  printf 'Cannot reserve output for %s\n' "$bundle" >&2
  exit 1
fi
stage=
cleanup() {
  if [ -n "$stage" ]; then
    rm -rf "$stage"
  fi
  rm -f "$lock"
}
trap cleanup EXIT

if [ -e "$output/$bundle" ] || [ -L "$output/$bundle" ]; then
  printf 'Output already exists: %s\n' "$output/$bundle" >&2
  exit 1
fi

stage=$(mktemp -d "$output/.difqo-package.XXXXXX")
mkdir -p "$stage/payload/bin" "$stage/payload/web" "$stage/payload/migrations" "$stage/release"

CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -o "$stage/payload/bin/difqo" ./cmd/difqo
cp scripts/provision-postgres-macos.sh "$stage/payload/bin/provision-postgres-macos"
chmod 755 "$stage/payload/bin/provision-postgres-macos"
cp -R web/dist/. "$stage/payload/web/"
cp migrations/*.sql "$stage/payload/migrations/"
cp LICENSE "$stage/payload/"
printf '%s\n' "$version" > "$stage/payload/VERSION"

(
  cd "$stage/release"
  entries=(bin web migrations LICENSE VERSION)
  if [ -n "$runtime" ]; then
    cp -R "$runtime" "$stage/payload/postgresql"
    entries+=(postgresql)
  fi
  COPYFILE_DISABLE=1 tar -czf "$bundle.tar.gz" -C "$stage/payload" "${entries[@]}"
  shasum -a 256 "$bundle.tar.gz" > SHA256SUMS
)
mv "$stage/release" "$output/$bundle"
printf '%s\n' "$output/$bundle"
