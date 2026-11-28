# Difqo architecture

## Implemented local task flow

```mermaid
flowchart LR
    Operator["Local operator"] --> Browser["Chief of Staff web app"]
    Operator -->|"Prepare an existing database before starting Difqo"| Setup["Portable database setup CLI"]
    Setup -->|"Apply bundle migrations and record checksums atomically"| Postgres
    Operator -->|"Optionally provision on Apple silicon"| Provision["macOS PostgreSQL adapter"]
    Bundle["Verified Apple silicon release"] -->|"Optional relocatable PostgreSQL runtime"| Provision
    Bundle -->|"Per-user startup adapter"| LaunchAgent["macOS launchd"]
    LaunchAgent -->|"Run server in the logged-in session"| Server
    LaunchAgent -->|"Run the managed cluster when selected"| Postgres
    Provision -->|"Initialize private loopback cluster"| Postgres
    Provision -->|"Run the same migration command"| Setup
    Operator -->|"Poll an enrolled repository"| GitHubIntake["Local GitHub intake"]
    GitHubIntake -->|"Request the pilot's read-only repository token over HTTPS"| GitHubBroker["GitHub provider in Connections"]
    GitHubBroker -->|"Mint a scoped token and verify owner access"| GitHub
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
    Browser -->|"Save decisions, submit text and attachments, and review patches over loopback HTTP"| Server["difqo server"]
    Browser -->|"Send a message or file to Chief of Staff"| Server
    Server -->|"Persist the conversation and one active turn lease"| Postgres
    Server -->|"Run a conversation turn with no project checkout"| ChiefRuntime["Isolated Chief of Staff Codex turn"]
    Browser -->|"Confirm a coding goal and eligible project"| Server
    Server -->|"Pin project binding and saved decision in the coding queue"| Postgres
    LaunchAgent -->|"Run a bounded local coding queue for confirmed goals"| Worker
    Browser -->|"Sign in with ChatGPT, link Slack, or connect and sync GitHub"| Server
    Server -->|"Run the installed Mac setup and sample commands"| WorkerHelper["Packaged Mac worker helper"]
    WorkerHelper -->|"Build the image and use the Codex device sign-in"| Runtime
    WorkerHelper -->|"Run the selected sample in a disposable repository"| Worker
    Server -->|"Connect and sync through the same scoped intake"| GitHubIntake
    Server -->|"Enroll, link and poll the official Slack DM"| OfficialSlack["Official Connections Slack relay"]
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
    Runtime -->|"Run a text and image App Server turn over stdio"| Codex["Disposable execution workspace"]
    Worker -->|"Capture base, candidate commit, diff, and check log"| Artifacts["Host artifact directory"]
    Server -->|"Read and verify attempt-bound evidence"| Artifacts
    Server -->|"Select a runtime with the saved local image"| Runtime
    Runtime -->|"Rerun the pinned check at the recorded candidate commit"| Check["Disposable check container"]
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

The official GitHub intake pilot requests a scoped installation token from
Connections and polls GitHub directly. Connections checks the configured
operator's current repository access before releasing the token. Manual
intake can also use a pre-issued installation token.
Imported tasks use the same decision and patch-review flow as local goals.
Workers opt into that repository's queue by numeric ID. See
[ADR 0007](adr/0007-github-intake.md) and [ADR 0015](adr/0015-official-github-pilot.md).

Private Slack app intake connects outbound over Socket Mode and maps direct
messages to the same local binding used by the offline harness. Accepted
goals commit before acknowledgement. The official app remains the planned
default through a relay that can reuse the local intake rules. See
[ADR 0009](adr/0009-local-slack-intake.md).

The installed CLI verifies connectivity to the operator-selected PostgreSQL
database and applies the bundled migrations and their history in one
transaction. On Apple silicon, the release adapter uses a bundled or operator-supplied
PostgreSQL 18 binary directory to provision a private loopback cluster and
calls the same CLI to set up the database. See
[ADR 0011](adr/0011-portable-database-setup.md).

The installed macOS service adapter registers per-user jobs and checks
readiness through the loopback task API. It preserves external database
configuration without registering a local database job. See
[ADR 0013](adr/0013-macos-launch-agents.md).

## Chief of Staff web app

The Go server can serve the built React app and API on the same loopback origin.
The installed Mac's main composer sends text and files to a one-to-one Chief of
Staff conversation. The local API persists each turn and its reply in
PostgreSQL. Codex receives recent completed turns as context in an isolated,
empty workspace. Chief of Staff can ask about files sent on their own. The
existing coding goal action in Project tools uses task intake. The browser mixes
saved conversation turns and coding goals by creation time, polls for state
changes, and expands available worker artifacts in a project file.
The project file links to patch review. Goals capture the latest saved
decision revision at submission. For a captured patch, the review shows that
decision, the original base, the checked candidate, the patch digest, and the
check command and logs. The server checks out the recorded candidate commit
for reruns in a disposable container. Older attempts reconstruct the base
and digest-verified patch. PostgreSQL records approval for the
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
    Browser["React + Vite web app"] <-->|"Shared conversation and task actions"| Intake
    Slack["Slack"] <-->|"OAuth, signed DM events, files, and interactive replies"| RelayIngress["Railway HTTPS ingress and same-container proxy"]
    RelayIngress -->|"Unchanged signed request body"| SlackRelay["Slack provider on Connections loopback"]
    SlackRelay <-->|"Outbound enrollment, OAuth handoff, delivery, and replies over HTTPS"| SlackConnector["Local official Slack connector"]
    Slack <-->|"Optional private app over Socket Mode"| SlackSocket["Local private Slack connector"]
    SlackConnector <-->|"Shared conversation and task actions"| Intake
    SlackSocket <-->|"Shared conversation and task actions"| Intake
    GitHub["GitHub API"] <-->|"Read comments and deliver approved replies"| GitHubPoller["Local outbound GitHub poller"]
    Official["Hosted Difqo credential service"] -->|"Scoped installation tokens"| GitHubPoller
    Private["Local private app credentials"] -->|"Mint installation tokens directly with GitHub"| GitHubPoller
    GitHubPoller --> Intake

    subgraph Server["Go server process"]
        Intake["Shared task intake"] --> CoS["Chief of Staff"]
        CoS -->|"Delegate work and review"| Policy["Policy, approvals, and tool gateway"]
        Policy --> TaskAPI["Task API and scheduler"]
        Policy --> Publisher["Project-scoped PR publisher"]
    end

    Intake --> DB[("Host PostgreSQL")]
    TaskAPI --> DB
    Publisher -->|"Reconcile branch, PR, and current-head evidence"| DB
    Publisher <-->|"Push a branch, open a draft, and update review state"| GitHub
    Official -->|"Repository-scoped publication credentials"| Publisher
    Private -->|"Repository-scoped publication credentials"| Publisher
    Worker["Go worker process"] <-->|"Claim, renew, and report"| TaskAPI
    ReviewSpecialist["Code review specialist"] <-->|"Claim a head-bound review and return findings to Chief of Staff’s task"| TaskAPI
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

For projects with GitHub delivery enabled, Chief of Staff's target result is a
pull request with verification and independent review on its current head.
Chief of Staff assigns the code review specialist, receives findings on the
original task, and coordinates fixes and re-review. The local web app and
optional Slack DM expose the same conversation and task actions. Local-only
projects and exceptions use the local review path.
See [ADR 0014](adr/0014-chief-of-staff-pr-delivery.md).
