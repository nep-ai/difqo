# ADR 0012 — Build the Apple silicon PostgreSQL runtime from upstream source

## Context

The Apple silicon provisioning adapter accepts PostgreSQL 18 binaries,
and the portable CLI supports explicit external databases. A clean-machine
installer also needs a reviewed acquisition path for a relocatable runtime.

## Decision

Build the Apple silicon runtime in native CI from the
[PostgreSQL 18.7 source release](https://www.postgresql.org/ftp/source/v18.7/).
Pin the SHA-256 from that release in the builder and compare it before
extraction or execution. Retain the source hash, upstream version, and
unmodified `COPYRIGHT` alongside the runtime. The
[PostgreSQL License](https://www.postgresql.org/about/licence/) allows
redistribution with the copyright and license text preserved.

Use the upstream default relative install layout and minimize optional
library dependencies. Relocate Mach-O load paths to bundled targets and
reject dependencies on libraries outside the runtime and macOS system paths.
Re-sign binaries modified by relocation with an ad hoc signature.
Test the runtime after moving it into the checksum-verified Difqo bundle by
running the provisioning smoke on native Apple silicon.

An optional runtime lives under `postgresql/` inside the Difqo release.
The existing trusted release manifest covers both Difqo and PostgreSQL.
The local installer continues verifying the full archive before extraction.
The macOS provisioning adapter defaults to the bundled runtime when present
and still accepts an explicit PostgreSQL binary directory or database URL.

## Alternatives

Homebrew remains a convenient developer prerequisite, but its installed
libraries and fixed paths make it a poor standalone archive. Requiring a
package manager on users' Macs would add machine configuration to installation.
A prebuilt third-party distribution would require separate provenance,
redistribution, dependency, and relocation review.

## Consequences

This build is intended for the private SCRAM-authenticated loopback cluster.
The runtime omits optional ICU, readline, zlib, and TLS support. Operators
can use their own PostgreSQL for other requirements. Updating the pin needs
a reviewed change and native smoke evidence. Debian, Windows, persistent
service registration, clean-machine acceptance, and public publication
remain separate.
