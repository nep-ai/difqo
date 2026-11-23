# ADR 0009 · Import authorized Slack DM goals into the local queue

## Context

Issue #11 calls for optional Slack intake. The planned default is Connect Slack
in Chief of Staff through the official Difqo app and a hosted relay. The relay
will handle Slack delivery and credentials. A private app can use local Socket
Mode. Both paths need the same local identity and replay checks.

## Decision

Accept an authenticated, normalized private DM event at a local boundary and
match its app, workspace, operator, and conversation to one persisted binding.
The first slice supplies an offline stdin harness for this boundary. A local
operator supplies the binding as command arguments. Each future network
adapter authenticates its own delivery and establishes the linked identity
before constructing the normalized event.

Only an explicit goal becomes a task. Reuse the same task creation path as
GitHub intake and ordinary local goals, including the saved project decision.
Serialize intake on the binding row and write the source event ID and task in
one PostgreSQL transaction. A replay returns successfully after finding the
original task. The first binding is preserved after restart.

## Consequences

The local queue, evidence, and patch approvals remain authoritative. The
existing patch flow validates the patch and check in Chief of Staff. Future
Slack approval controls must call that contract with explicit action and
identity context. Connection flows, linking and reconnect, cancellation, and
a deduplicated reply path belong to subsequent slices. The offline harness
uses a fixture database and makes no Slack API calls.
