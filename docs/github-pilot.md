# Official GitHub intake pilot

Use the existing Difqo app installation and select the approved pilot
repository in GitHub. Grant the app **Issues** and **Pull requests** read
access. GitHub also grants repository metadata read access. The token issuer
requests these read permissions even when the installation has broader grants.

## Configure Connections

Run the `connections` binary on its numeric loopback listener behind the
approved HTTPS ingress. Route `POST /github/token` to it and keep request
authorization headers and response bodies out of proxy logs. GitHub-only
mode needs no relay database. If Slack shares the listener, keep its existing
configuration and migrations.

The [Railway template](../.railway/railway.ts) provisions Slack's private
database and passes the GitHub pilot settings through shared-variable references.
Its public ingress forwards the GitHub token route on the same service.
Define the listed shared variables before applying the template. For a
Slack-only deployment, define them with empty values. For a GitHub-only service, use
the same Connections image and `-railway` start mode with only the GitHub
settings below. The Slack migration pre-deploy command applies only to
deployments with the Slack relay database.

| Setting | Value |
| --- | --- |
| `DIFQO_GITHUB_APP_ID` | Numeric ID of the existing official GitHub App |
| `DIFQO_GITHUB_INSTALLATION_ID` | Numeric installation ID for the selected account |
| `DIFQO_GITHUB_REPOSITORY` | Exact GitHub owner/name of the approved repository |
| `DIFQO_GITHUB_REPOSITORY_ID` | Numeric ID of that repository |
| `DIFQO_GITHUB_OWNER_LOGIN` | GitHub login of the pilot operator |
| `DIFQO_GITHUB_OWNER_ID` | Numeric GitHub user ID of that operator |
| `DIFQO_GITHUB_APP_PRIVATE_KEY` | Existing RSA private key in PEM format, set directly in the hosting secret store |
| `DIFQO_GITHUB_PILOT_PROOF_SHA256` | Hex SHA-256 digest of the exact ASCII proof supplied to the local instance |

Generate the proof using 32 cryptographically random bytes encoded as 64 hex
characters. Hash those characters without a trailing newline. Provide the
proof to the pilot operator through the approved secret channel and set it
as `DIFQO_GITHUB_CONNECTION_PROOF` locally. Rotate both sides to revoke the
local connection credential. The app key belongs only in the hosted service.

## Accept the pilot

Build the updated `difqo` and `connections` binaries. Run the synthetic
issuer and disposable PostgreSQL intake tests before configuring live secrets.

```sh
go test -race ./internal/githubconnection
DIFQO_GITHUB_TEST_DATABASE_URL="$DIFQO_DATABASE_URL" \
  go test -count=1 ./internal/githubintake
```

After the owner has configured the HTTPS deployment, choose a UTC enrollment
start at second precision just before submitting a synthetic request in an
issue or pull request conversation on the enrolled repo. Use the same start
time on every run. An existing manual enrollment must use its saved owner
and start time. Clear `DIFQO_GITHUB_TOKEN` before using the official path.

```sh
./bin/difqo github-intake -connections "$DIFQO_CONNECTIONS_ORIGIN" \
  -since "$DIFQO_GITHUB_ENROLLED_AT"
```

Post a benign goal starting with `@difqo` from the pinned operator account.
Confirm it appears once in the local task list and that polling again adds
zero tasks. Claim it with a Codex worker for the enrolled repository using
`-github-repository-id` and the trusted local checkout. Review and approval
remain in the local app. Neither Connections nor the poller publishes
a comment, mutates a pull request, or applies a patch.

If GitHub access is removed, the proof is rotated, or the configured account
no longer has access, the next official poll fails before importing anything.
GitHub token issuance and collaborator checks are documented in
[GitHub's installation token guide](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app)
and [repository permission API](https://docs.github.com/en/rest/collaborators/collaborators#get-repository-permissions-for-a-user).
