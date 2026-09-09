from __future__ import annotations

from pathlib import Path

from fastapi.testclient import TestClient

from astrorder.config import Settings
from astrorder.main import create_app


def test_connection_history_api_returns_page_and_cursor(tmp_path: Path):
    app = create_app(
        Settings(
            database_url=f"sqlite:///{(tmp_path / 'state.sqlite3').as_posix()}",
            browser_secret="browser-test-secret",
            connector_secret="connector-test-secret",
            attachments_dir=tmp_path / "attachments",
            static_dir=tmp_path / "static",
            allowed_origins=("http://testserver",),
        )
    )
    with TestClient(app) as client:
        login = client.post(
            "/api/v1/auth/session",
            json={"token": "browser-test-secret"},
            headers={"Origin": "http://testserver"},
        )
        assert login.status_code == 200
        connection = app.state.store.save_ssh_connection(
            {"display_name": "remote", "profile_name": "default", "host": "127.0.0.1", "port": 22, "user": "tester"},
            state="saved",
            detail="saved",
        )
        for stage in ("deploy", "start", "handshake"):
            app.state.store.append_connection_history(connection["id"], stage, "completed", stage)

        response = client.get(f"/api/v1/connections/{connection['id']}/history?limit=2")
        assert response.status_code == 200, response.text
        body = response.json()
        assert len(body["items"]) == 2
        assert body["next_cursor"]
        assert body["items"][0]["connection_id"] == connection["id"]
