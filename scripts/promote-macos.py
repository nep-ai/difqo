#!/usr/bin/env python3
"""Promote the exact macOS staging bytes to a versioned GitHub prerelease."""

import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import zipfile


REPO = "nep-ai/difqo"
WEB_WORKFLOW = ".github/workflows/web.yml"


def require(condition, message):
    if not condition:
        raise SystemExit(message)


def api(endpoint, *args, output=None):
    command = ["gh", "api", f"repos/{REPO}/{endpoint}", *args]
    if output is not None:
        with output.open("wb") as archive:
            subprocess.run(command, stdout=archive, check=True)
        return None
    return json.loads(subprocess.check_output(command))


def sha256(path):
    with path.open("rb") as file:
        return hashlib.file_digest(file, "sha256").hexdigest()


def check_production_gate():
    environment = api("environments/production")
    require(
        any(
            rule["type"] == "required_reviewers"
            and rule.get("prevent_self_review") is True
            and rule.get("reviewers")
            for rule in environment["protection_rules"]
        ),
        "Production must require an independent reviewer.",
    )
    require(
        (environment["deployment_branch_policy"] or {}).get("custom_branch_policies")
        is True,
        "Production must use a custom deployment branch policy.",
    )
    policies = api("environments/production/deployment-branch-policies")
    require(
        policies["total_count"] == 1
        and [(rule["type"], rule["name"]) for rule in policies["branch_policies"]]
        == [("branch", "main")],
        "The only production deployment rule must allow the main branch.",
    )
    # GitHub's settings endpoint needs Administration: read, which the job token lacks.
    require(
        os.getenv("IMMUTABLE_RELEASES_CONFIRMED") == "true",
        "Enable immutable releases and record confirmation in the production environment.",
    )


def fetch_candidate(run_id, attempt, artifact_id, digest, directory):
    workflow = api(f"actions/workflows/{WEB_WORKFLOW.rsplit('/', 1)[-1]}")
    run = api(f"actions/runs/{run_id}/attempts/{attempt}")
    revision = run["head_sha"]
    require(
        run["workflow_id"] == workflow["id"]
        and run["path"] == workflow["path"] == WEB_WORKFLOW
        and str(run["id"]) == run_id
        and str(run["run_attempt"]) == attempt
        and run["event"] == "push"
        and run["head_branch"] == "main"
        and run["head_repository"]["full_name"] == REPO
        and run["repository"]["full_name"] == REPO
        and run["status"] == "completed"
        and run["conclusion"] == "success"
        and re.fullmatch(r"[0-9a-f]{40}", revision),
        "The source must be a successful Web CI push attempt from this repository's main branch.",
    )
    require(
        api(f"compare/{revision}...main")["status"] in {"ahead", "identical"},
        "Candidate revision is no longer on main.",
    )
    artifact = api(f"actions/artifacts/{artifact_id}")
    source = artifact["workflow_run"]
    require(
        str(artifact["id"]) == artifact_id
        and artifact["name"] == f"difqo-macos-arm64-pilot-{attempt}"
        and artifact["expired"] is False
        and artifact["digest"] == f"sha256:{digest}"
        and str(source["id"]) == run_id
        and source["repository_id"] == source["head_repository_id"] == run["repository"]["id"]
        and source["head_branch"] == "main"
        and source["head_sha"] == revision,
        "Artifact ID, provenance, attempt, or digest did not match. It may have expired.",
    )

    archive = directory / "candidate.zip"
    api(f"actions/artifacts/{artifact_id}/zip", output=archive)
    require(sha256(archive) == digest, "Downloaded artifact ZIP digest did not match.")

    version = f"v0.1.0-pilot.{run_id}.{attempt}"
    package = f"difqo-{version}-darwin-arm64.tar.gz"
    assets = [
        package, "SHA256SUMS", "install-macos.sh", "VERSION", "REVISION",
        "START-HERE.md", "start-pilot.sh",
    ]
    with zipfile.ZipFile(archive) as candidate:
        require(
            sorted(candidate.namelist()) == sorted([*assets, "LIVE-SMOKE"]),
            "Unexpected candidate files or missing live smoke evidence.",
        )
        require(candidate.read("LIVE-SMOKE") == b"passed\n", "Live smoke did not pass.")
        for name in assets:
            # Write only known file names. Never execute candidate content.
            with candidate.open(name) as source_file, (directory / name).open("xb") as dest:
                shutil.copyfileobj(source_file, dest)

    require((directory / "VERSION").read_bytes() == f"{version}\n".encode(), "Version mismatch.")
    require((directory / "REVISION").read_bytes() == f"{revision}\n".encode(), "Revision mismatch.")
    digests = {name: f"sha256:{sha256(directory / name)}" for name in assets}
    checksum = f"{digests[package].removeprefix('sha256:')}  {package}\n"
    require((directory / "SHA256SUMS").read_bytes() == checksum.encode(), "Package checksum mismatch.")
    return version, revision, digests


