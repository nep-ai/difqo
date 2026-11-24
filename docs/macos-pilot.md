# Difqo Apple silicon pilot

Download the `difqo-macos-arm64-pilot` artifact from the reviewed GitHub
Actions run. The artifact name ends in its run attempt number. Unzip it and
open Terminal in the extracted folder. On a logged-in Apple silicon Mac run:

```sh
/bin/bash ./start-pilot.sh
```

The command verifies the package using the manifest from the same trusted
Actions run, installs a versioned release for the current user, prepares
PostgreSQL, starts the per-user services, and opens
`http://127.0.0.1:8765` after it is ready. Use `DIFQO_PILOT_NO_OPEN=1` to keep
the browser closed. To use an existing dedicated PostgreSQL database,
set `DIFQO_DATABASE_URL` before the first run. Subsequent invocations reuse
the saved database. Use the same artifact when testing a rerun.

## Verify the first patch

The installer reaches the UI independently of container tools. For a Codex
goal, start a supported Docker or Podman engine, make Git available, and use
a dedicated Codex sign-in. The steps below show Docker. Use a disposable
repository and material that is safe to send to the model.

Run these commands from the same artifact directory:

```sh
root="$HOME/Library/Application Support/Difqo"
release="$root/releases/difqo-$(cat VERSION)-darwin-arm64"
auth_home="$root/codex-home"
mkdir -m 700 -p "$auth_home"
docker build -t difqo-codex:0.171.0 "$release/containers/codex"
docker run --rm -it --pull=never --user "$(id -u):$(id -g)" \
  --env HOME=/tmp --env CODEX_HOME=/codex-home \
  --mount "type=bind,src=$auth_home,dst=/codex-home" \
  difqo-codex:0.171.0 codex login -c 'cli_auth_credentials_store="file"' --device-auth

fixture=$(mktemp -d "$HOME/difqo-pilot-repo.XXXXXX")
git -C "$fixture" init -q
printf 'before\n' > "$fixture/greeting.txt"
git -C "$fixture" add greeting.txt
git -C "$fixture" -c user.name='Difqo Pilot' -c user.email=pilot@example.invalid \
  -c commit.gpgSign=false commit -qm 'Synthetic pilot repository'
```

In Chief of Staff, save the project decision `Use the line after in
greeting.txt.` Submit the goal `Replace the fixture greeting according to the
saved decision.` Then run the worker:

```sh
"$release/bin/difqo" worker -repo "$fixture" -container-runtime docker \
  -codex-image difqo-codex:0.171.0 -codex-home "$auth_home" \
  -artifacts "$root/artifacts" -check 'test "$(cat greeting.txt)" = after'
```

Open **Review patch** in the corresponding update, inspect the decision,
base and diff, then select **Rerun check**. Once it passes, select **Approve
patch**. Reload to confirm approval persists. Verify that the source file
still reads `before`.

## Check the lifecycle

```sh
"$release/bin/service-macos" status
"$release/bin/service-macos" pause
"$release/bin/service-macos" start
"$release/bin/service-macos" restart
```

Verify the approved task survives restart. To test persistence across login,
log out and log back in. To leave the pilot inactive afterward, use
`"$release/bin/service-macos" pause`. Pause preserves the database and settings.
The `REVISION` file identifies the CI build. Capture the version, build
revision, whether the services recovered, and any failure before sharing
results. Logs are under `$root/service`. Use synthetic or redacted evidence.
