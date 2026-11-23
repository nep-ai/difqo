# ADR 0005 — Review a captured patch against its original base

## Context

The local worker captures a diff and check log. An approval needs a durable
identity for the reviewed patch and evidence that the configured check passed
on that exact change. The source checkout can move or contain uncommitted
changes between execution and review.

## Decision

Save an append-only project decision revision in PostgreSQL and bind the latest
saved revision to each goal at submission. Pass the bound revision to Codex.
Record the base commit, patch SHA-256, local image ID, and configured check
command with the completed attempt. Keep the shallow base and original
artifacts in the private host artifact directory.

The original check for a captured patch and each review rerun independently
clone the saved base, verify its commit, and apply the patch in a temporary
workspace. The check runs in the same pinned local image without networking
or mounted Codex credentials. The review API uses the same execution code as
the worker and verifies the patch digest before serving, rerunning, or
approving the patch.

Store rerun start and completion records in PostgreSQL. A newer rerun
supersedes the previous check for approval. The approval transaction
serializes with rerun starts and requires the latest completed passing rerun.
The approval refers to that check and its attempt, which fixes the patch
identity. It is a record of the review decision. The developer can download
the approved patch and apply it manually.

## Consequences

Previously captured evidence remains viewable. Review requires a new run
with snapshot metadata. Reruns need the original image and base artifacts
to remain available. The artifact directory and database are trusted local
operator storage. Review assumes the configured check is meaningful and
can run with the files in the repository and the tools in the pinned image.
Each reviewable attempt retains a shallow copy of the base. Changing a
decision affects new goals, while existing attempts keep their original
decision revision for traceability.
