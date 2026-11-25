# Apple silicon release runbook

## Stage and test

Every push to `main` runs the existing Web CI package checks and live
LaunchAgent smoke test. A successful run attempt has an **Apple silicon
staging candidate** summary with a version, run and attempt, revision, artifact
ID/link, and the artifact ZIP SHA-256. GitHub Actions keeps the immutable
artifact for 30 days. The summary also records the hashes of the three
installer assets inside the ZIP. A rerun that rebuilds the package produces a
new version and artifact. Railway Connections staging uses its existing
workflow and environment.

Download the exact artifact from the summary. Compare the downloaded ZIP with
the summary using `shasum -a 256 /path/to/candidate.zip`. Follow the
[Apple silicon pilot guide](macos-pilot.md) on a clean, logged-in Mac. Verify
the first goal, rerun, service recovery, and data-preservation checks covered by
[#42](https://github.com/nep-ai/difqo/issues/42). Record acceptance with the
version, revision, artifact ID, and ZIP digest. Use the same staging bytes to
exercise any landing bootstrap before public distribution. A CI pass alone
does not establish clean-machine or public installer acceptance.

## Prepare production once

In GitHub repository settings, create the `production` environment. Add
required reviewers, enable **Prevent self-review**, and disable administrator
bypass. Restrict deployments to **Selected branches and tags** with a single
branch rule for `main`. The dispatcher and reviewer must be different people.

Enable **immutable releases** in the repository's release settings, then set
the `production` environment variable
`DIFQO_IMMUTABLE_RELEASES_CONFIRMED=true`. The maintainer confirms that setting
because the workflow token lacks repository administration permission. Remove
the variable if the setting is turned off. Keep the repository's main branch
review rules in force. The workflow uses the built-in token and needs no
release secret or Railway credentials.

## Promote and hand off

1. Follow the public release criteria in [Maintaining Difqo](../MAINTAINING.md).
   Confirm that the recorded candidate passed acceptance and remains available.
2. On **Actions → Promote macOS pilot to production → Run workflow**, select
   `main`. Enter the recorded Web CI run ID, run attempt, artifact ID, and
   bare lowercase ZIP SHA-256. Have the independent production reviewer match
   those inputs to the acceptance record before approving.
3. Promotion checks the successful push attempt, current main ancestry,
   artifact metadata, ZIP digest, live smoke evidence, and the package
   checksum. It uploads those same files to a GitHub draft, checks every
   uploaded asset digest, and publishes an immutable **prerelease** at the
   tested revision. The tag equals the version
   (`v0.1.0-pilot.<run_id>.<run_attempt>`). It does not change the latest
   release or the website.
4. Verify that the public release is an immutable prerelease and the asset URLs
   use `https://github.com/nep-ai/difqo/releases/download/<version>/`.
   The production summary has the landing pin JSON with the version and the
   separate SHA-256 digests for `install-macos.sh`, `SHA256SUMS`, and
   `difqo-<version>-darwin-arm64.tar.gz`. A reviewed landing change pins those
   three files before enabling the public install command. The checksum file
   retains the existing archive checksum format. The core installer runs as
   `/bin/bash ./install-macos.sh "$version" "difqo-$version-darwin-arm64.tar.gz" ./SHA256SUMS --start`.

## Retry or roll back

- Retry with the same inputs while the staging artifact is retained. A
  matching draft resumes with any missing assets. A matching immutable
  prerelease returns the same pin. Conflicting tags, notes, or assets fail
  closed. Review an unpublished partial draft before removing any failed
  upload placeholder. Never overwrite release assets or move a published tag.
- If the artifact expires before publication, build a new candidate and
  repeat acceptance. A published release and its prior pin remain available
  independently of the staging artifact.
- If GitHub rejects tagging an older revision because its workflow files
  differ from current `main`, a maintainer with workflow write access can
  create the exact version as a lightweight tag at the accepted revision
  and retry the same inputs. The promotion job verifies that a pre-existing
  tag points to the tested commit. See the
  [GitHub release API permission rules](https://docs.github.com/en/rest/releases/releases#create-a-release).
- If an installer release fails acceptance after publication, revert the
  landing pin to the previous accepted version and its three digests, or
  disable installation if there is no accepted version. Verify the endpoint
  and try the reverted pin on a clean Mac. Correct the problem on `main` and
  test and promote a new version. If the published asset check fails, keep
  that version out of the landing pin and promote a new candidate.
- Website rollback only affects future installer downloads. For existing
  installations, pause the services and preserve the database and settings.
  Review the migrations between versions before reinstalling an older
  candidate. If the previous binary is incompatible with the applied schema,
  use a forward fix or restore a pre-upgrade backup into a separate database.
  Verify the restored data and the previous binary against that database
  before switching the service to it. Follow the PostgreSQL instructions for
  [creating and restoring a database dump](https://www.postgresql.org/docs/18/backup-dump.html).
