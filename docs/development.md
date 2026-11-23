# Local development

Install Go 1.26 or later, Git, and PostgreSQL. Start PostgreSQL on the host and
create a disposable local database. The sample connection assumes your
PostgreSQL server accepts your local user over loopback. Adjust it to match your
setup and keep credentials outside Git.

```sh
createdb difqo
cp .env.example .env
set -a
. ./.env
set +a
for migration in migrations/*.sql; do
  psql "$DIFQO_DATABASE_URL" -v ON_ERROR_STOP=1 -f "$migration" || break
done
go build -o bin/difqo ./cmd/difqo
./bin/difqo server
```

Apply each migration once, in order. To upgrade an existing database, stop the
server and workers, apply the migrations it is missing, and start the new server.
With the server running, use a second terminal at the repository root. Point
the worker at a disposable Git checkout with a commit.

```sh
curl -fsS http://127.0.0.1:8765/tasks \
  -H 'Content-Type: application/json' \
  -d '{"goal":"Record the fixture repository revision"}'
./bin/difqo worker -repo /path/to/disposable-checkout
curl -fsS http://127.0.0.1:8765/tasks/1
```

Use the ID returned by the submit call in place of `1` when needed. The result
should show exit code `0` and the same revision as
`git -C /path/to/disposable-checkout rev-parse --verify HEAD`. The worker handles
one claim and exits. Both commands accept `-port` to select a matching API port.

Each claim starts a 30-second lease. A client can send `POST /attempts/{id}/renew`
with a JSON body of `{}` to extend an active lease. Expired attempts return
HTTP 409 for renewals and result submissions. To recover an interrupted run,
invoke the worker again after the lease expires. After three expired attempts,
the task read returns `"status": "exhausted"`.

Run the same checks as [Go CI](../.github/workflows/go.yml) before review.

```sh
gofmt -l .  # Expect no output.
go vet ./...
go test ./...
go build -o bin/difqo ./cmd/difqo
```

The [architecture document](architecture.md) describes the current limitations.

## Bounded Codex execution

Use a local Docker or Podman engine and a trusted synthetic Git checkout with a commit.
Choose a check command that runs using tools available in the image. The
operator supplies this command independently of the task goal.

Pick the CLI and start its engine first. On macOS, start your Docker VM or use
`podman machine start` for Podman. Use shared paths for both the artifact
directory and the dedicated Codex home so the Linux engine can mount them at
the same paths. Use the same engine and connection to build the image, sign in,
and run the worker.

```sh
runtime=docker # Set to podman to use the native Podman CLI.
"$runtime" build -t difqo-codex:0.171.0 containers/codex
auth_home="$HOME/.local/share/difqo/codex-home"
artifact_root="$HOME/.local/share/difqo/artifacts"
mkdir -p "$auth_home" "$artifact_root"
chmod 700 "$auth_home"
set --
if [ "$runtime" = podman ] && [ "$(podman info --format '{{.Host.Security.Rootless}}')" = true ]; then
  set -- "--userns=keep-id:uid=$(id -u),gid=$(id -g)"
fi
"$runtime" run "$@" --rm -it --pull=never --user "$(id -u):$(id -g)" \
  --env HOME=/tmp --env CODEX_HOME=/codex-home \
  --mount "type=bind,src=$auth_home,dst=/codex-home" \
  difqo-codex:0.171.0 codex login -c 'cli_auth_credentials_store="file"' --device-auth
```

