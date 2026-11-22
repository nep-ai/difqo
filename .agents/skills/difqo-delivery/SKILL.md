---
name: difqo-delivery
description: Monitor Difqo pull requests through merge or work on CI, builds, packaging, deployments, releases, and operational recovery.
---

# Delivery and operations

- Use repository manifests, workflows, and [MAINTAINING.md](../../../MAINTAINING.md) to identify the supported environment, build commands, delivery target, and release criteria. Determine the intended revision and artifact before publishing or deploying.
- Run the documented checks from a clean checkout with pinned tool and dependency versions where the stack supports them. Keep fork PR jobs read-only and free of secrets. Keep publish or deploy credentials scoped to the trusted job and target. Promote stable checks to required status once contributor runs are proven.
- For an artifact or deployment, verify version and revision, document compatibility and any migration, and identify how to recover if validation fails. Check rollback against persisted data changes. For early access, collect the clean-checkout workflow evidence and follow the maintainer guide's public release steps.
- After an authorized deployment or publication, verify the artifact or deployed revision through its normal user entry point. Report the target, revision, smoke result, and any follow-up. For failures, use sanitized logs and the last known good revision to choose a recovery action, then verify recovery through the same entry point.

## Pull request monitoring

- After opening a PR, monitor its merge state, checks, and review activity. Use the client’s merge watcher when required, such as T3 Code’s watch through merge. Where polling is supported, check every five minutes while the PR is open.
- Address actionable findings in the same PR and rerun the relevant checks. Follow the merge policy in [MAINTAINING.md](../../../MAINTAINING.md).
- Confirm that GitHub reports the PR as merged before starting the next issue. Keep waiting while checks or review are pending or the PR is ready and awaiting merge. If the PR closes without merging, stop and report the outcome.
