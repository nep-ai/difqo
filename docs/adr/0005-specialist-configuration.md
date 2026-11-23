# ADR 0005 · Approve specialists for explicit local projects

## Context

A local operator needs to review what a specialist does and where it may do it.
The same specialist can serve multiple projects. The worker currently takes
its checkout at startup, and multi-project dispatch is tracked separately in
[#20](https://github.com/nep-ai/difqo/issues/20).

## Decision

Load project bindings from operator configuration at server startup. Use stable
project IDs and derive the binding identity from the canonical checkout and an
operator-provided version. Persist the reviewed name, purpose, expected output,
profile and project bindings in a proposal. On approval, atomically record the
decision and associate the proposal with an immutable specialist revision.
Use one bounded snapshot review profile. The operator can disable a specialist
and retain its history. Configuration and eligibility stay in the existing
local Go service and PostgreSQL.

## Alternatives and consequences

Storing editable configuration alone would lose the exact scope approved by
the operator and make concurrent updates harder to reject. A project
administration surface would add mutation and recovery flows to the first
slice. Startup configuration keeps registration with the trusted local
operator. Catalog changes need a server restart, and replaced bindings require
fresh approval. A checkout replaced in place requires a new binding version.
Run dispatch must use the selected project's registered checkout and enforce
its execution boundary before a specialist can run.
