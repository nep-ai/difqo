# Difqo brand guidelines

Difqo is a local-first workspace for coordinating coding agents. Its Chief of Staff is the point of contact for giving goals, checking progress, and reviewing decisions across software projects.

Use this guide when writing about Difqo or designing its website, documentation, and web app. Difqo is in pre-MVP development. The first milestone covers one local repository, from a saved project decision through patch review and verification. Use **Workflow demo** for simulations and **Planned** for roadmap capabilities such as coordination across projects and the full production feedback loop.

## Positioning

**Headline:** Give a goal. Know what ships and why.

**Supporting copy:** Work with one Chief of Staff across your software projects. Difqo coordinates the agents and tools behind the work and brings you the evidence you need to decide what ships and what to improve.

Use the headline and supporting copy for the product vision. Pair them with the current availability and an action the reader can take. On the early-access site, that action is **Join the waitlist**.

Start with what the developer wants to do. Introduce **Chief of Staff** as the coordinating role. Use **agent factory** to describe the projects, repositories, threads, specialist agents, skills, and MCP tools it coordinates behind the scenes. Bring those details into view when someone needs to inspect the work or change its setup.

The project file is the visual signature. It follows one goal and keeps the decisions, changes, and results together. In product screenshots, start with a Chief of Staff update and show the evidence a developer can open from it.

### The full software development lifecycle

| Stage | Show what happened |
| --- | --- |
| Ideation | Define the goal and acceptance criteria. Link any saved project decision that applies. |
| Implementation | Show the proposed patch and summarize what changed. |
| Review | Compare the patch with the goal and the source decision. Record reviewer feedback. |
| Test | Show what ran, the result, and the patch revision it applies to. |
| Deployment | Record which version shipped and to which environment. |
| Monitoring | Link a production finding to the release and its supporting telemetry. |
| Improvement | Propose a follow-up goal that carries the finding and relevant decisions back into planning. |

For example, a CSV export starts with a requirement to include only the active workspace’s rows. The developer reviews the patch and its two-workspace isolation check. The team then deploys the approved change. If monitoring shows that large reports time out, the next goal links to that finding and retains the original workspace constraint. Use synthetic or redacted data in public examples.

## Voice and terminology

Write like an engineer handing off work. State the outcome, tie it to evidence, and make the next action clear. Use sentence case in headings, buttons, and status labels. Capitalize product names such as **Difqo**, **Chief of Staff**, and **Decision Inbox**.

- **Progress** — “The CSV export is ready for review. The workspace isolation check passed on this patch.”
- **Blocked** — “GitHub access is required to open a pull request. Connect GitHub to continue.”
- **Evidence has expired** — “The patch changed after this check ran. Rerun the check before approving the patch.”
- **Production finding** — “Large report exports timed out after the latest deployment. Review the finding before setting a follow-up goal.”

Use **goal** for the outcome someone asks for, **run** for an execution attempt, **patch** for a proposed set of code changes, and **check** for a verification command and its result. Use **pull request** when a pull request exists. Give authorization and code review distinct labels. Describe the actual setup when making claims about local execution, permissions, or integrations.

### Action labels

| Context | Label | Use |
| --- | --- | --- |
| Early access | **Join the waitlist** | The visitor can submit an early-access request. |
| Public example | **View demo** | Opens an example workflow. Identify simulated work in the demo itself. |
| Source or documentation | **View on GitHub**, **Read the docs** | Name the destination. |
| Supported onboarding | **Get started** | Opens a usable setup flow or quickstart. |
| Conversation | **Send** | Sends the composed message. |
| Execution | **Start run**, **Pause run**, **Resume run**, **Cancel run** | Name the action and distinguish the run from the underlying goal. |
| Review | **Review changes**, **Approve patch**, **Request changes** | Use **Review changes** to open inspection, **Approve patch** to record the decision on a local patch, and **Request changes** to send feedback. Use **Approve pull request** for an actual pull request. |
| Verification | **Rerun check**, **View logs** | Distinguish executing a check from inspecting its output. |
| Authorization | **Connect GitHub**, **Approve access**, **Decline** | State the service and scope alongside the controls. |
| Deployment | **Deploy to staging**, **Deploy to production** | Name the environment and show the version before the action. |

