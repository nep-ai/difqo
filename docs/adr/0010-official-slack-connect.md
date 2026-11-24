# ADR 0010 · Connect the official Slack app through an outbound relay

Status: Proposed. Issue [#11](https://github.com/nep-ai/difqo/issues/11) remains open.

## Context

A local operator wants to submit a goal from a private Slack DM and review
the same task in Chief of Staff. The shipped private app uses Socket Mode
and the same local intake transaction as the offline fixture. The official
app needs installation on Slack, proof of the operator's Slack identity,
and delivery to a local instance without an inbound listener. Its relay
will handle Slack integration while PostgreSQL holds local task, evidence,
and approval state. [Local Slack intake ADR](0009-local-slack-intake.md)

## Proposed connection flow

1. **Connect Slack** starts the flow from the local UI. Display the relay
   disclosure next to the action. The local service creates a pairing with
   a fixed relay origin over HTTPS and holds the proof of possession. The
   browser receives an expiring start URL. A start URL opens the browser
   flow, while claiming the result requires proof held by the local
   service. Keep the relay proof and Slack credentials outside browser URLs,
   browser storage, and logs. The local service polls for transitions.
2. **Link the operator.** Run Sign in with Slack in the initiating browser.
   Validate the signed ID token, issuer, audience, expiry, nonce, and
   single-use state. Read the verified workspace and Slack user IDs and
   associate them with this pairing. Request the minimum scopes that return
   both IDs. Start with `openid profile` and confirm the claims in the
   fixture and a future test workspace. Discard the identity access token
   after linking. Keep the install OAuth state separate from identity state.
   Reuse an existing valid installation for the verified workspace.
   [Slack identity flow and scopes](https://docs.slack.dev/authentication/sign-in-with-slack/)
3. **Install if needed.** Redirect to Slack OAuth v2 with the verified
   workspace as a hint. Exchange the code on the relay after verifying the
   install state's expiry and purpose. Validate the returned app, actual
   workspace, workspace-level installation, and required bot scopes.
   An installing admin's OAuth identity is install metadata. It grants
   the admin no access to the operator's pairing or DM. For inbound, start
   with `im:history`, `im:write`, and `chat:write`. Enable user messages in
   the App Home Messages tab, which requires `chat:write`, and subscribe to
   `message.im`. Use `conversations.open` with exactly the verified operator
   ID to resolve the bot's DM. Confirm the returned conversation ID and DM
   type before enabling intake. The initial relay uses this scope set for
   inbound setup and delivery. Posting replies is a later feature.
   [Slack OAuth installation](https://docs.slack.dev/authentication/installing-with-oauth/) ·
   [App Home Messages tab](https://docs.slack.dev/surfaces/app-home/) ·
   [Direct-message events](https://docs.slack.dev/reference/events/message.im/) ·
   [Open a DM](https://docs.slack.dev/reference/methods/conversations.open/)
4. **Confirm locally.** Once the relay has a matching installation and
   verified operator DM, show the selected workspace and operator in Chief
   of Staff. The local operator confirms the binding before goal delivery
   starts. The local service claims the result once and persists it.
   Repeat confirmations by the same instance return the same result. An
   expired or cancelled pairing requires a new connection attempt. Holding
   a browser URL or an install callback alone cannot claim the local result.

The first connection may involve two distinct Slack authorizations, for
identity and installation. On an already-installed workspace, the operator
only needs the identity flow and local confirmation. An admin can install the
app independently. The operator resumes from Chief of Staff and proves
their own identity before linking the DM.

| Chief of Staff state | Trigger and recovery |
| --- | --- |
| Connecting | A pairing is open. Show **Resume** and **Cancel**. Browser abandonment leaves no active binding. |
| Workspace approval needed | Slack reports an install restriction or the operator returns after requesting approval in Slack. Preserve the verified workspace and show **Resume installation**. Use Slack's notification or an explicit retry as the signal to recheck. Approval alone may still require installation. A generic OAuth denial remains a cancelled or denied install. The official app requests no admin scopes to read workspace approvals. |
| Ready to link | The workspace installation and operator DM match. Show the identities and require confirmation in the originating local instance. |
| Connected | The local binding is active and the relay is reachable. Show the linked workspace and DM with **Disconnect**. |
| Reconnecting | The outbound connection failed. Keep the local binding and retry with backoff and the last durable receipt. |
| Reconnect required | The app was uninstalled, required scope or stored authorization was lost, or the local pairing expired. Stop delivery and repeat the relevant Slack flow. Preserve task and approval history. |

Workspace administrators can approve or restrict an install in Slack.
The operator can complete installation after approval. An install to a
different workspace returns the user to the original selection or a new
pairing. Org-wide installations are outside the first workspace-level slice.
[Slack app installation and requests](https://slack.com/help/articles/202035138-Add-apps-to-your-Slack-workspace) ·
[Slack workspace app management](https://slack.com/help/articles/360024269514-Manage-app-requests-for-your-workspace)

## Relay and local service boundary

- The relay owns Slack OAuth callbacks, installation credentials, Slack
  signature verification, private-DM routing, and a durable queue of scoped
  events awaiting local receipt. The local connector initiates all relay
  requests. Scope its relay credential to one installation and one local
  binding. The browser gets display state from the local service.
- Disclose that Slack goals transit and are temporarily stored by the relay
  for delivery. Replies will transit it when enabled. Keep raw Slack
  events and message bodies out of application logs. Retain only
  normalized messages needed for bounded delivery, delete their bodies
  after local receipt, and expire undeliverable messages under a disclosed
  retention policy. Local repositories, evidence, task state, and
  approvals remain on the local machine.
- Verify the Slack signature against the bounded raw HTTPS body and check
  the signed request's timestamp before decoding. For a verified
  `url_verification` request, return HTTP 200 with the plaintext `challenge`
  before applying workspace or DM binding checks. This startup handshake
  does not create a task or a delivery record. Derive app, workspace,
  sender, and conversation from that verified event. The relay acknowledges
  accepted events after durable queue insertion, within Slack's delivery
  deadline. Discard well-formed events outside the active exact binding.
  A persistence failure asks Slack to retry. Use the Slack event ID with
  app and workspace scope for relay deduplication. The local service trusts
  the authenticated relay to attest Slack origin and independently checks
  the active app, workspace, operator, DM, and binding generation.
  [Verify Slack requests](https://docs.slack.dev/authentication/verifying-requests-from-slack/) ·
  [Events URL verification](https://docs.slack.dev/reference/events/url_verification/) ·
  [Slack event delivery and retries](https://docs.slack.dev/apis/events-api/#responding-to-events)
- Batch delivery over outbound HTTPS. Local receipt follows the transaction
  that records the event and shared local task. A lost acknowledgement
  repeats delivery with the same event ID. Preserve event identity across
  restart, reconnect, and transport changes for the same Slack app and
  workspace. Retire the previous generation on disconnect and reject old
  deliveries before task creation. A relay outage cannot block ordinary
  local tasks.
- An accepted goal uses the same saved decision and task creation path as
  private Socket Mode. The local UI keeps existing patch review authority.
  Any later DM approval must authenticate the active binding and reference
  the exact task, patch, and check used by the existing approval contract.
  A message body cannot perform approval, choose a filesystem path, or
  expand project scope. Treat it as goal text in the existing executor.

## Implementation and acceptance sequence

| Slice | Synthetic acceptance |
| --- | --- |
| Local binding lifecycle | With fake relay statuses, restart each side during connect and resume with the same pairing. A second instance, reused or expired state, the wrong workspace, or an installing admin trying to claim the pairing cannot activate intake. Cancel and disconnect revoke the current generation. Prove private and official modes are mutually exclusive in one local database and keep tasks and replay history when switching. |
| Relay identity and install | Mock Slack callbacks and code exchanges. Cover identity and install succeeding in either order, pending approval with no callback, user cancellation, app or workspace mismatch, unsupported org installation, missing Messages tab scopes, replayed callbacks, and reconnect after revocation. Admin installation can satisfy the workspace prerequisite but only the verified local operator can link their DM. |
| Durable relay pairing | Recreate the relay and its database pool between start, callback, and poll. An unused callback remains usable and a consumed, replaced, expired, or revoked callback cannot reach the token exchange. Persist pending approval and preserve operator versus installer identity. Revoke while a callback or DM lookup is in flight and verify the old pairing stays retired across restart. |
| Signed webhook and relay queue | Echo a valid signed URL challenge before any pairing, without querying PostgreSQL. Reject tampered, unsigned, stale, or oversized requests. A proof-authenticated claim activates only the displayed binding. Queue one goal for concurrent or restarted delivery of the same app, workspace, and event ID. Discard other workspaces, senders, DMs, and bot messages. Return a retriable error if PostgreSQL fails. Revoke the claim and scrub queued goal bodies while retaining replay identifiers. |
| Local delivery | Retry after relay restart and simulated loss of the local receipt. Each delivered event creates one task bound to the existing saved decision. Simulate a disconnect with delivery in flight and verify the retired local generation cannot import it. GitHub intake and the private Socket Mode fixtures still pass. |
| UI and live acceptance | On fake endpoints, exercise pending, installed-but-unlinked, connected, offline, revoked, and reconnect from Chief of Staff. Enable the entry point for real users only after the official test app and relay can complete the flow. With owner authorization, verify in one approved test workspace that only the linked operator's DM creates a local, reviewable task. Later slices cover status, cancellation, action-specific approval, and replay-safe replies. |

The local connection lifecycle and migration now preserve existing private
bindings. The next relay slice provides a numeric loopback HTTP fixture in
`internal/slackrelay`. Its proof-authenticated `PUT /pairings/{id}` returns the
status consumed by the local lifecycle. Separate proof-authenticated
`POST /pairings/{id}/identity` and `POST /pairings/{id}/install` return short-lived
authorization URLs. Slack returns identity results by form POST and install
results by query parameters. Callbacks consume purpose-specific state and the
identity exchange also verifies the signed nonce. The local caller can record
an explicit request for workspace approval with
`POST /pairings/{id}/approval-needed`. A denied callback itself records no
approval request. A matching workspace and operator can become
`ready_to_link` after the proof-authenticated poll resolves the DM. Local
confirmation remains the activation boundary.

The relay fixture stores pairing and callback state in a separate PostgreSQL
database. Pairings are scoped to the app. The relay stores hashes of local
proofs and OAuth states, and records callback consumption before contacting
Slack. After the exchange, a second transaction requires the same current
flow and an unexpired, active pairing. If the relay stops during an exchange,
start a fresh authorization flow for that purpose. The pairing and any other
completed flow remain available. These transactions enforce one-use state
and prevent stale responses from overwriting newer state across processes.

The fixture seals the synthetic bot token for the pairing and workspace with
a key supplied to the test handler. Keeping that key across restart lets the
relay resolve the DM after an install-first flow. The fixture discards the
credential when the DM is resolved or the pairing is retired. A proof-authenticated
`DELETE /pairings/{id}` retires the pairing. The relay also retires a pairing
when a poll or callback observes expiry, or it receives a revoked token error
during DM lookup. Retirement clears provider data and keeps a tombstone so
an old poll cannot recreate the ID.

Apply `relay/migrations/*.sql` in order to a **separate disposable PostgreSQL
database**. Set `DIFQO_SLACK_RELAY_TEST_DATABASE_URL` to that database
and run `go test -race -count=1 ./internal/slackrelay`. The tests recreate the
HTTP handler and database pool against the same DB and use only numeric
loopback Slack simulators. The local app's migrations remain in
`migrations/`.

The Slack provider module of the planned Connections service accepts signed
events at `POST /slack/events`. It verifies the v0 signature over at most
64 KiB of raw body and accepts timestamps within five minutes. A verified
URL challenge returns plaintext before the queue is consulted. Message
normalization shares the private connector's Slack DM and goal rules.

The local client in the fixture claims the exact displayed binding through
a proof-authenticated `POST /pairings/{id}/claim`. The claim is idempotent
and activates one route for that app, workspace, operator, and DM. The
unclaimed pairing expires after 15 minutes. A claim stays active until
revoked. The queue locks the claimed row while inserting the goal, pairing
ID, and event identity. PostgreSQL deduplicates by app, workspace, and event
ID, including after a new pairing. Slack receives HTTP 200 after the write
or an intentional skip, and HTTP 503 if the queue cannot commit. Revocation
clears the old claim and queued goal bodies in the same transaction while
retaining replay identifiers.

When the local server starts with `-slack-relay-fixture`, it polls outbound.
Confirmation persists an intent before claiming the exact binding. An
interrupted claim retries with the same pairing proof. Disconnect retires
the local generation first and persists the pending relay revocation until
the relay acknowledges it. A pending disconnect blocks new pairing so a
relay outage cannot orphan an active route.

The relay uses the pairing proof for `GET /pairings/{id}/events` and
`PUT /pairings/{id}/events/{event_id}`. A poll returns the active binding and
at most eight queued goals. The local store checks the exact binding and
generation under the connection lock and commits the task with the scoped
event ID. Only then does the local connector send the receipt. The relay
clears the goal body but retains the event ID, so a lost receipt or replay
does not create another task. A batch received after local disconnect
cannot create a task or send a receipt for the retired generation.

For the full delivery test, apply `migrations/*.sql` to a second disposable
database and set `DIFQO_SLACK_DELIVERY_TEST_DATABASE_URL` as well as the relay
test database URL. Run `go test -race -count=1 ./internal/slackrelay`.
Automatic retention, workspace installation reuse, live Slack revocation
events, and hosted deployment remain follow-up work.
An outgoing-only local client keeps the workstation off the public Internet.
A direct OAuth callback on each workstation would distribute Slack secrets
and require a reachable callback for event delivery. An optional private
Socket Mode app already provides the self-hosted alternative.

Before live acceptance, the maintainer needs to designate an owner for the
official distributed app, choose the relay's HTTPS origin and deployment
owner, and authorize a test workspace that can exercise admin approval.
Those are deployment prerequisites. The lifecycle and relay fixture slices
can proceed without those inputs.
