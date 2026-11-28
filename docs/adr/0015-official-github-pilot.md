# 0015. Broker read-only tokens for the official GitHub pilot

## Context

[Issue #12](https://github.com/nep-ai/difqo/issues/12) needs a live path
for an already-installed Difqo GitHub App on one selected repository.
[ADR 0007](0007-github-intake.md) established local polling, replay
protection, and the shared task and patch-review path. The Connections
listener already serves the official Slack pilot behind HTTPS ingress.

## Decision

Add a single-owner GitHub pilot to Connections. The service operator enrolls
one app, installation, repository, and owner using numeric GitHub identities.
A dedicated high-entropy proof binds the local instance to that enrollment.
The service accepts no caller-supplied repository, installation, permissions,
or owner. Its secret configuration holds the app private key and the proof
digest.

On each poll, Connections signs a short-lived app JWT and asks GitHub for an
installation token restricted to one repository and read-only issue, pull
request, and metadata access. Before releasing it, Connections checks both
the actual token scope and the configured owner's current repository access
using the numeric GitHub user ID from the permission response. The local
poller receives the token and enrolled identity in memory and polls GitHub
directly. Token or permission errors stop intake and preserve its local
cursor. Task, review, and publication policies keep their existing owners.

A GitHub-only Connections deployment needs no relay database. When enabled
alongside Slack, Slack retains its existing schema and readiness checks. See
the [GitHub pilot runbook](../github-pilot.md) for configuration and testing.

The installed staging pilot accepts the enrollment proof through the local
Connections panel and keeps it in the per-user private service directory.
The panel exposes the approved repository and an explicit sync action. A
local disconnect removes the proof and preserves the cursor and previously
imported tasks. Installation tokens stay in memory during sync.

## Tradeoffs

A deployment-managed grant fits the first single-owner pilot and avoids
retaining a more broadly authorized GitHub user OAuth token. Self-serve
multi-user installation, account linking, and recovery still require a
browser authorization flow and independently revocable grants. This pilot
supports revocation by removing GitHub access or rotating the enrollment
proof. A previously issued token can remain valid until GitHub revokes it or
it expires. The poller fetches a fresh grant every invocation.

GitHub replies and pull request writes require their separate approved
publication and review flow.
