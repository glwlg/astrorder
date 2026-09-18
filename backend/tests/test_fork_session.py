import subprocess
from pathlib import Path
from fastapi.testclient import TestClient

from astrorder.config import Settings
from astrorder.main import create_app

AUTH_HEADERS = {"Authorization": "Bearer browser-test"}


def test_fork_session_chat_branch(tmp_path):
    repo_dir = tmp_path / "repo"
    repo_dir.mkdir()

    app = create_app(
        Settings(
            database_url=f"sqlite:///{tmp_path / 'fork.sqlite3'}",
            attachments_dir=tmp_path / "attachments",
            browser_secret="browser-test",
            auto_connect_local_hermes=False,
            allowed_workspaces=[repo_dir],
        )
    )
    created_calls = []

    class MockCodex:
        def create(self, workspace, title, *, ephemeral=False, parent_session_id=None):
            created_calls.append({
                "workspace": workspace,
                "title": title,
                "parent_session_id": parent_session_id,
            })
            return {
                "id": "new-child-id",
                "agent_id": "local-codex",
                "title": title,
                "workspace": workspace,
                "status": "idle",
                "updated_at": "2026-09-17T00:00:00Z",
            }

    with TestClient(app) as client:
        app.state.environments.runtime_for_agent = lambda agent_id: MockCodex() if agent_id == "local-codex" else None

        app.state.store.upsert_agent({
            "id": "local-codex",
            "kind": "codex",
            "name": "Codex",
            "status": "ready",
            "capabilities": ["chat"],
        })
        app.state.store.upsert_session({
            "id": "parent-1",
            "agent_id": "local-codex",
            "title": "测试主会话",
            "workspace": str(repo_dir),
            "status": "idle",
            "updated_at": "2026-09-17T00:00:00Z",
        })

        resp = client.post(
            "/api/v1/sessions/parent-1/fork",
            json={
                "agent_id": "local-codex",
                "worktree": False,
            },
            headers=AUTH_HEADERS,
        )
        assert resp.status_code == 200, resp.text
        data = resp.json()
        assert data["id"] == "new-child-id"
        assert data["title"] == "测试主会话 (分支)"
        assert len(created_calls) == 1
        assert created_calls[0]["parent_session_id"] == "parent-1"
        assert created_calls[0]["workspace"] == str(repo_dir)


def test_fork_session_worktree_not_git(tmp_path):
    app = create_app(
        Settings(
            database_url=f"sqlite:///{tmp_path / 'fork.sqlite3'}",
            attachments_dir=tmp_path / "attachments",
            browser_secret="browser-test",
            auto_connect_local_hermes=False,
        )
    )
    not_git_dir = tmp_path / "not-git"
    not_git_dir.mkdir()

    with TestClient(app) as client:
        app.state.store.upsert_agent({
            "id": "local-codex",
            "kind": "codex",
            "name": "Codex",
            "status": "ready",
            "capabilities": ["chat"],
        })
        app.state.store.upsert_session({
            "id": "parent-1",
            "agent_id": "local-codex",
            "title": "非 Git 会话",
            "workspace": str(not_git_dir),
            "status": "idle",
            "updated_at": "2026-09-17T00:00:00Z",
        })

        resp = client.post(
            "/api/v1/sessions/parent-1/fork",
            json={
                "agent_id": "local-codex",
                "worktree": True,
            },
            headers=AUTH_HEADERS,
        )
        assert resp.status_code == 400
        assert "不是 Git 仓库" in resp.json()["detail"]


def test_fork_session_worktree_success(tmp_path):
    repo_dir = tmp_path / "my-repo"
    repo_dir.mkdir()
    subprocess.run(["git", "init", str(repo_dir)], check=True, capture_output=True)
    subprocess.run(["git", "-C", str(repo_dir), "config", "user.name", "TestUser"], check=True)
    subprocess.run(["git", "-C", str(repo_dir), "config", "user.email", "test@test.com"], check=True)
    (repo_dir / "README.md").write_text("hello", encoding="utf-8")
    subprocess.run(["git", "-C", str(repo_dir), "add", "."], check=True)
    subprocess.run(["git", "-C", str(repo_dir), "commit", "-m", "init"], check=True)

    app = create_app(
        Settings(
            database_url=f"sqlite:///{tmp_path / 'fork.sqlite3'}",
            attachments_dir=tmp_path / "attachments",
            browser_secret="browser-test",
            auto_connect_local_hermes=False,
        )
    )
    created_calls = []

    class MockCodex:
        def create(self, workspace, title, *, ephemeral=False, parent_session_id=None):
            created_calls.append({
                "workspace": workspace,
                "title": title,
                "parent_session_id": parent_session_id,
            })
            return {
                "id": "worktree-child-id",
                "agent_id": "local-codex",
                "title": title,
                "workspace": workspace,
                "status": "idle",
                "updated_at": "2026-09-17T00:00:00Z",
            }

    with TestClient(app) as client:
        app.state.environments.runtime_for_agent = lambda agent_id: MockCodex() if agent_id == "local-codex" else None

        app.state.store.upsert_agent({
            "id": "local-codex",
            "kind": "codex",
            "name": "Codex",
            "status": "ready",
            "capabilities": ["chat"],
        })
        app.state.store.upsert_session({
            "id": "parent-git",
            "agent_id": "local-codex",
            "title": "Git 会话",
            "workspace": str(repo_dir),
            "status": "idle",
            "updated_at": "2026-09-17T00:00:00Z",
        })

        resp = client.post(
            "/api/v1/sessions/parent-git/fork",
            json={
                "agent_id": "local-codex",
                "worktree": True,
                "branch_name": "feat-branch-1",
            },
            headers=AUTH_HEADERS,
        )
        assert resp.status_code == 200, resp.text
        data = resp.json()
        assert data["id"] == "worktree-child-id"
        assert "feat-branch-1" in data["title"]
        assert len(created_calls) == 1
        new_ws = created_calls[0]["workspace"]
        assert Path(new_ws).exists()
        assert (Path(new_ws) / "README.md").exists()
