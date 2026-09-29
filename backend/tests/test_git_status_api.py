from fastapi.testclient import TestClient

from astrorder.config import Settings
from astrorder.main import create_app
from astrorder.routers import git as git_router


def test_git_status_exposes_file_and_line_counts_for_composer_and_diff(tmp_path, monkeypatch):
    app = create_app(Settings(
        database_url=f"sqlite:///{(tmp_path / 'test.sqlite3').as_posix()}",
        attachments_dir=tmp_path / "attachments", browser_secret="test-secret",
        connector_secret="connector-secret", auto_connect_local_hermes=False,
        session_daemon_enabled=False,
    ))

    def run_git(args, **_kwargs):
        if args[:2] == ["branch", "-a"]:
            return 0, "* feature/diff\n  master\n", ""
        if args[:2] == ["status", "--porcelain=v1"]:
            return 0, " M backend/a.py\nM  backend/b.py\n?? notes.txt\n", ""
        if args[:2] == ["diff", "--numstat"]:
            return 0, "3\t2\tbackend/a.py\n", ""
        if args[:3] == ["diff", "--cached", "--numstat"]:
            return 0, "5\t1\tbackend/b.py\n", ""
        return 0, "0\t0\n", ""

    monkeypatch.setattr(git_router, "_run_git", run_git)
    with TestClient(app) as client:
        response = client.get("/api/v1/git/status", params={"workspace": str(tmp_path)}, headers={"Authorization": "Bearer test-secret"})
    assert response.status_code == 200
    data = response.json()
    assert data["branch"] == "feature/diff"
    assert data["changed_files"] == 3
    assert data["insertions"] == 8
    assert data["deletions"] == 3
    assert [item["status"] for item in data["files"]] == ["modified", "staged", "untracked"]


def test_git_status_does_not_report_clean_when_workspace_is_not_a_repository(tmp_path, monkeypatch):
    app = create_app(Settings(
        database_url=f"sqlite:///{(tmp_path / 'test.sqlite3').as_posix()}",
        attachments_dir=tmp_path / "attachments", browser_secret="test-secret",
        connector_secret="connector-secret", auto_connect_local_hermes=False,
        session_daemon_enabled=False,
    ))
    monkeypatch.setattr(git_router, "_run_git", lambda args, **_kwargs: (128, "", "not a git repository"))
    with TestClient(app) as client:
        response = client.get("/api/v1/git/status", params={"workspace": str(tmp_path)}, headers={"Authorization": "Bearer test-secret"})
    assert response.status_code == 422
