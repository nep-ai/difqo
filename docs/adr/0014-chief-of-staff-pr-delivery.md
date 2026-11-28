# ADR 0014 · Shared Chief of Staff conversation and pull request delivery

Status: Proposed for [Slack conversation][slack], [agent review][review], and [GitHub delivery][delivery].

## Context

An operator gives Chief of Staff a goal, clarifies intent, and expects a
tested result. The local app already records exact verification evidence for
one attempt. Slack intake can save a linked operator's goal locally. GitHub
intake can poll a selected repository using a scoped credential. Making a
GitHub pull request the default result for eligible projects adds a publication
boundary, an independent review attempt, and status updates in the conversation.

## Decision

### One Chief of Staff across the web app and Slack

The web app is available with a local installation. Connect Slack optionally
to use the same Chief of Staff in the linked operator's private DM. Both
surfaces can submit a goal, answer a clarification, ask for status, request
cancellation or a revision, make a policy decision, and see the result. The
local service owns the conversation, task state, and policy. Each surface
uses the same task and action identifiers. A conversation can cover several
projects, with one resolved project per run. When context could refer to
several open tasks or projects, ask which one the operator means before
starting work or applying an action.

The Slack DM accepts plain text, a message with one or more files, and
files sent on their own. The web app accepts the same inputs. When a file
arrives without a clear request, ask what to do with it. Start with
images, documents, text logs, source files, and common structured data.
Show when a file is available, waiting, oversized, unsupported, or
unavailable. If a missing file matters to the task, ask for an alternative
before dispatch. Treat file names and content as untrusted task input.

Process only file references from the linked DM. Through the official
relay, resolve Slack-hosted files using `files:read`, stream bounded
downloads to the authorized local client, and persist them in local
conversation storage. The private app connector fetches the same files
directly. File references awaiting delivery follow relay retention. Preserve
message and file identity on replay, show remote-provider files as links,
and send Slack credentials only to approved Slack file hosts.
[Slack file API][slack-files]

The local web composer initially accepts PNG, JPEG, and short UTF-8 text
attachments with a coding request. The task and attachments are committed
together in local PostgreSQL. History responses contain file metadata only.
Workers declare support before claiming a task with file data, and the Codex
worker forwards the supported files in the same App Server turn as the task.
The installed Mac uses the same attachment validation for Chief of Staff
conversation turns. The local database owns the transcript. An idempotent
request starts an isolated Codex turn with recent completed messages and
the installation's existing ChatGPT sign-in. One active turn can hold a
time-limited lease. The Codex process sees an empty workspace, with shell
and web search disabled. A file-only message can receive a clarification,
and the next turn reuses those files when it has no attachments of its own.

Replaying bounded local history keeps recovery independent of Codex
container lifetime and leaves coding workspaces and provider sessions
isolated. Existing coding goals remain available under Project tools.
When a conversation turn proposes a coding goal, the operator chooses a
registered project before dispatch. The local service pins the project ID,
checkout binding, configured check and saved decision, then runs the queued
job with the existing lease, sandbox and patch review. The installed service
reads its project catalog from its private state directory. Coding and
conversation turns share the installation's Codex sign-in and serialize access
to that credential directory. Independent review and Slack conversation
replies still use their separate implementation steps.


### Discoverable Slack actions

Plain-language conversation is the entry point. Register zero slash
commands and accept natural-language goals without a prefix. Use Block Kit
only where a visible choice helps the operator act or see a result.
Each action has an equivalent in the web app and through natural-language
conversation, with the same authorization and task state.

| Moment | Slack interaction |
| --- | --- |
| The project is ambiguous | Ask one question and offer a chooser with eligible projects. |
| Work is underway | Show the task and current state in a compact status card. Keep detailed progress in its thread. |
| A decision is needed | Explain the decision and offer contextual buttons such as **Continue** or **Cancel task**. Use a modal when the operator needs to enter several fields. |
| Work is ready or blocked | Show check and review status, with **Open pull request** or a recovery action such as **Retry check** when available. |

Provide accessible text for interactive messages. Verify Slack interaction
signatures and respond within Slack's three-second window. Forward typed
actions to the local service, where the operator, binding, task, and
expected state are checked before execution. Resolve repeated clicks once
and show current state when an old card is used. Block Kit controls provide
input only and inherit the same action policy as the web app.
[Slack actions][slack-actions] · [Slack interaction callbacks][slack-interactions]

### Delivery and acknowledgment

Acknowledge “queued” when the relay has saved a Slack goal pending local
delivery, and “accepted” after the local service commits the task. Keep
updates tied to meaningful transitions. For official Slack, send outbound
messages only to the currently confirmed DM and recheck the binding before
delivery. A disconnected user can continue in the web app. Keep private app
credentials and direct Slack calls in the local connector.

For official replies, extend the relay's credential lifecycle deliberately.
Retain only the sealed, scoped Slack credential needed for posting, with
revocation and retention coverage. Use `chat.postMessage` in the confirmed
DM. The local service records outgoing conversation events durably. A
delivery key prevents duplicate sends from local or relay retries. Store the
provider message ID once known, honor rate limits, and treat loss of the
provider response as uncertain. Reconcile the original send before
retrying after uncertainty, or surface a delivery problem. Successful local
work stays visible in the web app. [Slack messaging API][slack-api]

