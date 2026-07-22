#!/usr/bin/env python3
"""Deterministic local tests for the repository-local attestation action."""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


ACTION = Path(__file__).with_name("attest.py")
ACTION_METADATA = Path(__file__).with_name("action.yml")
EXAMPLE_WORKFLOW = ACTION.parents[2] / "workflows" / "attest-example.yml"
START = "<!-- skill-attestation:start -->"
END = "<!-- skill-attestation:end -->"


class IssuerHandler(BaseHTTPRequestHandler):
    requests: list[dict[str, object]] = []
    responses: list[bytes] = []

    def do_POST(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler API
        body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        payload = json.loads(body)
        type(self).requests.append(
            {
                "path": self.path,
                "authorization": self.headers.get("Authorization"),
                "content_type": self.headers.get("Content-Type"),
                "payload": payload,
            }
        )

        learner_id = payload.get("learner_id")
        if learner_id == "fail":
            self.send_response(422)
            response = b'{"error":"rejected"}'
        elif learner_id == "invalid-json":
            self.send_response(200)
            response = b"not-json"
        elif learner_id == "no-proof":
            self.send_response(200)
            response = b'{"schema_version":"1"}'
        else:
            self.send_response(201)
            response = json.dumps(
                {
                    "schema_version": "1",
                    "proof_url": 'https://proof.example.test/a?x=1&y="z"',
                    "payload": payload,
                },
                separators=(",", ":"),
            ).encode()

        type(self).responses.append(response)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(response)))
        self.end_headers()
        self.wfile.write(response)

    def log_message(self, _format: str, *_args: object) -> None:
        return


class AttestationActionTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), IssuerHandler)
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        cls.issuer_url = f"http://127.0.0.1:{cls.server.server_port}/base/"

    @classmethod
    def tearDownClass(cls) -> None:
        cls.server.shutdown()
        cls.server.server_close()
        cls.thread.join(timeout=2)

    def setUp(self) -> None:
        IssuerHandler.requests.clear()
        IssuerHandler.responses.clear()
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.workspace = Path(self.temp.name)
        self.readme = self.workspace / "README.md"
        self.output = self.workspace / "artifacts" / "project-attestation.json"
        self.github_output = self.workspace / "github-output.txt"
        self.original_readme = b"# Demo\n\nKeep this byte-for-byte.\n"
        self.readme.write_bytes(self.original_readme)

    def run_action(self, **overrides: str) -> subprocess.CompletedProcess[str]:
        env = os.environ.copy()
        env.update(
            {
                "GITHUB_WORKSPACE": str(self.workspace),
                "GITHUB_OUTPUT": str(self.github_output),
                "INPUT_ISSUER_URL": self.issuer_url,
                "INPUT_ISSUER_TOKEN": 'secret-$HOME-"-token',
                "INPUT_LEARNER_ID": 'learner-"42"',
                "INPUT_PROJECT_NAME": 'Project "A"\nSecond line',
                "INPUT_EVALUATION_SCORE": "97",
                "INPUT_ISSUED_AT": "",
                "INPUT_OUTPUT_PATH": "artifacts/project-attestation.json",
                "INPUT_BADGE_LABEL": '<Agent & "Model">',
                "INPUT_README_PATH": "README.md",
            }
        )
        env.update(overrides)
        return subprocess.run(
            [sys.executable, str(ACTION)],
            cwd=self.workspace,
            env=env,
            check=False,
            capture_output=True,
            text=True,
        )

    def test_success_escapes_payload_writes_outputs_and_updates_badge_idempotently(self) -> None:
        first = self.run_action()
        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertEqual(len(IssuerHandler.requests), 1)

        request = IssuerHandler.requests[0]
        self.assertEqual(request["path"], "/base/v1/attestations")
        self.assertEqual(request["authorization"], 'Bearer secret-$HOME-"-token')
        self.assertEqual(request["content_type"], "application/json")
        self.assertEqual(
            request["payload"],
            {
                "learner_id": 'learner-"42"',
                "project_name": 'Project "A"\nSecond line',
                "evaluation_score": 97,
            },
        )
        self.assertEqual(self.output.read_bytes(), IssuerHandler.responses[0])

        changed_readme = self.readme.read_text()
        self.assertTrue(changed_readme.startswith(self.original_readme.decode()))
        self.assertEqual(changed_readme.count(START), 1)
        self.assertEqual(changed_readme.count(END), 1)
        self.assertIn("&lt;Agent &amp; &quot;Model&quot;&gt;", changed_readme)
        self.assertIn("Score 97/100", changed_readme)
        self.assertIn("Execution PASS", changed_readme)
        self.assertIn("x=1&amp;y=&quot;z&quot;", changed_readme)
        self.assertNotIn('<Agent & "Model">', changed_readme)

        action_outputs = self.github_output.read_text()
        self.assertIn('proof-url=https://proof.example.test/a?x=1&y="z"\n', action_outputs)
        self.assertIn(f"attestation-path={self.output.resolve()}\n", action_outputs)

        before_second_run = self.readme.read_bytes()
        second = self.run_action()
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertEqual(self.readme.read_bytes(), before_second_run)
        self.assertEqual(self.readme.read_text().count(START), 1)

    def test_optional_issued_at_is_included(self) -> None:
        result = self.run_action(INPUT_ISSUED_AT="2026-07-21T12:34:56Z")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(
            IssuerHandler.requests[0]["payload"],
            {
                "learner_id": 'learner-"42"',
                "project_name": 'Project "A"\nSecond line',
                "evaluation_score": 97,
                "issued_at": "2026-07-21T12:34:56Z",
            },
        )

    def assert_failure_preserves_files(self, learner_id: str) -> subprocess.CompletedProcess[str]:
        self.output.parent.mkdir(parents=True)
        self.output.write_bytes(b"existing-attestation\n")
        before_readme = self.readme.read_bytes()
        result = self.run_action(INPUT_LEARNER_ID=learner_id)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.output.read_bytes(), b"existing-attestation\n")
        self.assertEqual(self.readme.read_bytes(), before_readme)
        self.assertNotIn('secret-$HOME-"-token', result.stderr)
        return result

    def test_non_2xx_preserves_existing_files(self) -> None:
        result = self.assert_failure_preserves_files("fail")
        self.assertIn("HTTP 422", result.stderr)

    def test_invalid_json_preserves_existing_files(self) -> None:
        result = self.assert_failure_preserves_files("invalid-json")
        self.assertIn("valid JSON", result.stderr)

    def test_missing_proof_url_preserves_existing_files(self) -> None:
        result = self.assert_failure_preserves_files("no-proof")
        self.assertIn("proof_url", result.stderr)

    def test_invalid_score_fails_before_request(self) -> None:
        result = self.run_action(INPUT_EVALUATION_SCORE="97; echo injected")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(IssuerHandler.requests, [])
        self.assertFalse(self.output.exists())
        self.assertEqual(self.readme.read_bytes(), self.original_readme)

    def test_paths_outside_workspace_are_rejected_before_request(self) -> None:
        result = self.run_action(INPUT_OUTPUT_PATH="../outside.json")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(IssuerHandler.requests, [])
        self.assertIn("inside GITHUB_WORKSPACE", result.stderr)

    def test_action_metadata_declares_and_wires_contract(self) -> None:
        metadata = ACTION_METADATA.read_text()
        for input_name in (
            "issuer-url",
            "issuer-token",
            "learner-id",
            "project-name",
            "evaluation-score",
            "issued-at",
            "output-path",
            "badge-label",
            "readme-path",
        ):
            self.assertIn(f"  {input_name}:\n", metadata)
            self.assertIn(f"INPUT_{input_name.upper().replace('-', '_')}: ${{{{ inputs.{input_name} }}}}", metadata)
        self.assertIn("    default: project-attestation.json\n", metadata)
        self.assertIn("    default: README.md\n", metadata)
        self.assertIn("  proof-url:\n", metadata)
        self.assertIn("  attestation-path:\n", metadata)
        self.assertIn("using: composite", metadata)
        self.assertIn('python3 "$GITHUB_ACTION_PATH/attest.py"', metadata)

    def test_example_workflow_uses_secret_without_git_mutation(self) -> None:
        workflow = EXAMPLE_WORKFLOW.read_text()
        self.assertIn("workflow_dispatch:", workflow)
        self.assertIn("secrets.ISSUER_TOKEN", workflow)
        self.assertIn("uses: ./.github/actions/project-attest", workflow)
        self.assertIn("proof-url", workflow)
        lowered = workflow.lower()
        self.assertNotIn("git add", lowered)
        self.assertNotIn("git commit", lowered)
        self.assertNotIn("git push", lowered)


if __name__ == "__main__":
    unittest.main(verbosity=2)
