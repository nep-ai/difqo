# ADR 0004 · Serve the Chief of Staff web app locally

## Context

A user needs one place to submit a goal and see what Difqo actually ran.
The Go server owns task state in PostgreSQL. The worker already records
results and writes optional artifacts to a configured host directory.

## Decision

Serve the built React app from the Go server on the same loopback origin
as the task API. Allow the existing API-only mode to run without web assets.
Share the task status query between single-task reads and the recent
conversation, capped at the 100 newest tasks. The browser polls the API and
keeps a draft in memory while submission is pending or fails.

The server optionally opens the worker's artifact directory and serves its
fixed evidence files as plain text. It validates the file reference
against the completed attempt ID and confines file access to the configured
root. Go's cross-origin protection rejects cross-site browser mutations.
The listener and host checks continue to restrict access to loopback.

## Alternatives and consequences

A production Vite server would introduce a second service and a separate
origin. Embedding the compiled bundle into every Go binary would require
Node and web assets to build the existing CLI and worker. Serving a separate
build directory keeps those builds independent. Development Vite proxies
requests to the loopback Go service.

The task record remains authoritative. The app shows the worker's saved
output and evidence. Full agent coordination, patch acceptance, and rerunning
a reviewed patch need their own control-plane contracts before the browser
can expose those actions.
