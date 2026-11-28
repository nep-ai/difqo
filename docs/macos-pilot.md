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
export its connection URL before the first run:

```sh
export DIFQO_DATABASE_URL='postgres://USER@HOST:5432/difqo'
/bin/bash ./start-pilot.sh
```

Subsequent invocations reuse the saved database. Use the same artifact when
testing a rerun.

## Verify the first patch

On a clean Apple silicon Mac with Difqo installed, start Docker Desktop.
Open **Connections** in Chief of Staff and choose **Sign in with ChatGPT**.
Follow the OpenAI sign-in link and enter the one-time code shown in Difqo.
Wait for the worker to report ready, then load and queue the sample goal.
Choose **Run sample goal** on its queued card. The worker creates a disposable
Git repository, claims the selected goal, and checks that `greeting.txt`
contains `after`. In **Review patch**, inspect the diff, select **Rerun check**,
and approve the patch once it passes.

### Connect the staging integrations

- In **Connections → Slack**, enter the Slack pilot enrollment code and
  authorize your Slack identity. Install the app or request workspace approval,
  check authorization, and confirm the workspace and operator to link the DM.
  Send a coding goal as plain text in the linked pilot DM and verify it appears in Chief of Staff.
- In **Connections → GitHub**, open the official app, install it on the
  approved test repository, and enter the GitHub pilot connection code.
  Post a test comment beginning with `@difqo` and choose **Sync GitHub mentions**.
  A second sync should import zero tasks. This pilot grant is read-only.

For a manual run with a saved decision or a supported alternate runtime, use
the steps below.

The installer reaches the UI independently of container tools. For a Codex
goal, start Docker Desktop, make Git available, and use a dedicated Codex
sign-in. Use a disposable repository and material that is safe to send to
the model.

The LaunchAgent must be able to find the same Docker engine and image for
**Rerun check**. Use Docker's default configuration under your home directory.
If you use a named context, select it with `docker context use NAME` before
building the image. Shell-only `DOCKER_CONTEXT`, `DOCKER_HOST`, and
`DOCKER_CONFIG` overrides do not reach the LaunchAgent. Build, sign in, and
run the worker against the same engine and context.

Docker's stock policy can block the nested namespaces required by the Codex
sandbox. The worker checks sandbox readiness before claiming the goal. If
preflight fails, see the [runtime and sandbox setup guide](https://github.com/nep-ai/difqo/blob/main/docs/development.md#runtime-selection-and-linux-sandbox-setup)
for an administrator-approved policy scoped to the Codex container. If you
use a trusted wrapper for that policy, it must forward to the same engine
that the LaunchAgent can discover through Docker. The queued goal will still
be there. If you intentionally switch to supported Podman, repeat the image
build and device sign-in on that engine using the linked guide. The
LaunchAgent must be able to discover that Podman connection by default.
In the worker command, set `runtime=podman` if you switched to Podman.

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
runtime=docker  # Set to podman if you built the image and signed in there.
"$release/bin/difqo" worker -repo "$fixture" -container-runtime "$runtime" \
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
log out and log back in. To leave the pilot inactive afterward, open Terminal
in the extracted artifact folder again and run:

```sh
root="$HOME/Library/Application Support/Difqo"
release="$root/releases/difqo-$(cat VERSION)-darwin-arm64"
"$release/bin/service-macos" pause
```

Pause preserves the database and settings.
The `REVISION` file identifies the CI build. Capture the version, build
revision, whether the services recovered, and any failure before sharing
results. Logs are under `$root/service`. Use synthetic or redacted evidence.
