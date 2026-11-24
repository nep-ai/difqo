# ADR 0013 — Run the installed macOS release as user LaunchAgents

## Context

The verified Apple silicon bundle can initialize a private database or migrate
an explicit external one. Starting the service should survive a user login
and reuse the database configuration without putting credentials in a job
definition or changing the portable Go server.

## Decision

Ship a macOS service adapter in the release. The adapter stages per-user
LaunchAgent plists in `~/Library/LaunchAgents` and loads them into the current
graphical session using `launchctl`. The Difqo server runs in the foreground
under launchd. The managed database uses a separate foreground PostgreSQL
agent. An external database is managed by its operator.

The server launcher reads the selected mode and credentials from private files.
The agent plists contain executable paths and the user's home directory.
The server plist sets an explicit search path for standard container tools.
A shared file-descriptor lock serializes start and stop operations and is
released if the holder exits abruptly.
The server binds to its existing loopback address, and startup waits for
the database-backed task endpoint. On failure, unload the jobs started by
that invocation and remove the plists it created. Keep any existing jobs
and all database and configuration state.

Pause records the desired state and disables the service targets in
launchd before unloading the jobs. A successful start from pause re-enables
those targets and clears the marker only after the health check passes.
A failed resume restores the disabled state. Restart serializes unloading
and startup under the same operation lock. Status distinguishes a healthy
running service, unloaded jobs, intentional pause, and unhealthy or
incomplete transitions.

## Consequences

An explicit start is required after staging and, for a managed database,
after provisioning. The plists retain absolute release paths. Service
updates and uninstall need their own lifecycle changes.
The stop command unloads the jobs for the current session and preserves
files so they can load on the next login.

Fixture checks use the real macOS plist tools with a simulated launchctl
session. Verification of registration and recovery on a clean Mac remains
part of onboarding acceptance. Debian and native Windows can use their own
service adapters with the same portable server and database commands.
