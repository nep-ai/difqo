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
  -f migrations/0001_tasks.sql -f migrations/0002_attempt_leases.sql
go build -o bin/difqo ./cmd/difqo
./bin/difqo server
```

Apply each migration once, in order. To upgrade a database that already has
0001, stop the server and workers, apply 0002 only, and start the new server.
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

## PostgreSQL smoke test

CI runs the smoke test with a disposable PostgreSQL service. To reproduce it
locally, build the binary as above, set `DIFQO_DATABASE_URL` to a fresh disposable
database, and run:

```sh
psql "$DIFQO_DATABASE_URL" -v ON_ERROR_STOP=1 \
  -f migrations/0001_tasks.sql -f migrations/0002_attempt_leases.sql
python3 scripts/postgres-smoke.py bin/difqo
```

The script starts and restarts the server, runs a worker against a synthetic Git
checkout, and checks concurrent claims, lease renewal, interrupted worker
recovery, retry exhaustion, and result persistence. It uses `psql` to expire
fixture leases without waiting and leaves the test records in the disposable
database.
