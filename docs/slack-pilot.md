# Official Slack pilot checklist

This checklist covers the first official Slack pilot in the `nepai` workspace.
The local client supports a configured HTTPS relay. Build the Connections
process with `go build ./cmd/connections`. It serves the Slack provider over
a numeric loopback listener behind an HTTPS ingress. The operator-facing
OAuth handoff remains a prerequisite for live acceptance.

## Before deployment

- Designate the official Slack app and an admin who can approve its install
  in `nepai`. Record the workspace's Slack team ID and the pilot operator's
  Slack user ID. The workspace display name alone is insufficient.
- Choose the stable HTTPS origin and identify its deployment owner. Reserve
  it for Connections so an existing pairing can recover at the same origin.
- Keep the Connections listener on loopback and route the configured HTTPS
  origin to it. Keep the Slack callback and signed event routes reachable.
  The process uses only Slack's official origin for provider requests.
- Ship the local handoff. The approved operator must enroll the locally
  generated pairing before starting OAuth. The local service then calls
  the identity and installation routes with its pairing proof. Only the
  returned OAuth URLs go to the browser.
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
  Supply a pathless origin and use the system trust store. Keep the local API
  on loopback. If the relay address changes during a pending claim or
  disconnect, restore the original address before retrying.
- Before the live test, verify the TLS fixture and loopback delivery suite on
  disposable PostgreSQL with both DSNs described in
  [ADR 0010](adr/0010-official-slack-connect.md).

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
