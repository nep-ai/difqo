# Difqo architecture

## Implemented local task flow

```mermaid
flowchart LR
    Operator["Local operator"] --> Browser["Chief of Staff web app"]
    Operator -->|"Poll an enrolled repository"| GitHubIntake["Local GitHub intake command"]
    GitHubIntake -->|"Read installation scope and conversation comments"| GitHub["GitHub API"]
    GitHubIntake -->|"Save linked identity, source, cursor, and task"| Postgres
    Operator -->|"Supply an offline normalized DM event"| SlackImport["Local Slack intake harness"]
    Operator -->|"Start the private app connector"| SlackSocket["Private Slack Socket Mode connector"]
    SlackSocket -->|"Open an authenticated WebSocket and acknowledge deliveries"| Slack["Slack"]
    SlackSocket -->|"Map private DM goal events"| SlackBinding["Persisted Slack binding"]
    Server -->|"Claim, poll, receipt, and revoke over loopback"| SlackFixture["Opt-in loopback relay fixture"]
    SlackFixture <-->|"Separate identity and installation callbacks, DM lookup"| SlackSimulator["Synthetic Slack endpoints"]
    SlackSimulator -->|"Signed URL challenge and private DM events"| SlackFixture
    SlackFixture -->|"Persist pairing, callback, claim, and deduplicated events"| SlackRelayDB[("Separate relay PostgreSQL database")]
    Server -->|"Persist Slack connection generation and commit delivered goals"| Postgres
    SlackImport -->|"Check the linked app, workspace, operator, and DM"| SlackBinding
    SlackBinding -->|"Save event identity and local task atomically"| Postgres
    Browser -->|"Save decisions, submit goals, and review patches over loopback HTTP"| Server["difqo server"]
    Browser -->|"Select a project and start an approved specialist revision"| Server
    Operator -->|"Register checkout bindings"| Server
    Operator -->|"Run a project-bound worker"| SpecialistWorker["Specialist worker"]
    SpecialistWorker -->|"Request a claim for its registered binding and report output"| Server
    SpecialistWorker -->|"Read the selected committed tree using Git"| ProjectSnapshot["Selected project snapshot"]
    Server -->|"Recheck authorization and persist run selection and attempts"| Postgres
    Operator -->|"Run the ordinary or Codex worker"| Worker
    Worker["difqo worker"] -->|"Claim a leased attempt and report its result over loopback HTTP"| Server
    Server -->|"Persist decisions, attempts, review checks, and approvals"| Postgres[("PostgreSQL on host")]
    Worker -->|"Read HEAD or make a shallow clone"| Repo["Operator-selected Git checkout"]
    Worker -->|"In Codex mode, validate runtime, image, and sandbox before claim"| Runtime["Docker, Podman, or compatible container CLI"]
    Runtime -->|"Run one App Server turn over stdio"| Codex["Disposable execution workspace"]
    Worker -->|"Capture base, diff, and check log"| Artifacts["Host artifact directory"]
    Server -->|"Read and verify attempt-bound evidence"| Artifacts
    Server -->|"Select a runtime with the saved local image"| Runtime
    Runtime -->|"Rerun the pinned check on the original base plus the patch"| Check["Disposable check container"]
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

The GitHub intake command uses a pre-issued installation token scoped to a
repository and accepts task requests from the locally configured GitHub user.
Imported tasks use the same decision and patch-review flow as local goals.
Workers opt into that repository's queue by numeric ID. See
[ADR 0007](adr/0007-github-intake.md).

Private Slack app intake connects outbound over Socket Mode and maps direct
messages to the same local binding used by the offline harness. Accepted
goals commit before acknowledgement. The official app remains the planned
default through a relay that can reuse the local intake rules. See
[ADR 0009](adr/0009-local-slack-intake.md).

## Chief of Staff web app

The Go server can serve the built React app and API on the same loopback origin.
The conversation is reconstructed from the 100 newest saved goals and their
latest attempts. Submitting a goal uses the existing task intake. The browser
polls for state changes and expands available worker artifacts in a project file.
The project file links to patch review. Goals capture the latest saved
decision revision at submission. For a captured patch, the review shows that
decision, the original base, the patch digest, and the check command and logs.
The server reruns the check in a disposable container reconstructed from the
captured base and digest-verified patch. PostgreSQL records approval for the
attempt and its latest passing rerun. The reviewed patch is available for
manual application. See [ADR 0005](adr/0005-exact-patch-review.md).

See [ADR 0004](adr/0004-local-web-app.md) and the
[local web setup](development.md#chief-of-staff-web-app).


## Specialist configuration

The operator registers local Git checkout bindings at server startup and
reviews specialist proposals in the Chief of Staff conversation. PostgreSQL
stores the proposal, decision and immutable approved configuration. Each
configuration has one bounded profile and an explicit project set. The Go
server checks eligibility against current bindings and policy. Approval and
disable actions are transactional and need no worker.

A specialist run pins the approved revision, project binding, and that
project's saved decision. A bound worker rechecks the current authorization
at claim time and uses local Git reads to produce its snapshot report.
The conversation shows attempts and failures from the run's project file.
See [ADR 0005](adr/0005-specialist-configuration.md) and
[ADR 0008](adr/0008-project-bound-specialist-runs.md).

## MVP target

```mermaid
flowchart LR
    Browser["React + Vite web app"] --> Intake
    Slack["Slack"] <-->|"Official app OAuth and signed DM events"| SlackRelay["Slack provider in Connections"]
    SlackRelay <-->|"Outbound enrollment, OAuth handoff, and delivery over configured HTTPS"| SlackConnector["Local official Slack connector"]
    Slack <-->|"Optional private app over Socket Mode"| SlackSocket["Local private Slack connector"]
    SlackConnector --> Intake
    SlackSocket --> Intake
    GitHub["GitHub API"] <-->|"Read comments and deliver approved replies"| GitHubPoller["Local outbound GitHub poller"]
    Official["Hosted Difqo credential service"] -->|"Scoped installation tokens"| GitHubPoller
    Private["Local private app credentials"] -->|"Mint installation tokens directly with GitHub"| GitHubPoller
    GitHubPoller --> Intake

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

The proposed official Slack connection separates workspace installation from
linking the operator's private DM. The local service connects outbound to the
relay and remains authoritative for tasks and approvals.
[Official Slack Connect design](adr/0010-official-slack-connect.md)
