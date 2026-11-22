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
psql "$DIFQO_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/0001_tasks.sql
go build -o bin/difqo ./cmd/difqo
./bin/difqo server
```

Apply the migration once to a fresh database. With the server running, use a
second terminal at the repository root. Point the worker at a disposable Git
checkout with a commit.

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

Run `go test ./...` and `go vet ./...` before review. The
[architecture document](architecture.md) describes the current limitations.
