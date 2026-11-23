# Difqo architecture

## Implemented local task flow

```mermaid
flowchart LR
    Operator["Local operator"] --> Browser["Chief of Staff web app"]
    Browser -->|"Submit goals and read real results over loopback HTTP"| Server["difqo server"]
    Operator -->|"Run the worker when ready"| Worker
    Worker["difqo worker"] -->|"Claim a leased attempt and report its result over loopback HTTP"| Server
    Server -->|"Lock claims and persist attempt deadlines and results"| Postgres[("PostgreSQL on host")]
    Worker -->|"Read HEAD or make a shallow clone"| Repo["Operator-selected Git checkout"]
    Worker -->|"Validate engine, local image, and sandbox before claim"| Runtime["Docker, Podman, or compatible container CLI"]
    Runtime -->|"Run one App Server turn over stdio"| Codex["Disposable execution workspace"]
    Worker -->|"Capture diff and check log"| Artifacts["Host artifact directory"]
    Server -->|"Read attempt-bound evidence"| Artifacts
```

The local operator starts the server and starts a worker with a checkout path.
The default worker claims one task and runs a fixed Git revision check. Codex
mode selects one usable container CLI before claiming work, runs one turn in
a disposable workspace, renews the lease, and
captures the patch and operator-configured check output on the host. A task
whose credentials require sign-in is marked blocked. See
[ADR 0003](adr/0003-bounded-codex-execution.md) and
[ADR 0006](adr/0006-local-container-runtime-selection.md). The task API returns the latest
attempt and derives its status in PostgreSQL. A task starts queued, becomes
claimed while its lease is active, and finishes when a result is recorded. The API supports lease
renewal. If a worker stops, the next worker can reclaim the task after expiry
with a new attempt ID. The task is exhausted after three expired attempts.
Reposting the same completed result is safe, and expired attempts cannot report
results. See [ADR 0002](adr/0002-worker-attempt-leases.md).

The server uses an unauthenticated loopback API for the groundwork. Run it
in a trusted local development environment. PostgreSQL stores the revision check
output directly. The development workflow and API examples are in the [local
setup guide](development.md).

## Chief of Staff web app

The Go server can serve the built React app and API on the same loopback origin.
The conversation is reconstructed from the 100 newest saved goals and their
latest attempts. Submitting a goal uses the existing task intake. The browser
polls for state changes and expands available worker artifacts in a project file.
The app reports the worker's recorded result. Local patch approval and rerunning
checks against a reviewed patch remain planned in issue [#10](https://github.com/nep-ai/difqo/issues/10).

See [ADR 0004](adr/0004-local-web-app.md) and the
[local web setup](development.md#chief-of-staff-web-app).

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
    Worker --> Runtime["Container runtime adapter"]
    Worker --> Codex["Codex App Server adapter"]
    Runtime --> Container["Disposable execution containers"]
    Codex -->|"JSON-RPC over stdio"| Container
    Container -->|"Read-only pinned skills and isolated writable workspace"| Host["Host workspaces, skills, and artifacts"]
    Worker -->|"Capture diff and check output"| Host
```

The control plane owns configuration, policy, approvals, and durable task
history. The worker manages execution and reports evidence through the task API.
Attempt leases protect the task result. Isolated workspaces and host-backed
evidence keep the patch and check output available after container removal.
The Mac mini proof of concept will verify authentication, recovery after
interruption, and host restart behavior.

See [ADR 0001](adr/0001-local-first-mvp-stack.md) for the tradeoffs behind this
layout.
