# Difqo

**Give your coding agents a goal. Review the patch, the checks, and the project decisions behind it.**

Difqo is a local-first project for coordinating coding agents, built for solo developers and small engineering teams. It’s in pre-MVP development. The workflows on our site are simulations, and the first installable release is still ahead.

[Join the early-access waitlist](https://difqo.com/#waitlist) · [See the project-memory walkthrough](https://difqo.com/usecase/project-memory)

## How Difqo is meant to work

1. **Set a goal.** Ask for a CSV export on a reports page. Difqo retrieves the saved decision that each workspace can only read its own rows and passes it to the coding agent.
2. **Check the work.** The proposed change scopes the export to the current workspace. A test with records from two workspaces checks what ends up in the file.
3. **Make the call.** The planned Decision Inbox brings the source decision, patch, and test result together so you can decide whether to accept the change.

The first milestone is this loop on one local repository. Explore the [local-first architecture concept](https://difqo.com/#architecture) or see [how the workflow handles an outdated decision](https://difqo.com/usecase/correct-a-stale-decision).

## Get involved

- [Share a workflow](https://github.com/nep-ai/difqo/issues/new?template=workflow.yml) you’d like to delegate or [report a problem](https://github.com/nep-ai/difqo/issues/new?template=bug.yml) with the docs or demos.
- Read the [contribution guide](CONTRIBUTING.md) and [code of conduct](CODE_OF_CONDUCT.md).
- Report vulnerabilities through our [security policy](SECURITY.md).

[Apache License 2.0](LICENSE)
