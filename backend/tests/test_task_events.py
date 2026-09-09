from __future__ import annotations

from pathlib import Path

from fastapi.testclient import TestClient

from astrorder.config import Settings
from astrorder.main import create_app


def _app(tmp_path: Path):
    return create_app(
        Settings(
            database_url=f"sqlite:///{(tmp_path / 'state.sqlite3').as_posix()}",
            browser_secret="browser-test-secret",
            connector_secret="connector-test-secret",
            attachments_dir=tmp_path / "attachments",
            static_dir=tmp_path / "static",
            allowed_origins=("http://testserver",),
        )
    )


def _login(client: TestClient) -> None:
    response = client.post(
        "/api/v1/auth/session",
        json={"token": "browser-test-secret"},
        headers={"Origin": "http://testserver"},
    )
    assert response.status_code == 200


def test_connector_task_event_is_durable_and_scoped_to_session(tmp_path: Path) -> None:
    app = _app(tmp_path)
    with TestClient(app) as client:
        _login(client)
        with client.websocket_connect(
            "/ws/v1/connector", headers={"Authorization": "Bearer connector-test-secret"}
        ) as connector:
            connector.send_json(
                {
                    "type": "hello",
                    "protocol_version": 1,
                    "agent": {
                        "id": "hermes-task-agent",
                        "kind": "hermes",
                        "name": "Task Hermes",
                        "status": "ready",
                        "capabilities": ["chat", "stop", "events"],
                        "limitation": None,
                    },
                }
            )
            assert connector.receive_json()["type"] == "event"
            connector.send_json(
                {
                    "type": "event",
                    "event": {
                        "id": "session-task-1",
                        "cursor": 0,
                        "type": "session.upsert",
                        "agent_id": "hermes-task-agent",
                        "session_id": "session-task",
                        "data": {
                            "id": "session-task",
                            "agent_id": "hermes-task-agent",
                            "title": "Task session",
                            "workspace": "/workspace/task",
                            "status": "running",
                            "updated_at": "2026-09-07T12:00:00Z",
                        },
                    },
                }
            )
            connector.send_json(
                {
                    "type": "event",
                    "event": {
                        "id": "task-event-1",
                        "cursor": 0,
                        "type": "task.upsert",
                        "agent_id": "hermes-task-agent",
                        "session_id": "session-task",
                        "data": {
                            "id": "task-1",
                            "agent_id": "hermes-task-agent",
                            "session_id": "session-task",
                            "kind": "background",
                            "title": "运行测试",
                            "status": "running",
                            "progress": {"completed": 2, "total": 5},
                            "command": "uv run pytest -q",
                            "logs": [
                                {
                                    "id": "log-1",
                                    "text": "正在运行测试",
                                    "level": "info",
                                    "created_at": "2026-09-07T12:00:01Z",
                                }
                            ],
                            "target_id": "task-target-1",
                            "created_at": "2026-09-07T12:00:00Z",
                            "updated_at": "2026-09-07T12:00:01Z",
                        },
                    },
                }
            )

            tasks = client.get(
                "/api/v1/sessions/session-task/tasks?agent_id=hermes-task-agent",
                headers={"Origin": "http://testserver"},
            )
            assert tasks.status_code == 200, tasks.text
            assert tasks.json()["items"][0]["id"] == "task-1"
            assert tasks.json()["items"][0]["progress"] == {"completed": 2, "total": 5}
            assert tasks.json()["items"][0]["logs"][0]["text"] == "正在运行测试"

        with TestClient(app) as restarted:
            _login(restarted)
            tasks = restarted.get(
                "/api/v1/sessions/session-task/tasks?agent_id=hermes-task-agent",
                headers={"Origin": "http://testserver"},
            )
            assert tasks.status_code == 200
            assert [item["id"] for item in tasks.json()["items"]] == ["task-1"]
