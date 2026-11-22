# 0002. Recover interrupted worker attempts with PostgreSQL leases

## Context

The local worker can stop after claiming a task. The first schema permits one
attempt per task, leaving an incomplete claim stranded. Reclaiming work must
allow the old worker to fail safely if it reconnects with a result. See
[#7](https://github.com/nep-ai/difqo/issues/7).

## Decision

PostgreSQL owns each attempt's deadline. Claims grant a 30-second lease and
each successful renewal gives the active attempt another 30 seconds. An
expired lease cannot be renewed. The next claim can create a new attempt with
its own ID, up to three attempts total per task. An explicit worker result
completes the task, including a result with a nonzero exit code. Three expired
attempts exhaust recovery and leave the task inspectable.

Claimers lock the task and recheck the latest attempt in the same transaction.
Renewal, completion, and that recheck lock the attempt row before checking its
deadline. The result API rejects expired attempts, including attempts that have
already been replaced. It accepts an identical replay of a committed result and
rejects attempts to change that result. Expiry is derived on read and claim, so
the control plane can restart without a timer or recovery process.

## Alternatives and consequences

Reusing an attempt row would require a separate generation token to distinguish
late workers and would overwrite evidence of earlier claims. A background reaper
could update expired tasks proactively, at the cost of another process to recover
and coordinate. Append-only attempt identities and recovery on claim keep the
API and process topology small. The latest attempt remains visible in task reads
and earlier attempts remain in PostgreSQL.

Migration 0002 preserves attempt IDs and existing results. It numbers legacy
attempts as one and treats unfinished legacy claims as expired. Stop the old
worker and server before applying it. The existing Git check times out after five
seconds and fits in one lease. Longer-running executors must renew while running
and stop on lease loss. Lease expiry permits retry and protects committed results.
Work that performs side effects needs its own idempotency guarantees.
