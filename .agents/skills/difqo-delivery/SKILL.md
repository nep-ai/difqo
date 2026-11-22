---
name: difqo-delivery
description: Set up or troubleshoot Difqo CI, builds, packaging, deployments, releases, or operational recovery. Use when qualifying an artifact, changing the delivery pipeline, or diagnosing a deployed system.
---

# Delivery and operations

- Use repository manifests, workflows, and [MAINTAINING.md](../../../MAINTAINING.md) to identify the supported environment, build commands, delivery target, and release criteria. Determine the intended revision and artifact before publishing or deploying.
- Run the documented checks from a clean checkout with pinned tool and dependency versions where the stack supports them. Keep fork PR jobs read-only and free of secrets. Keep publish or deploy credentials scoped to the trusted job and target. Promote stable checks to required status once contributor runs are proven.
- For an artifact or deployment, verify version and revision, document compatibility and any migration, and identify how to recover if validation fails. Check rollback against persisted data changes. For early access, collect the clean-checkout workflow evidence and follow the maintainer guide's public release steps.
- After an authorized deployment or publication, verify the artifact or deployed revision through its normal user entry point. Report the target, revision, smoke result, and any follow-up. For failures, use sanitized logs and the last known good revision to choose a recovery action, then verify recovery through the same entry point.