### Verified pull request by default where enabled

For a repository with PR delivery enabled, the project policy authorizes
publication to a dedicated Difqo branch and opening a draft for the agreed
goal. Pin the authorized repository, target branch, base commit, saved
project decision, and execution attempt. Verify the candidate commit and
store its exact revision, check command, and outcome before publication.
Use a narrowly scoped installation token from the local GitHub integration
for the selected repository. Keep that token and all publication operations
outside the authoring and review containers. GitHub intake and publication
permissions can be granted separately. [GitHub PR creation][github-pr]

| Stage | Required evidence or recovery |
| --- | --- |
| Resolve and verify | Match the operator's request to one authorized project. Record a check against the exact candidate commit. On failure, surface the reason and keep the local result. |
| Publish a draft | Persist a publication record before writing to GitHub. Use a task-owned branch and reconcile by repository, head and base after a restart or lost response. Adopt an existing PR only when it matches that record. Recheck authorization before writes. |
| Review the draft | Chief of Staff assigns its code review specialist to the published head with the project decision, diff, and verification evidence. The specialist runs an independent attempt and reports actionable findings and the outcome to the same task. Publish findings to the PR where authorized. |
| Mark ready | Wait for a passing check and a completed review without blockers for the current head. A new head requires fresh evidence. Recheck the head around the ready transition and reconcile races with GitHub. GitHub branch protection governs merging. |
| Report the result | Give the operator the same PR URL and check and review state in either connected surface. A blocked review or unavailable publisher gives the operator a specific next action. |

The same Chief of Staff that took the goal in the web app or Slack
manages the code review specialist. It delegates review, coordinates a fix
or escalates a blocker, and requests a fresh check and review after changes.
The operator continues in the same conversation. The specialist's review
is independent of the authoring attempt and respects repository-required
approvals. An existing PR can also be reviewed on request through the same
path. Merging remains a distinct action.

Local-only projects and configured exceptions use the existing
[local review and approval path][local-review]. Missing or revoked GitHub
authorization leaves the result available locally and surfaces the blocked
publication. Use “changes”, “checks”, “code review”, and “pull request” in
product copy. The underlying artifact format and existing API remain
compatible.

## Alternatives and consequences

- A Slack-only workflow would make the optional integration a requirement
  for core work and strand conversations during a disconnect.
- Maintaining separate task or approval state per surface would make it
  difficult to continue a request across surfaces and safely handle replay.
- Asking a person to manually apply each result would retain the current
  local process and add routine work for repository projects. The draft PR
  path makes agent review and repo checks part of delivery.
- Publishing straight to a ready PR would shorten the happy path while
  exposing changes before agent review finishes. Draft first provides a
  visible place to coordinate blocked reviews and subsequent fixes.

## Implementation and acceptance

1. Extend the existing Slack connection with shared task status and a reply
   acknowledging a durably accepted goal. Exercise the same task from the
   web app and Slack. Cover duplicate delivery, a lost provider response,
   disconnect, and rejection of unlinked identities. Track under [#11][slack].
2. Accept plain text, text with multiple files, and file-only messages.
   Verify replay, invalid file URLs, missing access, and size limits with
   synthetic files. Add contextual Block Kit actions on the shared local
   task state. Verify a plain-text response and a button can resolve the
   same decision, plus repeated clicks and stale cards. Track under [#11][slack].
3. Add project-bound draft publication using a fixture GitHub endpoint and
   an isolated Git remote. Force a restart after each external write and
   verify one branch and one draft PR. Prove zero writes for revoked
   credentials and mismatched repositories. Track under [#53][delivery],
   with installation and scoping under [#12][github-intake].
4. Have Chief of Staff assign the code review specialist and enforce the
   current-head check and review gate. Verify one clean result and one
   blocker that Chief of Staff routes for a fix and then reassigns for
   review at the new head. Verify both surfaces report the same task,
   states, and links. Track under [#21][review].
5. Run a live acceptance exercise with an explicitly enrolled test
   repository and linked Slack DM. Start in one surface and answer a
   clarification in the other. Verify the saved decision affected the
   result, then inspect the head, check, findings, and one matching PR in
   both surfaces. Run the equivalent goal locally with Slack disconnected.

[slack]: https://github.com/nep-ai/difqo/issues/11
[github-intake]: https://github.com/nep-ai/difqo/issues/12
[review]: https://github.com/nep-ai/difqo/issues/21
[delivery]: https://github.com/nep-ai/difqo/issues/53
[local-review]: 0005-exact-patch-review.md
[slack-api]: https://docs.slack.dev/reference/methods/chat.postMessage/
[slack-files]: https://docs.slack.dev/messaging/working-with-files/
[slack-actions]: https://docs.slack.dev/reference/block-kit/blocks/actions-block
[slack-interactions]: https://docs.slack.dev/interactivity/handling-user-interaction/
[github-pr]: https://docs.github.com/en/rest/pulls/pulls#create-a-pull-request
