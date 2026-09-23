from __future__ import annotations

from pathlib import Path
from unittest.mock import Mock
from astrorder.config import Settings
from astrorder.core.events import EventHub
from astrorder.service import ControlService
from astrorder.store import Store


def test_delete_session_closes_bound_browser_tab(tmp_path: Path, monkeypatch) -> None:
    settings = Settings(database_url=f"sqlite:///{tmp_path / 'session.sqlite3'}")
    store = Store(settings)
    store.upsert_session({
        "id": "sess-1",
        "agent_id": "agent-1",
        "title": "Session 1",
        "status": "idle",
        "updated_at": "2026-09-09T12:00:00Z",
    })
    closed = []
    monkeypatch.setattr("astrorder.jev.browser.close_browser_session", closed.append)

    assert ControlService(store, EventHub(), settings).delete_session("agent-1", "sess-1") is True
    assert closed == ["agent-1::sess-1"]


def test_delete_empty_project(tmp_path: Path) -> None:
    db_file = tmp_path / "test.sqlite3"
    settings = Settings(database_url=f"sqlite:///{db_file}")
    store = Store(settings)
    store.upsert_projects([
        {
            "source_id": "local-src",
            "project_id": "proj-empty",
            "project_name": "Empty Project",
            "session_count": 0,
        }
    ])
    assert len(store.list_projects()) == 1

    found, deleted_sessions = store.delete_project(
        project_key="project:local-src\0proj-empty",
        project_id="proj-empty",
        source_id="local-src",
    )
    assert found is True
    assert deleted_sessions == []
    assert len(store.list_projects()) == 0


def test_delete_project_cascades_sessions_and_messages(tmp_path: Path, monkeypatch) -> None:
    db_file = tmp_path / "test.sqlite3"
    settings = Settings(database_url=f"sqlite:///{db_file}")
    store = Store(settings)
    store.upsert_projects([
        {
            "source_id": "local-src",
            "project_id": "proj-a",
            "project_name": "Project A",
            "workspace": "/workspace/proj-a",
            "session_count": 1,
        }
    ])
    store.upsert_session({
        "id": "sess-1",
        "agent_id": "agent-1",
        "title": "Session 1",
        "workspace": "/workspace/proj-a",
        "project_id": "proj-a",
        "status": "idle",
        "updated_at": "2026-09-09T12:00:00Z",
    })
    store.upsert_message({
        "id": "msg-1",
        "agent_id": "agent-1",
        "session_id": "sess-1",
        "role": "user",
        "kind": "message",
        "text": "Hello",
        "created_at": "2026-09-09T12:00:00Z",
    })

    assert len(store.list_projects()) == 1
    assert len(store.list_sessions()) == 1
    assert len(store.list_messages("agent-1", "sess-1", None, 10)[0]) == 1

    hub = EventHub()
    queue = hub.subscribe()
    service = ControlService(store, hub, settings)
    closed = []
    monkeypatch.setattr("astrorder.jev.browser.close_browser_session", closed.append)

    res = service.delete_project(
        project_key="project:local-src\0proj-a",
        project_id="proj-a",
        source_id="local-src",
        workspace="/workspace/proj-a",
        delete_sessions=True,
    )
    assert res["ok"] is True
    assert len(res["deleted_sessions"]) == 1
    assert res["deleted_sessions"][0]["id"] == "sess-1"

    assert len(store.list_projects()) == 0
    assert len(store.list_sessions()) == 0
    assert len(store.list_messages("agent-1", "sess-1", None, 10)[0]) == 0
    assert closed == ["agent-1::sess-1"]

    events = []
    while not queue.empty():
        events.append(queue.get_nowait())

    event_types = [e["type"] for e in events]
    assert "project.delete" in event_types
    assert "session.delete" in event_types


def test_delete_project_api(tmp_path: Path) -> None:
    from fastapi.testclient import TestClient
    from astrorder.main import create_app

    db_file = tmp_path / "api_test.sqlite3"
    settings = Settings(
        database_url=f"sqlite:///{db_file}",
        browser_secret="test-secret",
        auto_connect_local_hermes=False,
    )
    app = create_app(settings)
    with TestClient(app) as client:
        store = client.app.state.store
        store.upsert_projects([
            {
                "source_id": "test-src",
                "project_id": "api-proj",
                "project_name": "API Project",
                "session_count": 0,
            }
        ])

        # 未授权请求返回 401
        res = client.post("/api/v1/projects/delete", json={"project_id": "api-proj"})
        assert res.status_code == 401

        # 授权请求
        headers = {"Authorization": "Bearer test-secret"}
        res = client.post(
            "/api/v1/projects/delete",
            json={"project_id": "api-proj", "source_id": "test-src"},
            headers=headers,
        )
        assert res.status_code == 200
        assert res.json()["ok"] is True
        assert len(store.list_projects()) == 0
