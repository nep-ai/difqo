# 0007. Import GitHub comments through a local read-only poller

## Context

[Issue #12](https://github.com/nep-ai/difqo/issues/12) brings GitHub issue and
pull request conversation comments into the local task and patch approval flow.
The task queue and [exact patch review](0005-exact-patch-review.md) already own
decisions, attempts, check results, and approvals.

## Decision

Provide a one-shot outbound poll with an operator-set repository ID, linked
GitHub user ID, and enrollment start time. Consume a pre-issued short-lived
GitHub App installation token. Verify repository access against GitHub's
installation repository list before reading comments. Accept requests made by
the linked numeric user ID after enrollment. Persist origin, comment identity,
task, and cursor together in PostgreSQL. The task uses the same creation path
and captures the same project decision as a local goal.

Repeat the boundary second because GitHub's timestamp filter is exclusive.
The unique repository and comment key prevents duplicate tasks after retries,
edits, or concurrent polls. Refuse a multi-page window so this slice never
moves a cursor past an unprocessed page. A failed authorization response
leaves the cursor unchanged. The local operator selects the trusted checkout
with the matching repository ID at claim time. Default workers stay on the
local queue.

A GitHub comment can supply a task goal. Only the local human review flow can
approve its exact verified patch in this slice. The poller has no outbound
write path and never forwards the installation token to a worker.

## Next slices

1. Add an official app connection flow and hosted token issuer. Verify the
   authenticated user's installation grant and repository enrollment on every
   issuance. The host holds the app key and returns installation tokens
   restricted to enrolled IDs and minimal permissions. The local instance
   polls GitHub directly. Verify denied requests for other installations and
   repositories before enabling onboarding.
2. Add optional local minting for a private GitHub App, using the same
   enrollment rules and poller. Verify that flow with no hosted Difqo calls.
   Support paginated recovery, reconnect, and unlink in local connection
   management before relying on unattended polling.
3. Define the approved policy for publication and implement a durable delivery
   record for approved results before adding GitHub writes. Recheck access and
   patch/check approval before delivery, and reconcile retries to the same
   originating conversation.

## Tradeoffs

A local poller works behind NAT without a public webhook. A webhook or
official token service would introduce hosting and identity setup before the
local authorization and replay contract could be exercised. A synthetic GitHub
API and disposable PostgreSQL schema verify this slice without external
credentials. The single-page window prioritizes preserving the cursor over
processing a burst that exceeds the current bounds.

See [GitHub comment reads](https://docs.github.com/en/rest/issues/comments#list-issue-comments-for-a-repository)
and [installation token scopes](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app).
