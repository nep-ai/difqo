#!/usr/bin/env bash
set -euo pipefail

installer="$(cd "$(dirname "$0")" && pwd)/install-macos.sh"
scratch=$(mktemp -d "${TMPDIR:-/tmp}/difqo-pilot-smoke.XXXXXX")
trap 'rm -rf "$scratch"' EXIT
export HOME="$scratch/private-home" PILOT_LOG="$scratch/calls"
mkdir -p "$HOME" "$scratch/tools" "$scratch/payload/"{bin,web,migrations}
export PATH="$scratch/tools:$PATH"
unset DIFQO_DATABASE_URL DIFQO_PILOT_NO_OPEN

cat > "$scratch/tools/launchctl" <<'SCRIPT'
#!/bin/bash
[ "$1" = print ] && [[ "$2" = gui/* ]]
SCRIPT
cat > "$scratch/tools/open" <<'SCRIPT'
#!/bin/bash
printf 'open %s\n' "$1" >> "$PILOT_LOG"
SCRIPT
cat > "$scratch/payload/bin/provision-postgres-macos" <<'SCRIPT'
#!/bin/bash
printf 'provision\n' >> "$PILOT_LOG"
SCRIPT
cat > "$scratch/payload/bin/service-macos" <<'SCRIPT'
#!/bin/bash
set -eu
[ "$1" = start ]
state="$HOME/Library/Application Support/Difqo/service"
mkdir -p "$state"
if [ -n "${DIFQO_DATABASE_URL:-}" ]; then mode=external; else mode=managed; fi
printf '%s\n' "$mode" > "$state/mode"
printf 'start %s\n' "$mode" >> "$PILOT_LOG"
SCRIPT
touch "$scratch/payload/bin/difqo" "$scratch/payload/LICENSE" \
  "$scratch/payload/web/index.html" "$scratch/payload/migrations/0001_tasks.sql"
chmod +x "$scratch/tools/"* "$scratch/payload/bin/"*
version=v0.0.0-pilot
printf '%s\n' "$version" > "$scratch/payload/VERSION"
archive="difqo-$version-darwin-arm64.tar.gz"
(
  cd "$scratch"
  COPYFILE_DISABLE=1 tar -czf "$archive" -C payload bin web migrations LICENSE VERSION
  shasum -a 256 "$archive" > SHA256SUMS
)

cp "$scratch/$archive" "$scratch/corrupt"
printf 'corrupt\n' >> "$scratch/corrupt"
if /bin/bash "$installer" "$version" "$scratch/corrupt" "$scratch/SHA256SUMS" --start; then exit 1; fi
test ! -e "$PILOT_LOG"
test ! -e "$HOME/Library"
/bin/bash "$installer" "$version" "$scratch/$archive" "$scratch/SHA256SUMS" --start
DIFQO_PILOT_NO_OPEN=1 /bin/bash "$installer" "$version" "$scratch/$archive" "$scratch/SHA256SUMS" --start
printf 'provision\nstart managed\nopen http://127.0.0.1:8765\nstart managed\n' > "$scratch/expected"
cmp "$scratch/expected" "$PILOT_LOG"

export HOME="$scratch/external-home"
mkdir "$HOME"
: > "$PILOT_LOG"
DIFQO_DATABASE_URL='postgres://synthetic.invalid/difqo' DIFQO_PILOT_NO_OPEN=1 \
  /bin/bash "$installer" "$version" "$scratch/$archive" "$scratch/SHA256SUMS" --start
printf 'start external\n' > "$scratch/expected"
cmp "$scratch/expected" "$PILOT_LOG"
printf 'One-command installer smoke passed.\n'
