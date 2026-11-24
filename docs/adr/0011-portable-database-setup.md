# ADR 0011 — Prepare PostgreSQL through the Difqo CLI

## Context

An installed release includes the same SQL migrations used by source
checkouts. Setup needs to run from the bundle on each supported OS, recover
from interrupted upgrades, and preserve a database migrated by the existing
documented `psql` flow.

## Decision

Use the existing PostgreSQL driver and `DIFQO_DATABASE_URL` for the database the
operator selects. `difqo setup-database` checks connectivity and applies the SQL
files from the installed bundle. The setup command uses a dedicated Difqo
database with `public` as its current schema.

Keep the original files usable directly with `psql`. The CLI removes each
outer transaction wrapper and runs the migration body in a PostgreSQL block
that cannot commit or roll back the enclosing transaction. All pending SQL
and the migration ledger commit together under a database advisory lock.
Record the release filename and checksum of each applied migration. Reject
gaps, changed files, and attempts to run an older bundle against newer
migration history.

For an existing installation with no ledger, require the operator to name
the last migration applied successfully by hand. That assertion is the
authority for the recorded prefix. Setup refuses to infer the version from
table names. The adoption and all remaining migrations commit together.

The proposed default for a clean machine is a dedicated, per-user PostgreSQL
18 cluster bound to loopback with a generated credential. Platform provisioning
must establish the cluster and create the Difqo database and role before
calling the portable setup command. The explicit existing-database option
will continue to bypass provisioning.

The Apple silicon bundle ships an adapter that accepts a trusted PostgreSQL 18
binary directory. A mode-0700 cluster directory holds the port and private
credential. Initialization uses an ephemeral user-private admin socket.
The regular process serves only the non-superuser Difqo role over authenticated
loopback TCP. The adapter temporarily starts PostgreSQL to apply migrations and
then stops it. PostgreSQL binary distribution and per-user service registration
are separate concerns.

## Consequences

A setup failure leaves the previous database and migration history available
for retry. Run setup with existing Difqo services stopped. The advisory lock
coordinates simultaneous setup commands, while manual `psql` and application
processes do not participate in that lock.

Future migrations must remain transaction-safe. Recovering from an incorrect
manually asserted baseline requires restoring or repairing the database using
the operator's records. Platform provisioning, credential storage, and service
registration are separate changes.
