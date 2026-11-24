# Official Slack pilot checklist

This checklist covers the first official Slack pilot in the `nepai` workspace.
The local client supports a configured HTTPS relay. Build the Connections
process with `go build ./cmd/connections`. It serves the Slack provider over
a numeric loopback listener behind an HTTPS ingress. The local API provides the OAuth URL handoff for the pilot operator.

## Before deployment

- Designate the official Slack app and an admin who can approve its install
  in `nepai`. Record the workspace's Slack team ID and the pilot operator's
  Slack user ID. The workspace display name alone is insufficient.
- Choose the stable HTTPS origin and identify its deployment owner. Reserve
  it for Connections so an existing pairing can recover at the same origin.
- Keep the Connections listener on loopback and route the configured HTTPS
  origin to it. Keep the Slack callback and signed event routes reachable.
  The process uses only Slack's official origin for provider requests.
- Set and verify a retention limit for undelivered goal bodies. Ensure
  access logs omit request bodies, OAuth callback query strings, and
  authorization headers. Configure bounded request timeouts at ingress.
  Before live data is accepted, test that any rollback retires active
  claims and scrubs their queued goal bodies.

## Hosted configuration

Pass these settings to the Connections process. Its default listener is
`127.0.0.1:8766`. Use `-listen` to choose another numeric loopback address.

| Setting | Value |
| --- | --- |
| `DIFQO_CONNECTIONS_ORIGIN` | Stable HTTPS origin used in callback URLs and by the local client |
| `DIFQO_RELAY_DATABASE_URL` | Secret DSN for the dedicated relay database |
| `DIFQO_SLACK_APP_ID` | Official Slack app ID |
| `DIFQO_SLACK_WORKSPACE_ID` | Allowed `nepai` workspace ID |
| `DIFQO_SLACK_OPERATOR_ID` | Approved Slack operator user ID |
| `DIFQO_SLACK_ENROLLMENT_TOKEN` | Secret 32-byte random token encoded as 64 lowercase hex characters |
| `DIFQO_SLACK_CLIENT_ID` | OAuth Client ID |
| `DIFQO_SLACK_CLIENT_SECRET` | OAuth Client Secret |
| `DIFQO_SLACK_SIGNING_SECRET` | Slack request signing secret |
| `DIFQO_SLACK_TOKEN_KEY` | Secret base64-encoded 32-byte encryption key, stable across restarts |

Inject hosted settings through the deployment's secret store. Provide the
enrollment token only to the authorized pilot enrollment client. That client
posts the locally generated `pairing_id` and `proof` to `POST /slack/enroll`
with the enrollment token as its bearer credential. Subsequent pairing
routes use the pairing proof. Enrollment is retryable with the same ID and
proof and cannot replace a retired pairing. Keep the signing and OAuth
secrets separate. Keep token rotation off for the pilot until refresh-token
handling is implemented.

## Slack app configuration

