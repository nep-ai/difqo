---
name: difqo-ui-ux
description: Design or critique Difqo user flows, wireframes, prototypes, interaction states, and accessibility. Use when deciding how people navigate, inspect work, or act in the interface.
---

# UI and UX design

- Start with the user goal and map the flow from entry point to outcome. Use the existing product vocabulary and design system where available. Match the fidelity of the design to the decision the user needs to make.
- For a new visual direction, define shared choices for hierarchy, semantic color, typography, spacing, density, and motion before detailing screens. Reuse the product's existing tokens and components when available. Document any screen-specific exception with the user need that justifies it. Use [UI UX Pro Max](https://github.com/nextlevelbuilder/ui-ux-pro-max-skill) as a reference when exploring options or checking a specific design concern.
- For review surfaces, make the source project decision, affected patch, and check result easy to inspect together. Give the user a clear way to rerun the check and see whether it still applies to the current patch. Make explicit which action accepts the work.
- Design relevant loading, empty, failed, stale, and cancelled states, including recovery. Show an actionable reason when an action is blocked. For long-running work, explain what is happening and what control the user has.
- Specify keyboard and focus behavior, accessible control names, status announcements, and layouts for the supported viewport sizes. Convey status with text as well as color. Use realistic, sanitized examples when they help evaluate the flow.
- When checking a design or implementation, inspect readable contrast across supported themes, visible focus, keyboard order, narrow layouts and long content, and reduced-motion behavior where motion is used.
- Hand off the flow, states, interaction and accessibility criteria, and any open choices. Inspect the prototype or implementation on the affected surfaces when a running version is available and the task calls for it.