def check_tag(version, revision, *, required=False):
    refs = api(f"git/matching-refs/tags/{version}")
    tags = [ref["object"] for ref in refs if ref["ref"] == f"refs/tags/{version}"]
    require(
        (bool(tags) or not required)
        and all(tag["type"] == "commit" and tag["sha"] == revision for tag in tags),
        "Release tag must point directly to the tested revision.",
    )


def matching_assets(release_id, digests):
    assets = api(f"releases/{release_id}/assets?per_page=100")
    names = set()
    for asset in assets:
        name = asset["name"]
        require(
            name in digests and name not in names and asset["state"] == "uploaded"
            and asset["digest"] == digests[name],
            f"Unexpected or conflicting release asset: {name}. Review the draft before retrying.",
        )
        names.add(name)
    return names


def publish(version, revision, digests, directory, run_id, attempt, artifact_id, digest):
    body = (
        "Apple silicon macOS pilot. See START-HERE.md for setup, prerequisites, and pilot checks.\n\n"
        f"- Tested revision: `{revision}`\n"
        f"- [Web CI attempt {attempt}](https://github.com/{REPO}/actions/runs/{run_id}/attempts/{attempt})\n"
        f"- Actions artifact: `{artifact_id}`\n"
        f"- Artifact ZIP SHA-256: `{digest}`\n"
    )
    check_tag(version, revision)
    pages = api("releases?per_page=100", "--paginate", "--slurp")
    matches = [release for page in pages for release in page if release["tag_name"] == version]
    require(len(matches) <= 1, "Multiple releases use this version. Review them before retrying.")
    if matches:
        release = matches[0]
        require(
            release["prerelease"] is True
            and release["target_commitish"] == revision
            and (release["body"] or "").rstrip() == body.rstrip(),
            "Existing release metadata conflicts with the tested candidate.",
        )
    else:
        release = api(
            "releases", "--method", "POST", "-f", f"tag_name={version}", "-f", f"name=Difqo {version}",
            "-f", f"target_commitish={revision}", "-f", f"body={body}",
            "-F", "draft=true", "-F", "prerelease=true", "-f", "make_latest=false",
        )

    release_id = release["id"]
    present = matching_assets(release_id, digests)
    if release["draft"]:
        missing = sorted(digests.keys() - present)
        if missing:
            subprocess.run(
                ["gh", "release", "upload", version, "--repo", REPO,
                 *(str(directory / name) for name in missing)],
                check=True,
            )
        require(matching_assets(release_id, digests) == digests.keys(), "Incomplete draft assets.")
        check_tag(version, revision)
        release = api(
            f"releases/{release_id}", "--method", "PATCH", "-F", "draft=false", "-f", "make_latest=false",
        )
    check_tag(version, revision, required=True)
    require(
        release["draft"] is False and release["prerelease"] is True and release["immutable"] is True,
        "Expected an immutable published prerelease. Do not enable installer distribution.",
    )
    require(
        matching_assets(release_id, digests) == digests.keys(),
        "Published release has missing assets. Do not enable installer distribution.",
    )
    return release["html_url"]


def main():
    run_id, attempt, artifact_id = (
        os.environ[name] for name in ("SOURCE_RUN", "SOURCE_ATTEMPT", "SOURCE_ARTIFACT")
    )
    digest = os.environ["SOURCE_DIGEST"]
    require(
        all(re.fullmatch(r"[1-9][0-9]{0,19}", value) for value in (run_id, attempt, artifact_id))
        and re.fullmatch(r"[0-9a-f]{64}", digest),
        "Provide numeric Actions identifiers and a lowercase 64-character artifact SHA-256.",
    )
    check_production_gate()
    with tempfile.TemporaryDirectory() as scratch:
        directory = Path(scratch)
        version, revision, digests = fetch_candidate(run_id, attempt, artifact_id, digest, directory)
        url = publish(version, revision, digests, directory, run_id, attempt, artifact_id, digest)

    pin = {
        "version": version,
        "installerSha256": digests["install-macos.sh"].removeprefix("sha256:"),
        "checksumsSha256": digests["SHA256SUMS"].removeprefix("sha256:"),
        "archiveSha256": digests[f"difqo-{version}-darwin-arm64.tar.gz"].removeprefix("sha256:"),
    }
    summary = [
        f"### [Difqo {version}]({url})", "",
        f"Revision: `{revision}`", "",
        f"Staging artifact {artifact_id} SHA-256: `{digest}`", "",
        "Reviewed landing installer pin", "", "```json",
        json.dumps(pin, indent=2), "```", "",
    ]
    with Path(os.environ["GITHUB_STEP_SUMMARY"]).open("a") as output:
        output.write("\n".join(summary))


if __name__ == "__main__":
    main()
