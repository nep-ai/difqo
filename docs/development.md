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
psql "$DIFQO_DATABASE_URL" -v ON_ERROR_STOP=1 \
  -f migrations/0001_tasks.sql -f migrations/0002_attempt_leases.sql \
  -f migrations/0003_execution_evidence.sql -f migrations/0004_patch_review.sql
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

Use a local Docker engine and a trusted synthetic Git checkout with a commit.
Choose a check command that runs using tools available in the image. The
operator supplies this command independently of the task goal.

```sh
docker build -t difqo-codex:0.171.0 containers/codex
auth_home="$HOME/.local/share/difqo/codex-home"
artifact_root="$HOME/.local/share/difqo/artifacts"
mkdir -p "$auth_home"
chmod 700 "$auth_home"
docker run --rm -it --user "$(id -u):$(id -g)" \
  --env HOME=/tmp --env CODEX_HOME=/codex-home \
  --mount "type=bind,src=$auth_home,dst=/codex-home" \
  difqo-codex:0.171.0 codex login -c 'cli_auth_credentials_store="file"' --device-auth
```

Use a dedicated Codex home with its own ChatGPT sign-in. Configure the Docker
engine to share the artifact root and that home with its Linux runtime when
needed. The worker starts the App Server with a workspace-only permissions
profile that denies tool access to the sign-in directory. The runtime must
support the Linux sandbox used by the pinned Codex release.
[Codex permissions](https://learn.chatgpt.com/docs/permissions).

With the server running, submit a bounded goal and start one worker:

```sh
curl -fsS http://127.0.0.1:8765/tasks \
  -H 'Content-Type: application/json' \
  -d '{"goal":"Replace the contents of greeting.txt with the line after"}'
./bin/difqo worker -repo /path/to/disposable-checkout \
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

## PostgreSQL smoke test

CI runs the smoke test with a disposable PostgreSQL service. To reproduce it
locally, build the binary as above, set `DIFQO_DATABASE_URL` to a fresh disposable
database, and run:

```sh
psql "$DIFQO_DATABASE_URL" -v ON_ERROR_STOP=1 \
  -f migrations/0001_tasks.sql -f migrations/0002_attempt_leases.sql \
  -f migrations/0003_execution_evidence.sql -f migrations/0004_patch_review.sql
python3 scripts/postgres-smoke.py bin/difqo
```

The script starts and restarts the server, runs a worker against a synthetic Git
checkout, and checks concurrent claims, lease renewal, interrupted worker
recovery, retry exhaustion, and result persistence. It uses `psql` to expire
fixture leases without waiting and leaves the test records in the disposable
database.

To include the Docker checks, build the Codex image above and the synthetic App
Server image below, then set `DIFQO_SMOKE_CODEX=1` when running the smoke script
against a fresh migrated database. These checks use the real App Server to
verify missing sign-in and a synthetic JSON-RPC turn to verify decision
binding, patch capture, reruns, stale evidence, and persisted approval.

```sh
docker build -t difqo-codex-smoke -f scripts/fixtures/Dockerfile.codex-smoke scripts/fixtures
DIFQO_SMOKE_CODEX=1 python3 scripts/postgres-smoke.py bin/difqo
```

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
host. The server needs access to the same local Docker engine and image ID
used by the worker. Keep the shallow base and patch in the artifact directory
for reruns. A changed patch file is treated as unavailable. Submit a new goal
to review a different patch or use a different saved decision. Evidence
captured before migration 0004 is still viewable from **View run evidence**.
