#!/usr/bin/env python3
"""Issue an attestation and update its repository proof badge."""

from __future__ import annotations

import html
import json
import os
import re
import sys
import tempfile
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode, urlsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener

START = "<!-- skill-attestation:start -->"
END = "<!-- skill-attestation:end -->"
MAX_RESPONSE = 4 * 1024 * 1024


class ActionError(Exception):
    pass


class NoRedirects(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):  # noqa: ANN001
        return None


def required(name: str) -> str:
    value = os.environ.get(name, "")
    if not value.strip():
        raise ActionError(f"required input {name.removeprefix('INPUT_').lower().replace('_', '-')} is empty")
    return value


def workspace_path(name: str, default: str, workspace: Path) -> Path:
    raw = os.environ.get(name, default)
    if not raw or any(ord(char) < 32 for char in raw):
        raise ActionError(f"{name} is not a valid path")
    path = Path(raw)
    if not path.is_absolute():
        path = workspace / path
    path = path.resolve()
    try:
        path.relative_to(workspace)
    except ValueError as exc:
        raise ActionError(f"{name} must resolve inside GITHUB_WORKSPACE") from exc
    return path


def endpoint(base: str) -> str:
    parsed = urlsplit(base)
    if parsed.scheme not in {"http", "https"} or not parsed.netloc or parsed.query or parsed.fragment:
        raise ActionError("issuer-url must be an absolute HTTP(S) base URL without query or fragment")
    return base.rstrip("/") + "/v1/attestations"


def issue(url: str, token: str, payload: dict[str, object]) -> bytes:
    request = Request(
        url,
        data=json.dumps(payload, separators=(",", ":"), ensure_ascii=False).encode(),
        headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json", "Accept": "application/json"},
        method="POST",
    )
    try:
        with build_opener(NoRedirects()).open(request, timeout=30) as response:
            body = response.read(MAX_RESPONSE + 1)
            status = response.status
    except HTTPError as exc:
        exc.read(MAX_RESPONSE)
        raise ActionError(f"issuer returned HTTP {exc.code}") from exc
    except URLError as exc:
        raise ActionError(f"issuer request failed: {exc.reason}") from exc
    if not 200 <= status < 300:
        raise ActionError(f"issuer returned HTTP {status}")
    if len(body) > MAX_RESPONSE:
        raise ActionError("issuer response exceeds 4 MiB")
    return body


def proof_url(body: bytes) -> str:
    try:
        document = json.loads(body)
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ActionError("issuer response is not valid JSON") from exc
    if not isinstance(document, dict):
        raise ActionError("issuer response must be a JSON object")
    value = document.get("proof_url")
    if not isinstance(value, str) or any(ord(char) < 32 for char in value):
        raise ActionError("issuer response requires a valid proof_url")
    parsed = urlsplit(value)
    if parsed.scheme not in {"http", "https"} or not parsed.netloc:
        raise ActionError("issuer response requires an absolute HTTP(S) proof_url")
    return value


def badge_block(label: str, score: int, proof: str) -> str:
    label = " ".join(label.split())
    if not label:
        raise ActionError("badge-label and project-name cannot both be empty")
    message = f"{label} | Score {score}/100 | Execution PASS"
    badge = "https://img.shields.io/static/v1?" + urlencode(
        {"label": "Verified skill", "message": message, "color": "brightgreen"}
    )
    alt = f"Verified skill: {label}; Score {score}/100; Execution PASS"
    return "\n".join(
        [
            START,
            f'<a href="{html.escape(proof, quote=True)}"><img alt="{html.escape(alt, quote=True)}" src="{html.escape(badge, quote=True)}"></a>',
            END,
        ]
    )


def update_readme(current: str, block: str) -> str:
    starts, ends = current.count(START), current.count(END)
    if starts == ends == 0:
        separator = "" if current.endswith("\n\n") else "\n" if current.endswith("\n") else "\n\n"
        return current + separator + block + "\n"
    if starts != 1 or ends != 1:
        raise ActionError("README must contain zero or one complete skill attestation badge block")
    start = current.index(START)
    end = current.index(END, start) + len(END)
    return current[:start] + block + current[end:]


def atomic_write(path: Path, data: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    mode = path.stat().st_mode & 0o777 if path.exists() else 0o644
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(dir=path.parent, prefix=f".{path.name}.", delete=False) as handle:
            temporary = Path(handle.name)
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
        os.chmod(temporary, mode)
        os.replace(temporary, path)
        temporary = None
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


def run() -> None:
    workspace = Path(os.environ.get("GITHUB_WORKSPACE", os.getcwd())).resolve()
    issuer_url = required("INPUT_ISSUER_URL")
    token = required("INPUT_ISSUER_TOKEN")
    learner = required("INPUT_LEARNER_ID")
    project = required("INPUT_PROJECT_NAME")
    score_text = required("INPUT_EVALUATION_SCORE")
    if not re.fullmatch(r"(?:0|[1-9][0-9]?|100)", score_text):
        raise ActionError("evaluation-score must be an integer from 0 through 100")
    score = int(score_text)
    output = workspace_path("INPUT_OUTPUT_PATH", "project-attestation.json", workspace)
    readme = workspace_path("INPUT_README_PATH", "README.md", workspace)
    if output == readme:
        raise ActionError("output-path and readme-path must be different")
    issued_at = os.environ.get("INPUT_ISSUED_AT", "")
    payload: dict[str, object] = {"learner_id": learner, "project_name": project, "evaluation_score": score}
    if issued_at:
        payload["issued_at"] = issued_at

    try:
        current_readme = readme.read_text(encoding="utf-8")
    except (OSError, UnicodeError) as exc:
        raise ActionError(f"cannot read UTF-8 README at {readme}") from exc
    body = issue(endpoint(issuer_url), token, payload)
    proof = proof_url(body)
    label = os.environ.get("INPUT_BADGE_LABEL", "") or project
    changed_readme = update_readme(current_readme, badge_block(label, score, proof))

    atomic_write(output, body)
    atomic_write(readme, changed_readme.encode())

    github_output = os.environ.get("GITHUB_OUTPUT")
    if github_output:
        with open(github_output, "a", encoding="utf-8") as handle:
            handle.write(f"proof-url={proof}\nattestation-path={output}\n")


if __name__ == "__main__":
    try:
        run()
    except ActionError as error:
        print(f"project-attest: {error}", file=sys.stderr)
        raise SystemExit(1)
