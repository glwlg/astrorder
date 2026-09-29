from pathlib import Path, PurePosixPath
from unittest.mock import patch, MagicMock
import json
import subprocess

from fastapi.testclient import TestClient

from astrorder.config import Settings
from astrorder.main import create_app
from astrorder.core.review_relay import capture_remote_workspace_snapshot, capture_workspace_snapshot


def test_review_relay_binding_is_persisted(tmp_path: Path) -> None:
    app = create_app(Settings(
        database_url=f"sqlite:///{(tmp_path / 'state.sqlite3').as_posix()}",
        browser_secret="test-secret",
        connector_secret="connector-secret",
        attachments_dir=tmp_path / "attachments",
        auto_connect_local_hermes=False,
    ))
    headers = {"Authorization": "Bearer test-secret"}
    path = "/api/v1/review-relay/bindings/agent-a/session-a"
    payload = {
        "review_agent_id": "agent-b",
        "review_session_id": "session-b",
        "workspace": "P:/workspace/project",
        "enabled": True,
    }
    with TestClient(app) as client:
        response = client.put(path, json=payload, headers=headers)
        assert response.status_code == 200
        assert response.json()["review_session_id"] == "session-b"
        assert client.get(path, headers=headers).json() == response.json()
        assert client.delete(path, headers=headers).status_code == 204
        assert client.get(path, headers=headers).json() is None


def test_review_relay_run_is_persisted(tmp_path: Path) -> None:
    app = create_app(Settings(
        database_url=f"sqlite:///{(tmp_path / 'state.sqlite3').as_posix()}",
        browser_secret="test-secret",
        connector_secret="connector-secret",
        attachments_dir=tmp_path / "attachments",
        auto_connect_local_hermes=False,
    ))
    headers = {"Authorization": "Bearer test-secret"}
    payload = {
        "source_agent_id": "agent-a",
        "source_session_id": "session-a",
        "review_agent_id": "agent-b",
        "review_session_id": "session-b",
        "command_id": "command-1",
        "baseline_ids": ["message-1"],
        "status": "reviewing",
    }
    with TestClient(app) as client:
        created = client.post("/api/v1/review-relay/runs", json=payload, headers=headers)
        assert created.status_code == 200
        run_id = created.json()["id"]
        assert client.get(f"/api/v1/review-relay/runs/{run_id}", headers=headers).json()["status"] == "reviewing"
        updated = client.patch(
            f"/api/v1/review-relay/runs/{run_id}",
            json={"status": "draft_ready", "comment_text": "::code-comment{{body=found}}"},
            headers=headers,
        )
        assert updated.status_code == 200
        assert updated.json()["comment_text"] == "::code-comment{{body=found}}"
        listed = client.get(
            "/api/v1/review-relay/runs?source_agent_id=agent-a&source_session_id=session-a&active_only=false",
            headers=headers,
        )
        assert listed.status_code == 200
        assert [item["id"] for item in listed.json()["items"]] == [run_id]


def test_review_relay_validation_rejects_different_git_snapshots(tmp_path: Path) -> None:
    source = tmp_path / "source"
    review = tmp_path / "review"
    subprocess.run(["git", "init", "-q", str(source)], check=True)
    subprocess.run(["git", "-C", str(source), "config", "user.email", "test@example.com"], check=True)
    subprocess.run(["git", "-C", str(source), "config", "user.name", "Test"], check=True)
    (source / "file.txt").write_text("one", encoding="utf-8")
    subprocess.run(["git", "-C", str(source), "add", "file.txt"], check=True)
    subprocess.run(["git", "-C", str(source), "commit", "-qm", "initial"], check=True)
    subprocess.run(["git", "clone", "-q", str(source), str(review)], check=True)
    (review / "file.txt").write_text("changed", encoding="utf-8")
    app = create_app(Settings(
        database_url=f"sqlite:///{(tmp_path / 'state.sqlite3').as_posix()}",
        browser_secret="test-secret",
        connector_secret="connector-secret",
        attachments_dir=tmp_path / "attachments",
        auto_connect_local_hermes=False,
    ))
    headers = {"Authorization": "Bearer test-secret"}
    with TestClient(app) as client:
        valid = client.post("/api/v1/review-relay/validate", json={
            "source_workspace": str(source), "review_workspace": str(source),
        }, headers=headers)
        assert valid.status_code == 200
        invalid = client.post("/api/v1/review-relay/validate", json={
            "source_workspace": str(source), "review_workspace": str(review),
        }, headers=headers)
        assert invalid.status_code == 409


