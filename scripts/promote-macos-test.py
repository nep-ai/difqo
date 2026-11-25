"""Offline checks for the macOS promotion gate and retry behavior."""

import hashlib
from pathlib import Path
import runpy
import tempfile
import unittest
from unittest.mock import patch
import zipfile


promotion = runpy.run_path(str(Path(__file__).with_name("promote-macos.py")))
revision = "a" * 40
version = "v0.1.0-pilot.123.2"
package = f"difqo-{version}-darwin-arm64.tar.gz"


class PromotionTest(unittest.TestCase):
    def test_production_requires_independent_review_and_main_only(self):
        gate = promotion["check_production_gate"]
        rule = {"type": "required_reviewers", "prevent_self_review": True, "reviewers": [{}]}
        branch = {"type": "branch", "name": "main"}
        responses = {
            "environments/production": {
                "protection_rules": [rule],
                "deployment_branch_policy": {"custom_branch_policies": True},
            },
            "environments/production/deployment-branch-policies": {
                "total_count": 1, "branch_policies": [branch],
            },
        }
        with patch.dict(gate.__globals__, api=responses.__getitem__):
            with patch.dict("os.environ", IMMUTABLE_RELEASES_CONFIRMED="true"):
                gate()
                branch["type"] = "tag"
                with self.assertRaisesRegex(SystemExit, "only production deployment rule"):
                    gate()
                rule["prevent_self_review"] = False
                with self.assertRaisesRegex(SystemExit, "independent reviewer"):
                    gate()

    def test_candidate_provenance_and_bytes(self):
        fetch = promotion["fetch_candidate"]
        repo = {"full_name": promotion["REPO"], "id": 1}
        workflow = {"id": 2, "path": promotion["WEB_WORKFLOW"]}
        run = {
            "id": 123, "workflow_id": 2, "path": workflow["path"], "run_attempt": 2,
            "head_sha": revision, "event": "push", "head_branch": "main",
            "head_repository": repo, "repository": repo, "status": "completed",
            "conclusion": "success",
        }
        with tempfile.TemporaryDirectory() as scratch:
            directory = Path(scratch)
            zip_path = directory / "fixture.zip"
            with zipfile.ZipFile(zip_path, "w") as fixture:
                for name in ["install-macos.sh", "START-HERE.md", "start-pilot.sh", package]:
                    fixture.writestr(name, b"synthetic")
                fixture.writestr("VERSION", version + "\n")
                fixture.writestr("REVISION", revision + "\n")
                fixture.writestr("LIVE-SMOKE", "passed\n")
                fixture.writestr(
                    "SHA256SUMS", hashlib.sha256(b"synthetic").hexdigest() + f"  {package}\n",
                )

            digest = promotion["sha256"](zip_path)
            artifact = {
                "id": 456, "name": "difqo-macos-arm64-pilot-2", "expired": False,
                "digest": f"sha256:{digest}",
                "workflow_run": {
                    "id": 123, "head_sha": revision, "repository_id": 1,
                    "head_repository_id": 1, "head_branch": "main",
                },
            }
            responses = {
                "actions/workflows/web.yml": workflow,
                "actions/runs/123/attempts/2": run,
                f"compare/{revision}...main": {"status": "ahead"},
                "actions/artifacts/456": artifact,
            }

            def api(endpoint, *, output=None):
                if endpoint == "actions/artifacts/456/zip":
                    output.write_bytes(zip_path.read_bytes())
                    return None
                return responses[endpoint]

            with patch.dict(fetch.__globals__, api=api):
                self.assertEqual(fetch("123", "2", "456", digest, directory)[:2], (version, revision))
                zip_path.write_bytes(b"corrupted")
                with self.assertRaisesRegex(SystemExit, "Downloaded artifact ZIP digest"):
                    fetch("123", "2", "456", digest, directory)
                artifact["name"] = "difqo-macos-arm64-pilot-1"
                with self.assertRaisesRegex(SystemExit, "provenance, attempt, or digest"):
                    fetch("123", "2", "456", digest, directory)
                run["event"] = "pull_request"
                with self.assertRaisesRegex(SystemExit, "successful Web CI push"):
                    fetch("123", "2", "456", digest, directory)

    def test_draft_retry_and_matching_published_release(self):
        publish = promotion["publish"]
        digests = {"SHA256SUMS": "sha256:aaa", package: "sha256:bbb"}
        tag = {
            "ref": f"refs/tags/{version}", "object": {"type": "commit", "sha": revision},
        }
        release = {"id": 3, "draft": True, "prerelease": True, "tag_name": version}
        assets = [{"name": "SHA256SUMS", "digest": "sha256:aaa", "state": "uploaded"}]
        lose_asset_on_publish = False

        def api(endpoint, *args):
            if endpoint.startswith("git/matching-refs/"):
                return [tag]
            if endpoint == "releases?per_page=100":
                return [[release]] if "body" in release else [[]]
            if endpoint == "releases":
                release["target_commitish"] = revision
                release["body"] = next(v[5:] for v in args if v.startswith("body="))
                return release
            if endpoint == "releases/3/assets?per_page=100":
                return assets
            if endpoint == "releases/3":
                if lose_asset_on_publish:
                    assets.pop()
                release.update(draft=False, immutable=True, html_url="https://example.invalid/release")
                return release
            self.fail(f"Unexpected API endpoint {endpoint}")

        def upload(command, *, check):
            self.assertTrue(check)
            self.assertEqual(command[-1], f"/candidate/{package}")
            assets.append({"name": package, "digest": "sha256:bbb", "state": "uploaded"})

        args = (version, revision, digests, Path("/candidate"), "123", "2", "456", "c" * 64)
        with patch.dict(publish.__globals__, api=api), patch("subprocess.run", side_effect=upload) as up:
            self.assertEqual(publish(*args), "https://example.invalid/release")
            self.assertEqual(publish(*args), "https://example.invalid/release")
            up.assert_called_once()
            assets[0]["digest"] = "sha256:conflict"
            with self.assertRaisesRegex(SystemExit, "conflicting release asset"):
                publish(*args)
            tag["object"]["sha"] = "b" * 40
            with self.assertRaisesRegex(SystemExit, "tag must point directly"):
                publish(*args)
            tag["object"]["sha"] = revision
            assets[0]["digest"] = "sha256:aaa"
            release["draft"] = True
            lose_asset_on_publish = True
            with self.assertRaisesRegex(SystemExit, "Published release has missing assets"):
                publish(*args)


if __name__ == "__main__":
    unittest.main()