Use a dedicated Codex home with its own ChatGPT sign-in. The worker starts
the App Server with a workspace-only permissions profile that denies tool
access to the sign-in directory. The runtime must support the Linux sandbox
used by the pinned Codex release.
[Codex permissions](https://learn.chatgpt.com/docs/permissions).

With the server running, submit a bounded goal and start one worker:

```sh
curl -fsS http://127.0.0.1:8765/tasks \
  -H 'Content-Type: application/json' \
  -d '{"goal":"Replace the contents of greeting.txt with the line after"}'
./bin/difqo worker -repo /path/to/disposable-checkout \
  -container-runtime "$runtime" \
  -codex-image difqo-codex:0.171.0 -codex-home "$auth_home" \
  -artifacts "$artifact_root" -check 'test "$(cat greeting.txt)" = after'
curl -fsS http://127.0.0.1:8765/tasks/1
```

Use the ID from the submit response. Read `change.diff` and `check.log` under
`$artifact_root/<attempt.result.artifacts>` after the worker exits. The patch
compares the Codex edit with the initial commit and includes new files. The
source repository remains available for review. The check runs in a separate
container without network access or the Codex home mount. A missing or rejected
sign-in produces `"status": "blocked"`. Sign in and submit a new task to retry.
The worker allows up to three and a half minutes for the full host-side
execution. App Server startup and turn failures can include a private
`execution.log` with a fixed diagnostic selected from the error category.

### Runtime selection and Linux sandbox setup

Omit `-container-runtime` or set it to `auto` to try Docker first, then native
Podman. The worker selects the first CLI whose engine is reachable, whose image
is local, and whose containers pass the preflight. The probes check an offline
workspace bind mount and start the real Codex sandbox with a test sign-in
directory. They verify that the sandbox can write to the workspace and cannot
read the test credential file. An explicit CLI selection fails on its own
errors. If validation fails, the worker exits with a diagnostic before claiming
a task. The worker uses the selected executable for the turn, diff, check, and
cleanup.

- **Docker.** The engine's outer seccomp and, where present, AppArmor profiles
  must permit bubblewrap to create its nested user and mount namespaces. The
  stock Docker policies can block that setup. Use an administrator-approved
  policy scoped to the Codex container. A CLI wrapper can apply the profiles to
  `run` invocations that contain `--env=CODEX_HOME=/codex-home` and forward all
  other invocations unchanged. The [Go workflow](../.github/workflows/go.yml)
  and [its wrapper](../scripts/fixtures/docker-codex-smoke) show the setup for
  synthetic fixtures on a disposable runner.
- **Podman.** Rootless execution uses `keep-id` with your host UID and GID.
  Configure subordinate IDs and resource-limit support for local Linux rootless
  engines. On SELinux hosts, give containers access to the dedicated bind
  directories using your host's labeling policy. If SELinux blocks bubblewrap
  from mounting its own `devpts` even with working bind mounts, you can opt in
  to `-container-selinux-label-disable` on a trusted machine. The option disables
  SELinux labeling for the Codex turn and its sandbox probe. The offline diff
  and check containers retain the engine's labeling policy.
- **Other compatible CLIs.** Pass one executable name or path to
  `-container-runtime`. It must implement Docker-style `info`,
  `image inspect --format '{{.Id}}' IMAGE`, `run`, and `rm -f NAME`, including the run flags in
  the [container adapter](../internal/execution/container.go). It must honor the
  requested user, capability drop, read-only root, process and resource limits,
  bind mounts, network isolation, pull policy, and stdin/stdout behavior. Use a
  trusted executable or put any required fixed arguments in a wrapper.

## PostgreSQL smoke test

CI runs the smoke test with a disposable PostgreSQL service. To reproduce it
locally, build the binary as above, set `DIFQO_DATABASE_URL` to a fresh disposable
database, and run:

```sh
for migration in migrations/*.sql; do
  psql "$DIFQO_DATABASE_URL" -v ON_ERROR_STOP=1 -f "$migration" || break
done
python3 scripts/postgres-smoke.py bin/difqo
```

The script starts and restarts the server, runs a worker against a synthetic Git
checkout, and checks concurrent claims, lease renewal, interrupted worker
recovery, retry exhaustion, and result persistence. It uses `psql` to expire
fixture leases without waiting and leaves the test records in the disposable
database.

To include container checks, build the Codex image above and the synthetic App
Server image below with the selected runtime. Use a fresh migrated database for
each run. The smoke test verifies pre-claim rejection, the real App Server's
missing sign-in status, a synthetic authenticated turn, container removal, and
patch and log retrieval over HTTP. It also verifies decision binding,
original-base reruns, stale evidence rejection, and persisted approval.

```sh
"$runtime" build -t difqo-codex-smoke -f scripts/fixtures/Dockerfile.codex-smoke scripts/fixtures
DIFQO_SMOKE_CODEX=1 DIFQO_SMOKE_CONTAINER_RUNTIME="$runtime" \
  python3 scripts/postgres-smoke.py bin/difqo
```

Set `DIFQO_SMOKE_SELINUX_LABEL_DISABLE=1` if using the Podman SELinux option.
For VM-backed engines, point `TMPDIR` at a directory shared with the engine
if the default temporary directory is outside the file shares. To test a
compatible executable, build with its underlying engine and set
`DIFQO_SMOKE_CONTAINER_RUNTIME` to the executable path.
When multiple worktrees share an engine, tag the synthetic image separately
for each worktree and pass its tag as `DIFQO_SMOKE_FIXTURE_IMAGE`.

## Chief of Staff web app

Set up the database and Go binary using the instructions above. Install Node.js
24 and build the frontend from the repository root.

```sh
corepack pnpm@10.34.9 install --frozen-lockfile
corepack pnpm@10.34.9 --filter difqo-local-ui build
./bin/difqo server -web web/dist
```

Open `http://127.0.0.1:8765`, submit a goal, then run the local worker in a
separate terminal. The conversation shows the saved goal, the latest run
status, and the worker's actual output. The default worker reports the
repository revision. To view a Codex patch and check output, pass the same
absolute `-artifacts` directory to the server and worker. Create that directory
before starting the server. Keep the application on loopback.

For frontend development, start the server on the default port, then run
`corepack pnpm@10.34.9 --filter difqo-local-ui dev`. The Vite server proxies API
requests to Difqo. The frontend follows the [Difqo brand guidelines](brand.md)
and serves its fonts locally.

### Review a patch

1. In the Chief of Staff composer, open **Project decision** and save a
   decision. Each new goal captures the latest saved revision.
2. Submit a goal and run the Codex worker with an operator-selected check.
3. Open **Review patch** under the Chief of Staff update. Inspect the decision
   revision, base commit, patch digest, and original check. **View patch**
   includes **Download patch**.
4. Select **Rerun check**, then **Approve patch** after the rerun passes.
   Approval is recorded in PostgreSQL. Apply the downloaded patch manually.

Use a private artifact directory shared by the server and worker on the same
host. Pass the same `-container-runtime` to the server and worker to use a
specific engine for both original checks and reruns. In automatic mode the
server tries Docker, then Podman, for the saved image ID and an offline check
container. Reruns need neither Codex credentials nor the SELinux label
exception for the Codex sandbox. Keep the shallow base and patch in the artifact directory
for reruns. A changed patch file is treated as unavailable. Submit a new goal
to review a different patch or use a different saved decision. Evidence
captured before migration 0004 is still viewable from **View run evidence**.

### Configure specialists for local projects

Register checkouts in a local JSON file and start the server with
`-projects /path/to/projects.json`. Use a stable ID, a display name, an absolute
Git checkout root, and a binding version for each project. Keep this file local
to the operator. For example:

```json
[
  {"id":"reports","name":"Sample Reports","checkout":"/path/to/reports","binding_version":"1"},
  {"id":"billing","name":"Sample Billing","checkout":"/path/to/billing","binding_version":"1"}
]
```

Restart the server after editing the catalog. Advance the binding version when
replacing a checkout in place. Moving a checkout, changing its binding version,
or removing it makes the earlier binding ineligible. Review a new specialist
proposal to approve the new binding.

Open **Specialists** in the Chief of Staff conversation. Describe the role and
expected output, select the projects it may serve, and review the proposal.
The specialist is saved locally when you approve it. Use **Inspect a specialist**
to see its current approved configuration, propose an update, or review a
disable request. A single profile covers snapshot review for all approved
projects.

### Start from a specialist template

In **Specialists**, open **Browse specialist templates** and choose
**Committed change summary** or **Tracked file inventory**. Both use the
same local Git snapshot report and prefill the name, purpose, and expected
output. Edit those fields, select the projects to authorize, and review and
approve the proposal. The resulting specialist uses the usual update and
approval flow. Running it requires a project-bound worker and a committed
checkout.

### Start and inspect a specialist run

In **Project decision**, select the named project to save its decision. Ordinary
goals use their existing decision scope. A specialist run pins the latest
decision for the selected checkout binding when it enters the queue.

In **Specialists**, inspect an enabled specialist and open **Run this
specialist**. Select one approved project, enter the goal, and choose
**Start run**. Invoke a worker for that project's exact checkout and binding
version:

```sh
./bin/difqo worker -project reports -binding-version 1 -repo /path/to/reports
```

The snapshot-review profile reports committed file changes and tracked files,
with the requested goal and saved decision as context. It uses local Git reads.
No provider credentials or container configuration are needed. The worker
claims one run for that binding per invocation. Ordinary workers continue to
claim ordinary goals.

Open **View attempt history** from the goal's project file to inspect results
and earlier expired attempts. An expired attempt can be reclaimed by a matching
worker while the project remains authorized. Removing scope or disabling the
specialist blocks new starts and claims. Active attempts can finish. Manual
retry, pause, cancellation, and other execution profiles are later work.

## Local GitHub comment intake

The one-shot poller accepts a short-lived installation token in
`DIFQO_GITHUB_TOKEN`. Supply a token scoped to the enrolled repository with
read permissions for issues and pull requests. Configure the repository's
numeric ID and the numeric GitHub user ID of the local owner. Only that
account's issue and pull request conversation comments starting with
`@difqo` followed by a goal enter the queue. Use `-handle` for a private app
with a different handle. Set an enrollment start time and reuse it on
subsequent calls. Keep credentials outside Git.

```sh
./bin/difqo github-intake -repository OWNER/REPO -repository-id REPO_ID \
  -owner-id GITHUB_USER_ID -since 2026-11-23T00:00:00Z
```

Use `-github-repository-id REPO_ID` on a Codex worker and point `-repo` at the
enrolled repository's trusted local checkout. Ordinary workers claim local
goals. Review and approval remain in the local app. The poller makes read-only
GitHub requests and reports expired or revoked access as a reconnect error.

Poll frequently enough to stay within one page of comments. If GitHub returns
more than 100 comments for the cursor window, the poll fails and preserves its
cursor. This slice accepts a pre-issued token. Official app installation,
account linking, hosted token issuance, local private-app token minting, and
approved GitHub replies are future work. See the
[GitHub intake ADR](adr/0007-github-intake.md).

Run the synthetic HTTP and PostgreSQL checks without GitHub credentials.
The test role needs permission to create a temporary schema in the test database.

```sh
DIFQO_GITHUB_TEST_DATABASE_URL="$DIFQO_DATABASE_URL" \
  go test -count=1 ./internal/githubintake
```
