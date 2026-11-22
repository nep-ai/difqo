# Working on Difqo

Difqo is a local-first project for coordinating coding agents. The first milestone is one local repository where a saved project decision informs a patch and a human can review the change and rerun its check before accepting it. See [README.md](README.md) for current product scope.

Use [CONTRIBUTING.md](CONTRIBUTING.md) for contribution rules, [MAINTAINING.md](MAINTAINING.md) for review and release policy, and [SECURITY.md](SECURITY.md) for private reporting. Use synthetic or redacted examples in evidence that leaves the workspace.

## Lifecycle skills

Use the skills that match the requested work. A change can enter at any stage. For substantial features, keep the problem, design decisions, and acceptance evidence linked through the issue and PR. Small changes can carry that context inline.

| Stage | Skill | Output |
| --- | --- | --- |
| Brainstorming, discovery, PRD | [difqo-product-discovery](.agents/skills/difqo-product-discovery/SKILL.md) | Product direction or PRD with a testable outcome |
| UI/UX design | [difqo-ui-ux](.agents/skills/difqo-ui-ux/SKILL.md) | User flow, interaction states, and design acceptance criteria |
| Visual feedback | [difqo-agentation](.agents/skills/difqo-agentation/SKILL.md) | Scoped browser annotations, fixes, and verification |
| System design, technical spec, implementation plan | [difqo-system-design](.agents/skills/difqo-system-design/SKILL.md) | Architecture, behavioral contracts, and ordered implementation slices |
| Implementation and debugging | [difqo-implement](.agents/skills/difqo-implement/SKILL.md) | Working change with focused checks |
| Code and PR review | [difqo-review](.agents/skills/difqo-review/SKILL.md) | Findings tied to the diff, requirements, and evidence |
| Testing and acceptance | [difqo-verify](.agents/skills/difqo-verify/SKILL.md) | Reproducible check results and coverage gaps |
| CI, packaging, deployment, release, and operations | [difqo-delivery](.agents/skills/difqo-delivery/SKILL.md) | Reproducible delivery or operational recovery with verification |

Use the build and check commands documented by the implementation. When introducing a new component, document its setup and checks alongside it. Add a skill when a recurring workflow needs guidance that the existing skills cannot cover clearly.

## Working efficiently

- Choose the smallest complete change. Reuse existing behavior and established interfaces before adding abstractions, options, or dependencies. Preserve correctness, security, accessibility, and the checks the change requires.
- Lead updates and handoffs with the outcome. Include the changed paths, checks and results, and any blockers or uncertainty that affect the decision. Keep commands, paths, and evidence exact. Expand when the user asks or the tradeoff needs explanation.
- Use Serena for symbol definitions and references in supported languages when it is connected. Confirm the active project is this checkout before using it. Use `rg` and targeted reads for documentation, config, unsupported languages, or when Serena is unavailable.
- If RTK is available, use explicit wrappers such as `rtk git status` for routine summaries. Use the original command when you need a complete diff, exact logs, or machine-readable output. Use the original command when RTK is unavailable.

## Agent client setup

Open the repository root in your client. All three clients share this file and the skills under `.agents/skills/`.

| Client | Project setup |
| --- | --- |
| Codex | Trust the checkout and reopen the session to load [the MCP config](.codex/config.toml). |
| Claude Code | [CLAUDE.md](CLAUDE.md) imports these instructions. The entries in `.claude/skills/` link to the shared skills and require symlink support in the checkout. Review the project servers from [.mcp.json](.mcp.json) when prompted. |
| Cursor | Cursor discovers the shared skills directly and reads [the project MCP config](.cursor/mcp.json). Review the project servers in Cursor's MCP settings. |

Keep `uvx` and `npx` on the client's PATH. If Serena starts outside Difqo, activate this checkout by its absolute path before querying symbols. Add language servers to [.serena/project.yml](.serena/project.yml) as application languages land.
