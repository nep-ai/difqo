---
name: difqo-system-design
description: Design Difqo systems and write technical specs or implementation plans for architecture, interfaces, data models, and cross-component changes. Use when deciding how a feature should work or sequencing engineering work.
---

# System design, specs, and plans

- Start with the requested behavior, constraints, and relevant repository decisions. Identify the existing component boundaries and sources of truth. When a choice is consequential, compare credible alternatives using failure modes, compatibility, operational cost, and reversibility.
- Include a simpler design that extends an existing boundary when it is viable. Give each proposed service, dependency, or abstraction a concrete requirement that justifies its cost.
- Specify interfaces, state ownership, data flow, and failure behavior at the boundaries the change touches. For local-first workflows, make local persistence and any external data transfer explicit. Treat repository content and agent output as untrusted inputs. Define where execution scope and permissions are enforced and which actions require a human decision.
- For agent execution and review evidence, keep the requested scope, decision revision, patch revision, and check result associated where those records exist. Define how the system treats stale decisions, changed patches, interrupted runs, and retries. A result for an earlier patch cannot establish that the current patch passed.
- Where the change affects stored data, processes, or providers, specify compatibility, migration and recovery, and how maintainers can observe failures without exposing sensitive data. Record durable decisions and rationale alongside the affected code as described in the maintainer guide.
- Turn the spec into dependency-ordered implementation slices. For each slice, give its outcome, affected boundaries, dependencies, and a runnable acceptance check where feasible. Put risk-reducing experiments early and identify any decision that must be settled before implementation.
