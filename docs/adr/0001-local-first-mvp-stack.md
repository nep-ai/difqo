# ADR 0001 · Local-first MVP stack

**Status** — Accepted

## Context

Difqo needs to run bounded coding tasks on a local machine, preserve work across
process and container failure, and show a human the patch and check result. Web,
Slack, and GitHub are MVP entry points for the same workflow.

## Decision

- Ship a single Go module and binary with server and worker process modes.
  Workers claim tasks and report evidence through the server API.
- Use PostgreSQL for state and queueing. Run it on the host for the Mac mini
  proof of concept so the control plane can keep serving status during a Docker
  interruption. Only the server connects to PostgreSQL.
- Execute agent attempts in fresh Docker containers with isolated host-backed
  workspaces and artifacts. Keep Docker lifecycle handling separate from the
  Codex App Server JSON-RPC adapter.
- Use a single React and Vite package in a pnpm workspace for the local web app.
  Use Slack Socket Mode and GitHub App polling for the external channels. All
  three use the same intake, policy, and task history.

## Alternatives and tradeoffs

SQLite would simplify installation. PostgreSQL provides transactional task
claims and a shared state store behind the server API, with an additional
service to install and operate. A separate queue would introduce another
service and require recovery across two systems. Running PostgreSQL in the
same Docker runtime would simplify a Compose setup and couple status
availability to the execution runtime.

## Consequences

The initial implementation proves task intake, exclusive claims, and result
persistence over loopback HTTP with a deterministic Git check. It has one
attempt per task. Lease expiry and retry, Codex authentication, evidence
capture, and reboot behavior need implementation and verification before the
Mac mini proof of concept can pass. The
[architecture document](../architecture.md) shows the implemented flow and
MVP target.
