# ADR 0008 · Bind each specialist run to one project

## Context

A specialist can have approval for several local projects. Starting work
requires one project and one approved configuration revision. Legacy workers
take a checkout at startup and cannot infer which project a specialist run
intended to access.

## Decision

Queue specialist runs through the Go control plane with the chosen project
binding, immutable approved revision, and the project's current saved decision
revision. Store the selection alongside the task. Scope decisions by project
ID and binding. Existing decisions retain their ordinary-goal scope.
Serialize queue and claim authorization with updates to the specialist. Keep
the existing lease and result records, and expose all attempts for a run.

A specialist worker supplies its own registered project and computes the
binding from its canonical checkout and operator-provided version. The
server matches that binding against current registration, pinned selection,
and current scope before leasing a run. Legacy workers remain in the ordinary
queue. The snapshot-review profile runs fixed read-only Git commands using
one committed tree. The report includes the selected goal and decision as
context. This profile uses no external provider or user-supplied command.

## Alternatives and consequences

One worker that dynamically switches repositories would reduce startup
commands and introduce checkout routing and credential isolation at the same
time. A worker bound to one checkout makes its execution inputs explicit.
Using the existing patch-producing Codex profile here would require a
separately approved access policy and a worker capable of enforcing it.

Removing the project from current scope or changing its binding prevents new
claims. Active attempts can report their result. Lease expiry permits
automatic recovery within the existing attempt limit. Manual run controls and
additional profiles can build on this boundary. A retry uses the original
decision and specialist revision and reads the bound project's committed HEAD
at that attempt, so the report states the exact commit inspected.
