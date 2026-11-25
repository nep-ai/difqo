# Maintaining Difqo

GitHub is the record for public decisions, accepted work, and releases. Use [difqo.com](https://difqo.com/) for illustrated workflows and early access. Keep the README and website’s availability claims in sync.

## Triage and review

- Review the issue and PR queues weekly while the MVP is being built. Confirm scope and impact, link duplicates to the original, and ask for the smallest useful clarification when context is missing.
- Use `bug`, `documentation`, and `enhancement` to identify the work. Use `accepted` once a maintainer agrees the work fits the current milestone. Use `good first issue` for scoped work with a clear acceptance check and enough context for a newcomer.
- Describe the outcome and acceptance criteria on each accepted issue. Create an MVP milestone when the initial implementation issues exist. Record significant design decisions and their rationale alongside the code when they’re settled.
- For every PR, check the diff against [the architecture document](docs/architecture.md). If it changes architecture, require both an [ADR](docs/adr/) that records the decision and an update to the architecture document and affected Mermaid diagrams in the same PR before merging.
- Route changes to `main` through focused PRs and squash-merge after reviewing the final diff, verification evidence, and any outstanding review threads. The maintainer who merges the change owns that decision.
- Triage sensitive vulnerability and conduct reports through the private channels described in the relevant policies.

## From first code to early access

1. Land the MVP setup instructions and stack-specific checks with the implementation. Run the documented build, lint, and tests on PRs. Use a read-only token and no secrets for fork PRs. Require the stable checks on `main` after confirming they run successfully for contributors.
2. Prove the first workflow from a clean checkout. Give it a task, apply a saved project decision, verify the resulting change, and review the evidence before accepting it. Record the supported environment and current limitations based on the run.
3. Before opening access, publish a tested quickstart and a short demo captured from the running build. Publish release notes with a GitHub prerelease once there is a supported artifact, and update the README, website, and waitlist copy together.

For Apple silicon staging, production promotion, and rollback, follow the
[macOS release runbook](docs/macos-release.md).