def test_review_snapshot_detects_changed_content_without_status_change(tmp_path: Path) -> None:
    repo = tmp_path / "repo"
    subprocess.run(["git", "init", "-q", str(repo)], check=True)
    subprocess.run(["git", "-C", str(repo), "config", "user.email", "test@example.com"], check=True)
    subprocess.run(["git", "-C", str(repo), "config", "user.name", "Test"], check=True)
    (repo / "tracked.txt").write_text("initial", encoding="utf-8")
    subprocess.run(["git", "-C", str(repo), "add", "tracked.txt"], check=True)
    subprocess.run(["git", "-C", str(repo), "commit", "-qm", "initial"], check=True)
    (repo / "untracked.txt").write_text("one", encoding="utf-8")
    first = capture_workspace_snapshot(repo)
    (repo / "untracked.txt").write_text("two", encoding="utf-8")
    assert first.change_fingerprint != capture_workspace_snapshot(repo).change_fingerprint


def test_remote_snapshot_preserves_posix_path(monkeypatch) -> None:
    class Runtime:
        def __init__(self, *args, **kwargs):
            pass
        def _base_ssh_argv(self):
            return ["ssh", "-T"]
        def _target(self):
            return "remote"

    import astrorder.ssh_transport as transport
    monkeypatch.setattr(transport, "SshNativeRuntime", Runtime)
    response = {"workspace": "/home/luwei/workspace/OpsCore", "repo_root": "/home/luwei/workspace/OpsCore", "branch": "main", "head": "abc", "change_fingerprint": "xyz"}
    with patch("astrorder.core.review_relay.run_subprocess_hidden", return_value=subprocess.CompletedProcess([], 0, json.dumps(response), "")) as run:
        snapshot = capture_remote_workspace_snapshot(response["workspace"], {}, "wsl-ssh")
    assert snapshot.workspace == PurePosixPath(response["workspace"])
    assert str(snapshot.workspace) == response["workspace"]
    assert "BatchMode=yes" in run.call_args.args[0]


def test_validate_remote_sessions_never_resolves_wsl_path_locally(tmp_path: Path) -> None:
    app = create_app(Settings(
        database_url=f"sqlite:///{(tmp_path / 'state.sqlite3').as_posix()}",
        browser_secret="test-secret", connector_secret="connector-secret",
        attachments_dir=tmp_path / "attachments", auto_connect_local_hermes=False,
    ))
    workspace = "/home/luwei/workspace/OpsCore"
    with TestClient(app) as client:
        store = app.state.store
        with patch.object(store, "get_session", side_effect=lambda aid, sid: {"workspace": workspace, "connection_id": "wsl-ssh"}), \
             patch.object(store, "get_agent", return_value={"connection_id": "wsl-ssh"}), \
             patch.object(store, "get_ssh_connection", return_value={"settings": {}}), \
             patch("astrorder.routers.review_relay.capture_remote_workspace_snapshot") as remote, \
             patch("astrorder.routers.review_relay.capture_workspace_snapshot", side_effect=AssertionError("local path used")):
            remote.return_value = MagicMock(workspace=PurePosixPath(workspace), repo_root=PurePosixPath(workspace), branch="main", head="abc", change_fingerprint="xyz")
            result = client.post("/api/v1/review-relay/validate", json={
                "source_workspace": workspace, "review_workspace": workspace,
                "source_agent_id": "hermes", "source_session_id": "dev",
                "review_agent_id": "codex", "review_session_id": "review",
            }, headers={"Authorization": "Bearer test-secret"})
            assert result.status_code == 200
            assert result.json()["source"]["workspace"] == workspace
            assert remote.call_count == 2


