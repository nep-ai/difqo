# Difqo architecture

## Implemented local task flow

```mermaid
flowchart LR
    Operator["Local operator"] -->|"Submit goal or read result over loopback HTTP"| Server["difqo server"]
    Worker["difqo worker"] -->|"Claim one task and report its result over loopback HTTP"| Server
    Server -->|"Persist tasks, claims, and results"| Postgres[("PostgreSQL on host")]
    Worker -->|"Run git rev-parse --verify HEAD"| Repo["Operator-selected Git checkout"]
```

The local operator starts the server and starts a worker with a checkout path. The
worker claims one queued task and runs a fixed Git revision check. The task goal
is stored as text. Task status is derived from the attempt record. An absent
attempt means queued, an incomplete attempt means claimed, and a completed
attempt carries the result. Reposting the same result is safe. If a worker
stops after claiming, the recorded attempt stays incomplete.

The server uses an unauthenticated loopback API for the groundwork. Run it
in a trusted local development environment. PostgreSQL stores the revision check
output directly. The development workflow and API examples are in the [local
setup guide](development.md).

## MVP target

```mermaid
flowchart LR
    Browser["React + Vite web app"] --> Intake
    Slack["Slack Socket Mode"] <-->|"Private owner DM"| Intake
    GitHub["GitHub App"] <-->|"Poll comments and reply"| Intake

    subgraph Server["Go server process"]
        Intake["Shared task intake"] --> CoS["Chief of Staff"]
        CoS --> Policy["Policy, approvals, and tool gateway"]
        Policy --> TaskAPI["Task API and scheduler"]
    end

    Intake --> DB[("Host PostgreSQL")]
    TaskAPI --> DB
    Worker["Go worker process"] <-->|"Claim, renew, and report"| TaskAPI
    Worker --> Docker["Docker execution adapter"]
    Worker --> Codex["Codex App Server adapter"]
    Docker --> Container["Fresh container per attempt"]
    Codex -->|"JSON-RPC over stdio"| Container
    Container -->|"Read-only pinned skills and isolated writable workspace"| Host["Host workspaces, skills, and artifacts"]
    Worker -->|"Capture diff and check output"| Host
```

The control plane owns configuration, policy, approvals, and durable task
history. The worker manages execution and reports evidence through the task API.
Attempt leases, isolated workspaces, and host-backed evidence are the planned
recovery boundary. The Mac mini proof of concept will verify container
authentication, recovery after interruption, and host restart behavior after
the execution and recovery code is implemented.

See [ADR 0001](adr/0001-local-first-mvp-stack.md) for the tradeoffs behind this
layout.
