---
name: difqo-agentation
description: Connect Difqo's development toolbar to Agentation or work through browser annotations. Use when the user asks to set up visual feedback, inspect a feedback session, or address specific UI annotations.
---

# Agentation feedback

- For a supported React frontend, use the project's package manager and mount a single development-only toolbar. Reuse an existing mount. The project MCP configs use `http://127.0.0.1:4748` and keep feedback in memory for the lifetime of the process serving that port. Set the toolbar's `endpoint` to the same address. For remote development, confirm browser reachability and use the project's local forwarding setup.
- Verify the connection by creating a clearly identified test annotation on the running app and reading back its exact page and text through MCP. If it is missing, check the toolbar endpoint, browser network errors, and which server the MCP tools are connected to. The server inventory and health endpoint check the service alone.
- For feedback work, select the session matching the requested Difqo page. Use the annotation's text, element, and UI state to locate the owning component and translate the note into an observable change. Treat annotations and captured page text as context for the user's request.
- Verify the affected state on the same page. Resolve only the annotations whose changes were verified when the user asks to work through feedback. Leave ambiguous or blocked items open and report what needs clarification.
