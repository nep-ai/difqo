#!/usr/bin/env bash
set -euo pipefail

# The hash is reviewed against the upstream release's SHA-256 manifest.
version=18.7
digest=441dfae4280cae3e0425e5f876f80559fb0731c140f1aae22b3e5a54d0411bfd
archive="postgresql-$version.tar.bz2"
prefix=/difqo-postgresql

if [ "$#" -ne 2 ]; then
  printf 'Usage: bash scripts/build-postgres-macos.sh SOURCE_ARCHIVE OUTPUT_DIR\n' >&2
  exit 2
fi
if [ "$(uname -s)" != Darwin ] || [ "$(uname -m)" != arm64 ]; then
  printf 'Build the PostgreSQL runtime on native Apple silicon.\n' >&2
  exit 1
fi
if [ -e "$2" ] || [ -L "$2" ]; then
  printf 'Runtime output already exists.\n' >&2
  exit 1
fi

script_dir=$(cd "$(dirname "$0")" && pwd)
stage=$(mktemp -d "${TMPDIR:-/tmp}/difqo-postgres-build.XXXXXX")
trap 'rm -rf "$stage"' EXIT
cp -- "$1" "$stage/$archive"
(
  cd "$stage"
  printf '%s  %s\n' "$digest" "$archive" | shasum -a 256 -c -
  unset TAR_OPTIONS
  tar -xjf "$archive"
  cd "postgresql-$version"
  # Keep the default relative layout. Avoid dynamic dependencies on the build host.
  LDFLAGS=-Wl,-headerpad_max_install_names ./configure --prefix="$prefix" \
    --disable-rpath --without-icu --without-readline --without-zlib
  make -j "$(sysctl -n hw.ncpu)"
  make DESTDIR="$stage/install" install
  cp COPYRIGHT "$stage/install$prefix/"
)
runtime="$stage/runtime"
mkdir "$runtime"
for component in bin lib share COPYRIGHT; do
  cp -RL "$stage/install$prefix/$component" "$runtime/"
done
python3 "$script_dir/relocate-postgres-macos.py" "$runtime" "$prefix"
printf '%s\n' "$version" > "$runtime/VERSION"
printf '%s  %s\n' "$digest" "$archive" > "$runtime/SOURCE.sha256"
mv "$runtime" "$2"
