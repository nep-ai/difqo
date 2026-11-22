---
name: difqo-implement
description: Implement Difqo features, fixes, and refactors or diagnose an application bug. Use when changing code or narrowing down failing behavior through the existing entry points.
---

# Implementation and debugging

- Locate the normal entry point and the contract callers rely on. Use the accepted scope and settled decisions when available. Implement a focused slice through the existing boundaries and cover affected older data, error handling, and retries where applicable.
- Check the current implementation and its callers before changing an interface. Prefer a change to existing code, then an existing platform feature or dependency, then new code. Add a dependency only when those options cannot meet the contract cleanly.
- For a bug, build a tight feedback loop that demonstrates the reported behavior through a relevant interface. Use a sanitized fixture, log, or minimal check to isolate the cause. Add a regression test when the fix addresses behavior that existing checks would miss.
- Run the smallest relevant checks while iterating, then run broader repository checks when the change crosses components. When changing how a patch is produced or reviewed, verify the path from the source decision to the observed patch and check result.
- Inspect the final diff for unintended changes. Provide the checks run, their results, and any remaining verification gaps so a reviewer can repeat the important checks.
- After opening a focused PR, follow the [delivery skill](../difqo-delivery/SKILL.md#pull-request-monitoring) through merge before starting the next issue in the queue.