Use these labels with the verbs and objects users already encounter in developer tools. GitHub’s review and workflow terminology informs the review and rerun labels. The GitHub CLI site illustrates setup guidance that leads to a concrete install path. [GitHub code review](https://docs.github.com/en/pull-requests/how-tos/review-pull-requests/reviewing-proposed-changes-in-a-pull-request) · [GitHub workflow reruns](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/re-run-workflows-and-jobs) · [GitHub CLI](https://cli.github.com/)

## Visual identity

Write **Difqo** in text. Use the lowercase **difqo.** wordmark with the period attached in branded placements. On paper, use ink letters and a cobalt period. On cobalt, use a white wordmark. Use the existing favicon for a compact square mark. [Live landing page](https://difqo.com/) · [Favicon](https://difqo.com/favicon.svg) · [Social preview](https://difqo.com/share.png)

Use **Barlow Condensed Semibold** for campaign headlines and large editorial titles. Use **Geist** for body copy, interface text, and the bold wordmark. Reserve system monospace for literal commands, paths, diffs, and logs.

| Role | Color | Use |
| --- | --- | --- |
| Cobalt | `#3049BD` | Primary actions, links on light surfaces, evidence accent, large marketing fields |
| Paper | `#FAF8F3` | Reading surfaces and workspace shell |
| Ink | `#171B2A` | Text on light surfaces |
| White | `#FFFFFF` | App content and text on cobalt |
| Deep navy | `#182039` | Code and log surfaces with `#F3F6FF` text |
| Muted ink | `#53596B` | Secondary text and necessary control boundaries on paper |
| Tint | `#E1E6F5` | Secondary grouping and informational states |
| Line | `#D4D7E3` | Decorative dividers |

Use explicit status text alongside color. For light surfaces, use `#236046` on `#E6F2E9` for success, `#795000` on `#FFF1D6` for warnings, and `#962E35` on `#FCE9EB` for failures. Use a visible cobalt focus ring on paper and `#FFDF87` on cobalt. Target at least 4.5:1 for normal text and 3:1 for meaningful control and state indicators. [WCAG text contrast](https://www.w3.org/WAI/WCAG22/Understanding/contrast-minimum.html) · [WCAG non-text contrast](https://www.w3.org/WAI/WCAG22/Understanding/non-text-contrast.html)

## Web app components

Keep the conversation prominent and navigation quiet. Use cobalt for the primary action and green for verified success. Use 15–16 px text for messages and controls, 13–14 px for supporting text, and a 4 px spacing unit. A conversation column around 720–800 px wide and stacked content on narrow screens are starting points.

| Component | Design and behavior |
| --- | --- |
| Workspace header | Show Difqo, the Chief of Staff workspace, and connection state when it matters. Keep setup and administrative actions accessible without crowding the conversation. |
| Setup and empty state | Show readiness checks and a recovery action for each blocker. In an empty conversation, offer a sample goal and a way to ask for an update. |
| Conversation turn | Label the speaker. Keep the latest work card with the relevant update and let older replies link to it. Preserve reading position while the user inspects earlier messages. |
| Composer | Provide a labeled multiline input and a visible **Send** button. Keep an unsent draft if submission fails. |
| Goal and run card | Show the project, goal, run state, and available next action. Explain permissions before starting a run and show recovery options if it stops. |
| Decision Inbox and evidence card | Keep the saved decision, base revision, patch, and check result together. Explain when a check is stale and offer **Rerun check**. Enable approval when the required evidence applies to the current patch. |
| Diff and log viewer | Use monospace and preserve diff markers alongside color. Let users expand and scroll within the output without losing the surrounding review context. |
| Authorization card | Show the service, requested access, and what approval enables. Keep approval and decline controls together. |
| Buttons and status labels | Use one filled primary action per card. Use text labels such as **Running**, **Ready for review**, **Check out of date**, and **Cancelled**. |
| Release and monitoring cards | In the planned lifecycle, use the same evidence pattern for the deployed version, environment, production finding, and linked follow-up goal. |

Make actions keyboard accessible, preserve visible focus, and announce meaningful state changes without interrupting the current task. Give controls accessible names, keep touch targets comfortable, and honor reduced-motion preferences. [Primer timeline accessibility](https://primer.style/product/components/timeline/accessibility/) · [AI Elements chat patterns](https://ai-sdk.dev/elements/components/chatbot)
