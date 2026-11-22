---
name: difqo-review
description: Review a Difqo PR, branch, commit range, or working-tree change against the requested behavior and repository conventions. Use for findings and merge-readiness assessments.
---

# Change review

- Identify the diff and revision under review. Use the PR base, requested comparison, or current working tree as appropriate. For working-tree reviews include staged, unstaged, and relevant untracked files. Load the originating issue or spec if available and check the surrounding callers and tests.
- Use complete diffs and original command output for evidence that supports a finding. Use Serena reference lookup when available to check affected callers of a changed symbol.
- Verify behavior and acceptance criteria first. Prioritize actionable issues introduced by the change, including data loss or disclosure, compatibility failures, scope or authorization regressions, and missing checks where a failure would matter. For agent evidence, check that the source decision and result still apply to the patch being reviewed.
- Assess whether the evidence exercises the normal entry point and distinguishes the intended behavior from the likely failure. Report any checks you ran separately from evidence supplied with the change.
- Report findings in order of impact with file and line, the triggering condition, and the user-visible consequence. State remaining verification gaps even when there are no findings. Follow the repository PR template when preparing review evidence for a PR.
