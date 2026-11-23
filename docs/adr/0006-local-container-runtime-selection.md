# 0006. Select and validate a local container runtime before claiming work

## Context

[ADR 0003](0003-bounded-codex-execution.md) uses Docker to run a bounded
Codex turn. Docker and Podman provide comparable container operations, but
the presence of a CLI does not establish that its engine can run the local
image with Difqo's bind mounts, resource limits, and nested Codex sandbox.
See [#22](https://github.com/nep-ai/difqo/issues/22).

## Decision

Try Docker and then native Podman in automatic mode. Accept an explicit
Docker or Podman selection or a trusted compatible executable by name or path.
Validate the engine and local image, then use disposable containers to test
the runtime flags, host-backed workspace, and workspace-only sandbox with
synthetic credentials. Resolve the executable before claiming work and use
it for every container command for that task. Report an unusable runtime
locally and leave the task queued.

Pin the worker's image ID during preflight and record it with the snapshot
described in [ADR 0005](0005-exact-patch-review.md). The server uses its own
operator-configured runtime selection for review reruns. It resolves the
saved image ID and validates the offline check container. Pass the same
runtime to both processes when both should use a specific engine.

Use Podman's rootless `keep-id` mapping to preserve host access to workspace
and credential mounts. Keep the outer container restrictions and the nested
Codex permissions profile. Allow an explicit per-container SELinux label
exception for the Codex invocation when the host policy blocks bubblewrap.
The offline diff and check containers continue to run with the engine's
labeling policy and without a credential mount.

## Consequences

The worker starts two disposable probes before a claim, including when
there is currently no task to run. Images and shared directories must
be available to the selected engine. Docker installations whose defaults
block the sandbox require a scoped engine policy. Compatible executables
must honor the same isolation flags and command contract. See the
[local setup guide](../development.md#runtime-selection-and-linux-sandbox-setup).
