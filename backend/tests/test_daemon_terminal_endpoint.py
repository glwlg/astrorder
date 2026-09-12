from fastapi.testclient import TestClient

from astrorder.config import Settings
from astrorder.daemon.bridge import DaemonBridge
from astrorder.main import create_app


def test_local_terminal_websocket_delegates_to_daemon_relay(monkeypatch, tmp_path):
    async def run_until_stopped(_bridge, stopping):
        await stopping.wait()

    monkeypatch.setattr(DaemonBridge, "run", run_until_stopped)
    app = create_app(
        Settings(
            database_url=f"sqlite:///{tmp_path}/terminal.sqlite3",
            attachments_dir=tmp_path / "attachments",
            browser_secret="browser-test",
            connector_secret="connector-test",
            auto_connect_local_hermes=False,
            session_daemon_enabled=True,
            daemon_pty_enabled=True,
            session_daemon_endpoint="ws://127.0.0.1:30124",
            session_daemon_secret="test-only-daemon-secret",
        )
    )
    calls = []

    class Relay:
        async def serve(self, websocket, *, agent_id, session_id, workspace):
            calls.append((agent_id, session_id, workspace))
            await websocket.accept()
            await websocket.send_text("daemon relay selected")
            await websocket.close()

    with TestClient(app) as client:
        app.state.store.upsert_session(
            {
                "id": "native-local-session",
                "agent_id": "local-codex",
                "title": "Not used for identity",
                "workspace": str(tmp_path),
                "status": "idle",
                "updated_at": "2026-09-11T00:00:00Z",
                "connection_id": None,
            }
        )
        app.state.daemon_terminal_relay = Relay()
        with client.websocket_connect(
            "/ws/v1/terminal?session_id=native-local-session",
            headers={"Authorization": "Bearer browser-test"},
        ) as websocket:
            assert websocket.receive_text() == "daemon relay selected"

    assert calls == [("local-codex", "native-local-session", str(tmp_path))]