1. Add both redirect URLs under **OAuth & Permissions** for the same app.
   Register `https://<relay-host>/oauth/identity` for Sign in with Slack and
   `https://<relay-host>/oauth/install` for workspace installation. The relay
   handles the identity callback as a form POST and the install callback
   as a GET. Start identity with `openid profile` and request bot scopes on
   the separate installation flow.
   [Sign in with Slack](https://docs.slack.dev/authentication/sign-in-with-slack/) ·
   [Installing via OAuth](https://docs.slack.dev/authentication/installing-with-oauth/)
2. Set bot scopes to `im:history`, `im:write`, and `chat:write`. Enable the
   App Home Messages tab and allow users to send messages there. Enable
   Event Subscriptions in HTTP mode and set the Request URL to
   `https://<relay-host>/slack/events`. Verify the signed URL challenge
   and subscribe to the bot event `message.im`.
   [Events API](https://docs.slack.dev/apis/events-api/) ·
   [Direct message event](https://docs.slack.dev/reference/events/message.im/) ·
   [App Home settings](https://docs.slack.dev/surfaces/app-home/)
3. Approve and install the app in `nepai` with the designated admin. Treat
   the installer and the linked operator as separate identities.

## Migration and health checks

- Apply `relay/migrations/*.sql` in lexical order to the dedicated relay
  database using `psql -v ON_ERROR_STOP=1`. Apply `migrations/*.sql` separately
  to the local database. The deployment should verify the pairing, OAuth flow,
  and event tables at readiness so a missing migration fails the startup check.
- Probe `GET /healthz` for listener liveness and `GET /readyz` for the
  relay database and schema check. Expect HTTP 200
  from both after migration and a non-2xx readiness result if the database
  or required schema is unavailable. Verify public TLS with a standard
  client and the Slack-signed URL challenge through ingress. Verify a request
  without a pairing proof cannot read any queue.
- Start local Difqo with `difqo server -slack-relay https://<relay-host>`.
  Supply a pathless origin, use the system trust store, and inject
  `DIFQO_SLACK_ENROLLMENT_TOKEN` into the local server environment for
  enrollment. Keep the local API on loopback. If the relay address changes during a pending claim or
  disconnect, restore the original address before retrying.
- Before the live test, verify the TLS fixture and loopback delivery suite on
  disposable PostgreSQL with both DSNs described in
  [ADR 0010](adr/0010-official-slack-connect.md).

## Local OAuth handoff

Send JSON actions with an HTTP POST to the local `/slack/connection` API.
Use the `pairing_id` returned by `begin` for each subsequent action.

| Action | What to do |
| --- | --- |
| `begin` | Create and enroll a pairing. Retry this action if the relay was unavailable. |
| `start_identity` | Open the returned `oauth_url` in the browser and sign in as the approved operator. |
| `refresh` | After the identity callback, check `verified_workspace` and `verified_operator`. |
| `start_install` | Open the returned `oauth_url` and complete the workspace installation. |
| `approval_needed` | Use after Slack requests workspace approval, then retry `start_install` once approved. |
| `refresh` | After the install callback, check for `ready_to_link` and verify the displayed `binding`. |
| `confirm` | Send the exact displayed `binding` to activate delivery. |
| `disconnect` | Send the current `generation` and `pairing_id` to revoke or cancel. |

The API returns authorization URLs for the two start actions only. The
enrollment credential and pairing proof stay on the server side. Each URL
must target Slack, include the expected relay callback, and have the
parameters for that OAuth flow. Slack callbacks still consume one-time
state on the relay.

## Acceptance in nepai

1. Start a new pairing as the pilot operator. If workspace approval is required,
   confirm the local state indicates pending approval until the admin approves
   the install. After OAuth completes, verify the displayed workspace,
   operator, and DM. Confirm locally before sending the acceptance goal.
2. From the linked operator's private app DM, send
   `goal Verify the nepai Slack pilot`. Verify exactly one local task with
   the current saved decision and confirm the relay clears the acknowledged
   goal body. Retry the same event in the approved replay harness and verify
   the original task remains the only task for that event.
3. Send a different test goal from a non-linked `nepai` member, an ordinary
   channel, and an unclaimed DM. Verify zero extra tasks. Verify an install
   by the admin did not make the admin the linked operator.
4. Stop and restart the local server while the relay queues a test goal.
   Verify delivery resumes and creates one task. Disconnect and send another
   test goal to the old DM. Verify the relay route is retired and no new
   local task appears. Reconnect through confirmation and verify a new event
   can be delivered.

Record only test outcomes and redacted identifiers. Live authorization and
deployment require maintainer approval.

## Railway staging

The approved pilot origin is `https://connections-staging.difqo.com`.
[Railway IaC](https://docs.railway.com/infrastructure-as-code) uses
`.railway/railway.ts` to define one Connections replica and a dedicated
`RelayPostgres` service. PostgreSQL has no public proxy. The relay uses the
private `DATABASE_URL` reference and Railway HTTPS ingress targets port
8080. Inside the same container, the proxy forwards to Connections at
`127.0.0.1:8766` without rewriting signed bodies or logging requests.

1. In the approved Railway project and staging environment, install the
   IaC SDK with
   `npm install --prefix .railway --no-save --package-lock=false railway@3.17.0`.
   Populate the named `DIFQO_CONNECTIONS_ORIGIN` and `DIFQO_SLACK_*` shared
   variables in Railway. Start with synthetic settings. Keep live values
   in Railway, and run `railway config plan` without showing values. Verify
   the project and environment, private database, Dockerfile, pre-deploy
   command, and readiness check before applying the reviewed plan.
2. On the new private database, Railway runs `/connections -migrate` from
   the built image before exposing the service. The command applies embedded
   migrations in a transaction, serializes concurrent deployments, and skips
   recorded migrations with matching checksums. For a database initialized
   outside this runner, reconcile the schema and migration ledger separately
   before linking it. A failure blocks deployment. Inspect the failed
   migration name, verify the database reference, and retry the deployment
   after fixing the cause. Keep the prior deployment serving if it remains
   compatible. Avoid rollback to an older schema.
3. After the synthetic health checks pass, attach
   `connections-staging.difqo.com` to the Connections service at port 8080.
   Apply the exact DNS records Railway returns, wait for TLS, and check
   `/readyz` and `/healthz` at the approved origin. Keep the database private.
   Rerun readiness after restarting Connections and verify pairing and
   queue records persisted on the same database volume.
4. Once the operator and app admin authorize live traffic, configure the
   Slack OAuth redirects `https://connections-staging.difqo.com/oauth/identity`
   and `https://connections-staging.difqo.com/oauth/install`, and the Events
   URL `https://connections-staging.difqo.com/slack/events`. Follow the
   local OAuth handoff and the scoped `nepai` acceptance above. For recovery,
   use the retirement and database restore procedure below before restoring
   ingress.

Go CI builds and runs the container against a private synthetic PostgreSQL
container. To repeat the image rehearsal locally, run:

```sh
docker build -t difqo-connections:smoke -f containers/connections/Dockerfile .
python3 scripts/connections-container-smoke.py difqo-connections:smoke
```

Set `CONTAINER_RUNTIME=podman` for the smoke script if the image was built
with Podman. The rehearsal exercises migration replay, signed challenge
and tamper rejection at the ingress, protected enrollment, restart,
and revocation.

## Staging runbook

Use a dedicated relay database and the packaged Connections binary from one
reviewed commit. Test with synthetic data before authorizing Slack traffic.

1. Record the commit with `git rev-parse HEAD`. Build on the staging host
   with the Go toolchain pinned by Go CI using
   `go build -trimpath -o bin/connections ./cmd/connections`. Record the
   artifact checksum with `shasum -a 256 bin/connections`.
2. Provision a dedicated database. Apply relay migrations once in lexical
   order using the migration identity. On an existing database, apply only
   migrations known to be outstanding. Start the binary with the service
   manager using `bin/connections -listen 127.0.0.1:8766` and the hosted
   configuration above. Keep a separate migration/maintenance identity if
   the runtime role has no schema privileges.
3. At HTTPS ingress, forward `/slack/enroll`, `/slack/events`,
   `/oauth/identity`, `/oauth/install`, and `/pairings/*` to the loopback
   listener. Preserve methods, bodies, query strings, authorization and
   Slack signature headers. Disable request logging on these routes or
   exclude those sensitive fields. Disable upstream retries on mutating
   requests. Route health probes without credentials and expose only their
   status. Keep the listener unavailable from the public network.
4. Check `/healthz` and `/readyz` over the configured origin with certificate
   verification enabled. Confirm unsigned event requests and unauthorized
   enrollment are rejected. Run the synthetic rehearsal below before
   accepting real Slack events.

### Retention and rollback

Recommended pilot policy is to scrub undelivered goal text after 24 hours,
with an hourly job and an alert on missed runs. With an on-time job, a body
can remain for up to 25 hours. A receipt or explicit revocation scrubs it
earlier. The job acts on queued bodies. It cannot recall a body already
returned to an authenticated local poll. Keep pairing tombstones and event
IDs for the pilot so retries cannot create duplicate tasks. Apply the
approved retention policy to backups and any log storage as well.

Use a secret-injected maintenance DSN and the exact configured app and
workspace/operator IDs. From the checkout, run the following with the
approved retention hours (24 shown).

```sh
pilot_scope="${DIFQO_SLACK_WORKSPACE_ID}:${DIFQO_SLACK_OPERATOR_ID}"
psql -X --dbname "$DIFQO_RELAY_DATABASE_URL" -v ON_ERROR_STOP=1 \
  -v app_id="$DIFQO_SLACK_APP_ID" -v pilot_scope="$pilot_scope" \
  -v retention_hours=24 -f relay/operations/expire-slack-goals.sql
```

To roll back the pilot, stop local polling, pause the pilot ingress routes,
and drain and stop Connections before changing the binary. Then retire the
pilot scope and scrub queued bodies in one transaction.

```sh
pilot_scope="${DIFQO_SLACK_WORKSPACE_ID}:${DIFQO_SLACK_OPERATOR_ID}"
psql -X --dbname "$DIFQO_RELAY_DATABASE_URL" -v ON_ERROR_STOP=1 \
  -v app_id="$DIFQO_SLACK_APP_ID" -v pilot_scope="$pilot_scope" \
  -f relay/operations/retire-slack-pilot.sql
```

If a database restore is required, keep ingress paused and Connections
stopped. Run `relay/operations/retire-slack-pilot.sql` again against the
restored database using the same app and pilot scope. A pre-retirement backup
can contain active claims and queued bodies.

Verify the active database has no claimed pairings, OAuth flows, or queued
bodies in the retired scope, and that its replay IDs remain. Keep ingress
paused until the binary is compatible with that schema and passes health
checks. Resuming the pilot requires fresh pairing. Local tasks that already
committed remain in local Difqo.

### Rehearsal

Use the disposable databases described in [ADR 0010](adr/0010-official-slack-connect.md)
and run the same tests as Go CI.

```sh
go build -o bin/connections ./cmd/connections
go test -race -count=1 ./internal/slackrelay ./internal/slackintake
python3 scripts/connections-smoke.py bin/connections
python3 scripts/slack-staging-smoke.py
```

The executable smoke checks startup, health, and shutdown. The staging smoke
creates its own disposable schema and checks expiry, rollback, replay ID
preservation, scope isolation, and repeatability. The Go tests cover synthetic
TLS pairing, signed events, and local receipts. Verify the real ingress and
Slack configuration separately during the authorized pilot.

### Owner decisions before a live pilot

| Decision | Required input |
| --- | --- |
| Origin and infrastructure | Exact stable HTTPS origin, staging host and TLS/ingress owner, dedicated database and migration owner |
| Slack app and people | Confirm the pilot app, the `nepai` workspace ID, approved operator ID and installing admin. Authorize both OAuth redirects and the Events URL at the chosen origin. |
| Secrets | Name who provisions the relay DSN, Slack client and signing secrets, stable token encryption key, and pilot enrollment token via a secret store. Only the enrollment credential goes to the local pilot operator's server. |
| Retention | Approve the proposed 24-hour cutoff and hourly sweep, the missed-run alert and owner, and backup retention. |
| Rollback and acceptance | Name who can pause ingress and retire the pilot, where the prior compatible artifact is kept, and who can authorize the live acceptance test. |
