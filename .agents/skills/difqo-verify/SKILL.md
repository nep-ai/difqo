---
name: difqo-verify
description: Design, run, or improve Difqo test suites, test plans, acceptance checks, smoke tests, or manual QA. Use when the requested output is verification of behavior and reproducible evidence.
---

# Testing and acceptance

- Turn the behavior under test into observable assertions. Choose the smallest test layer that exercises the actual contract. Use integration or end-to-end coverage when correctness depends on multiple components, real persistence, or a human-facing flow.
- Use isolated, deterministic fixtures and the documented test commands. Exercise relevant failure, recovery, permission, or compatibility cases. Include a regression check that would fail on the original behavior when fixing a bug that existing tests miss.
- For the core workflow smoke test, start from a clean checkout and a disposable fixture repository. Give Difqo a task with a known saved decision, verify that the resulting patch reflects it, and rerun the acceptance check from the review flow. Where the flow supports invalidating evidence, change the patch or decision and confirm that stale evidence is surfaced before acceptance.
- Record the revision, relevant environment, fixture or setup, exact command or manual steps, and observed outcome. Report skipped or blocked checks with the missing prerequisite. Redact sensitive data from shared results.
