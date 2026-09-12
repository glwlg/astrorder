from __future__ import annotations

from fastapi.testclient import TestClient

from astrorder.config import Settings
from astrorder.daemon.bridge import DaemonBridge
from astrorder.main import create_app


def test_daemon_hermes_app_connection_registers_command_handler_without_stopping_daemon_on_lifespan_end(
    monkeypatch, tmp_path
):
    calls: list[tuple[str, dict[str, object]]] = []

    async def run_until_stopped(_bridge, stopping):
        await stopping.wait()

    async def request_control(_bridge, action, fields):
        calls.append((action, dict(fields)))
        assert action == "session.spawn"
        return {
            "result": {
                "status": "idle",
                "agent_id": "local-hermes-default",
                "source_id": "hermes-local-opaque-source",
                "profile_name": "default",
            }
        }

    monkeypatch.setattr(DaemonBridge, "run", run_until_stopped)
    monkeypatch.setattr(DaemonBridge, "request_control", request_control)
    app = create_app(
        Settings(
            database_url=f"sqlite:///{tmp_path}/daemon-hermes-api.sqlite3",
            attachments_dir=tmp_path / "attachments",
            browser_secret="browser-test",
            connector_secret="connector-test",
            auto_connect_local_hermes=False,
            session_daemon_enabled=True,
            daemon_hermes_enabled=True,
            session_daemon_endpoint="ws://127.0.0.1:30127",
            session_daemon_secret="test-only-daemon-secret",
        )
    )

    with TestClient(app):
        result = app.state.connections.connect_local(app.state.service)
        assert result["local"]["agent_id"] == "local-hermes-default"
        assert "local-hermes-default" in app.state.service._native_command_handlers
        assert "local-hermes-default" not in app.state.service._native_history_handlers
        app.state.store.upsert_agent(
            {
                "id": "local-hermes-default",
                "kind": "hermes",
                "name": "Daemon Hermes",
                "status": "ready",
                "capabilities": ["chat", "events"],
                "limitation": None,
                "source_id": "hermes-local-opaque-source",
            }
        )
        projected = app.state.connections.snapshot(app.state.service)
        assert projected["local"]["state"] == "connected"

    assert [action for action, _fields in calls] == ["session.spawn"]
