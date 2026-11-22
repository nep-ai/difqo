# 0003. Run a bounded Codex turn in a disposable Docker workspace

## Context

Difqo needs one local, reviewable Codex turn whose patch and check output remain
available after execution. The lease API already provides attempt identity and
guards result submission. See [#8](https://github.com/nep-ai/difqo/issues/8).

## Decision

The worker opt-in mode makes a shallow, local clone of the selected checkout's
committed HEAD. The worker's Docker adapter mounts only that clone and, for the
App Server, the operator's dedicated Codex home. The Codex adapter opens one
ephemeral thread and one turn over newline-delimited JSON-RPC. Interactive
requests are denied. Codex's workspace-only profile denies access to the login
directory from tool commands. Authentication errors produce a terminal blocked
result, so recovery from lost credentials is an explicit operator action.

The worker renews the attempt lease during execution. Each container has a
process deadline, CPU and memory limits, and a disposable root filesystem.
After the turn, the worker captures the diff against the initial revision and
runs the operator's check in a credential-free container with networking
disabled. It saves the outputs on the host outside the mounted checkout and
records their directory relative to the artifact root. PostgreSQL commits the
reference with the same stale-attempt checks as any result. A failed result
submission may leave an unreferenced directory for local inspection.

## Alternatives and consequences

Using a persistent container would simplify interactive iteration and retain
environment state from previous attempts. Fresh containers and shallow clones
make the inputs easier to identify and keep changes out of the source checkout.
Putting all output in PostgreSQL would simplify retrieval through the API and
move bulk execution artifacts into the control plane. Host files let the local
worker keep patch and check evidence available independently of container
lifetime. This slice runs trusted fixtures with a local Docker engine. Host
startup and restart behavior remain the Mac mini verification task.
