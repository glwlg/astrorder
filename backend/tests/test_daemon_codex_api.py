from types import SimpleNamespace

from fastapi.testclient import TestClient

from astrorder.config import Settings
from astrorder.main import create_app


def test_authenticated_session_create_persists_daemon_codex_native_identity(tmp_path):
    app = create_app(
        Settings(
            database_url=f"sqlite:///{tmp_path}/state.sqlite3",
            attachments_dir=tmp_path / "attachments",
            browser_secret="browser-test",
            connector_secret="connector-test",
            allowed_origins=("http://testserver",),
            auto_connect_local_hermes=False,
        )
    )
    calls: list[tuple[str | None, str | None, bool, str | None]] = []

    class DaemonCodexProjection:
        def create(self, workspace, title, *, ephemeral=False, parent_session_id=None):
            calls.append((workspace, title, ephemeral, parent_session_id))
            return {
                "id": "daemon-native-thread-1",
                "agent_id": "daemon-codex",
                "source_id": "daemon-codex",
                "source_session_id": "daemon-native-thread-1",
                "workspace": workspace,
                "title": title,
                "status": "idle",
                "updated_at": "2026-09-11T00:00:00Z",
                "history_state": "available",
                "control_state": "owned",
                "ephemeral": ephemeral,
            }

    with TestClient(app) as client:
        login = client.post(
            "/api/v1/auth/session",
            json={"token": "browser-test"},
            headers={"Origin": "http://testserver"},
        )
        assert login.status_code == 200
        codex = DaemonCodexProjection()
        app.state.environments = SimpleNamespace(
            for_agent=lambda agent_id: codex if agent_id == "daemon-codex" else None,
            shutdown=lambda: None,
        )
        response = client.post(
            "/api/v1/sessions",
            json={
                "agent_id": "daemon-codex",
                "workspace": "C:/allowed",
                "title": "Daemon-created",
                "project_id": "project-daemon",
                "project_name": "Daemon project",
                "ephemeral": True,
            },
            headers={"Origin": "http://testserver"},
        )
        assert response.status_code == 200, response.text
        created = response.json()
        assert created["id"] == "daemon-native-thread-1"
        assert created["agent_id"] == "daemon-codex"
        assert created["ephemeral"] is True
        assert created["project_id"] == "project-daemon"
        assert created["project_name"] == "Daemon project"
        assert app.state.store.get_session("daemon-codex", "daemon-native-thread-1")["id"] == "daemon-native-thread-1"
    assert calls == [("C:/allowed", "Daemon-created", True, None)]
