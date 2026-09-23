from __future__ import annotations

import json
from pathlib import Path
from types import SimpleNamespace

import pytest
from fastapi.testclient import TestClient
from starlette.websockets import WebSocketDisconnect

from astrorder import main
from astrorder.config import Settings
from astrorder.daemon.bridge import DaemonBridge
from astrorder.main import create_app
from astrorder.service import ControlService


@pytest.fixture
def configured(tmp_path: Path):
    def factory(**overrides):
        values = {
            "database_url": f"sqlite:///{(tmp_path / 'state.sqlite3').as_posix()}",
            "browser_secret": "browser-test-secret",
            "connector_secret": "connector-test-secret",
            "attachments_dir": tmp_path / "attachments",
            "static_dir": tmp_path / "static",
            "allowed_origins": ("http://testserver", "http://localhost:30002"),
            "event_retention": 3,
            "auto_connect_local_hermes": False,
        }
        values.update(overrides)
        return create_app(Settings(**values))

    return factory


def login(client: TestClient, token: str = "browser-test-secret"):
    response = client.post(
        "/api/v1/auth/session",
        json={"token": token},
        headers={"Origin": "http://testserver"},
    )
    assert response.status_code == 200, response.text


def hello_agent(ws, *, agent_id: str = "hermes-1", capabilities=None):
    ws.send_json(
        {
            "type": "hello",
            "protocol_version": 1,
            "agent": {
                "id": agent_id,
                "kind": "hermes",
                "name": "Hermes test",
                "status": "ready",
                "capabilities": capabilities or ["chat", "queue", "history", "events", "attachments"],
                "limitation": None,
            },
        }
    )
    frame = ws.receive_json()
    assert frame["type"] == "event"
    assert frame["event"]["type"] == "agent.upsert"


def add_session(ws, session_id: str = "session-1", agent_id: str = "hermes-1"):
    ws.send_json(
        {
            "type": "event",
            "event": {
                "id": f"session-{session_id}",
                "cursor": 0,
                "type": "session.upsert",
                "agent_id": agent_id,
                "session_id": session_id,
                "data": {
                    "id": session_id,
                    "agent_id": agent_id,
                    "title": "Test session",
                    "workspace": None,
                    "status": "idle",
                    "updated_at": "2026-09-06T00:00:00Z",
                },
            },
        }
    )


def command_payload(command_id: str = "command-1", text: str = "hello"):
    return {
        "id": command_id,
        "agent_id": "hermes-1",
        "session_id": "session-1",
        "action": "send",
        "text": text,
        "attachment_ids": [],
        "target_id": None,
    }


def test_private_state_fails_closed_without_configured_secret(configured):
    app = configured(browser_secret=None, connector_secret=None)
    with TestClient(app) as client:
        assert client.get("/health").json()["protocol_version"] == 1
        response = client.get("/api/v1/bootstrap")
        assert response.status_code in (401, 403, 503)
        assert "traceback" not in response.text.lower()


def test_desktop_shutdown_route_precedes_static_mount(configured):
    app = configured()
    calls = []
    app.state.desktop_shutdown = lambda: calls.append(True)
    with TestClient(app, client=("127.0.0.1", 12345)) as client:
        response = client.post(
            "/_desktop/shutdown",
            headers={"Authorization": "Bearer browser-test-secret"},
        )
    assert response.json() == {"stopping": True}
    assert calls == [True]


def test_daemon_restart_route_requires_confirmation_and_runs_in_background(configured, monkeypatch):
    async def restart(app, operation_id):
        app.state.daemon_restart_operations[operation_id].update(
            status="success", state="running", pid=123,
        )

    monkeypatch.setattr(main, "_restart_daemon", restart)
    with TestClient(configured()) as client:
        login(client)
        assert client.post("/api/v1/services/daemon/restart", json={}).status_code == 422
        started = client.post("/api/v1/services/daemon/restart", json={"confirm_active": True})
        assert started.status_code == 202
        operation_id = started.json()["id"]
        for _ in range(20):
            status = client.get(f"/api/v1/services/daemon/restart/{operation_id}")
            if status.json()["status"] == "success":
                break
        assert status.json() == {
            "id": operation_id, "status": "success", "state": "running", "pid": 123,
        }


@pytest.mark.asyncio
async def test_daemon_restart_worker_launches_independent_service_command(monkeypatch):
    calls = []

    class Process:
        returncode = 0

        async def communicate(self):
            return b'{"ok":true,"state":"running","pid":456}\n', None

    async def create_subprocess(*args, **kwargs):
        calls.append((args, kwargs))
        return Process()

    monkeypatch.setattr(main.asyncio, "create_subprocess_exec", create_subprocess)
    app = SimpleNamespace(state=SimpleNamespace(
        daemon_restart_operations={"restart-1": {"status": "queued"}},
    ))
    await main._restart_daemon(app, "restart-1")

    assert calls[0][0][-3:] == ("daemon", "restart", "--confirm-active")
    assert calls[0][1]["creationflags"]
    assert calls[0][1]["env"]["PYTHONIOENCODING"] == "utf-8"
    assert app.state.daemon_restart_operations["restart-1"] == {
        "status": "success", "state": "running", "pid": 456,
    }


def test_daemon_projection_starts_after_stale_connectors_are_disconnected(configured, monkeypatch):
    order = []
    original_mark = ControlService.mark_persisted_connectors_disconnected

    def mark_disconnected(self):
        order.append("marked")
        return original_mark(self)

    async def run_bridge(_self, stopping):
        order.append("bridge")
        await stopping.wait()

    monkeypatch.setattr(ControlService, "mark_persisted_connectors_disconnected", mark_disconnected)
    monkeypatch.setattr(DaemonBridge, "run", run_bridge)
    app = configured(
        session_daemon_enabled=True,
        session_daemon_secret="daemon-test-secret",
        session_daemon_endpoint="ws://127.0.0.1:30009",
    )

    with TestClient(app):
        assert order == ["marked", "bridge"]


def test_browser_auth_origin_and_safe_error(configured):
    app = configured()
    with TestClient(app) as client:
        assert client.post("/api/v1/auth/session", json={"token": "wrong"}).status_code == 401
        assert (
            client.post(
                "/api/v1/auth/session",
                json={"token": "browser-test-secret"},
                headers={"Origin": "https://evil.example"},
            ).status_code
            == 403
        )
        login(client)
        response = client.get("/api/v1/bootstrap")
        assert response.status_code == 200
        assert response.json()["agents"] == []


def test_bootstrap_does_not_scan_presence(configured, monkeypatch):
    import astrorder.api as api_mod

    calls: list[str] = []

    def fake_presence(request):
        calls.append("presence")
        return {"items": [], "open": [], "live": []}

    monkeypatch.setattr(api_mod, "_collect_presence", fake_presence)
    app = configured()
    with TestClient(app) as client:
        login(client)
        assert client.get("/api/v1/bootstrap").status_code == 200
        assert calls == []
        response = client.get("/api/v1/presence")
        assert response.status_code == 200
        assert response.json() == {"items": [], "open": [], "live": []}
        assert calls == ["presence"]


def test_connector_role_is_separate_and_origin_checked(configured):
    app = configured()
    with TestClient(app) as client:
        with pytest.raises(WebSocketDisconnect), client.websocket_connect(
            "/ws/v1/connector",
            headers={"Authorization": "Bearer browser-test-secret"},
        ):
            pass
        with pytest.raises(WebSocketDisconnect), client.websocket_connect(
            "/ws/v1/connector",
            headers={
                "Authorization": "Bearer connector-test-secret",
                "Origin": "https://evil.example",
            },
        ):
            pass
        with client.websocket_connect(
            "/ws/v1/connector", headers={"Authorization": "Bearer connector-test-secret"}
        ) as ws:
            hello_agent(ws)


def test_duplicate_id_payload_mismatch_and_canonical_outbox(configured):
    app = configured()
    with TestClient(app) as client:
        login(client)
        with client.websocket_connect(
            "/ws/v1/connector", headers={"Authorization": "Bearer connector-test-secret"}
        ) as connector:
            hello_agent(connector)
            add_session(connector)
            first = client.post(
                "/api/v1/commands", json=command_payload(), headers={"Origin": "http://testserver"}
            )
            assert first.status_code in (200, 201)
            command = first.json()
            assert command["id"] == "command-1"
            assert command["text"] == "hello"
            assert command["attachments"] == []
            delivered = connector.receive_json()
            assert delivered["type"] == "command"
            assert delivered["command"]["id"] == "command-1"

            duplicate = client.post(
                "/api/v1/commands", json=command_payload(), headers={"Origin": "http://testserver"}
            )
            assert duplicate.status_code == 200
            assert duplicate.json()["id"] == "command-1"

            mismatch = client.post(
                "/api/v1/commands",
                json=command_payload(text="different"),
                headers={"Origin": "http://testserver"},
            )
            assert mismatch.status_code == 409

            connector.send_json(
                {
                    "type": "event",
                    "event": {
                        "id": "command-ack-1",
                        "cursor": 0,
                        "type": "command.upsert",
                        "agent_id": "hermes-1",
                        "session_id": "session-1",
                        "data": {
                            **command,
                            "state": "completed",
                            "error": None,
                        },
                    },
                }
            )
            connector.send_json(
                {
                    "type": "event",
                    "event": {
                        "id": "message-1",
                        "cursor": 0,
                        "type": "message.upsert",
                        "agent_id": "hermes-1",
                        "session_id": "session-1",
                        "data": {
                            "id": "message-1",
                            "session_id": "session-1",
                            "agent_id": "hermes-1",
                            "role": "assistant",
                            "kind": "message",
                            "text": "hello back",
                            "attachments": [],
                            "created_at": "2026-09-06T00:00:01Z",
                            "command_id": "command-1",
                            "tool": None,
                        },
                    },
                }
            )
            commands = client.get(
                "/api/v1/sessions/session-1/commands?agent_id=hermes-1"
            ).json()["items"]
            messages = client.get(
                "/api/v1/sessions/session-1/messages?agent_id=hermes-1"
            ).json()["items"]
            assert [item["id"] for item in commands] == ["command-1"]
            assert commands[0]["state"] == "completed"
            assert [item["id"] for item in messages] == ["message-1"]
            assert messages[0]["command_id"] == "command-1"


def test_same_payload_text_does_not_merge_distinct_commands(configured):
    app = configured()
    with TestClient(app) as client:
        login(client)
        with client.websocket_connect(
            "/ws/v1/connector", headers={"Authorization": "Bearer connector-test-secret"}
        ) as connector:
            hello_agent(connector)
            add_session(connector)
            for command_id in ("command-1", "command-2"):
                response = client.post(
                    "/api/v1/commands",
                    json=command_payload(command_id=command_id),
                    headers={"Origin": "http://testserver"},
                )
                assert response.status_code in (200, 201)
                assert connector.receive_json()["command"]["id"] == command_id
            items = client.get(
                "/api/v1/sessions/session-1/commands?agent_id=hermes-1"
            ).json()["items"]
            assert [item["id"] for item in items] == ["command-1", "command-2"]


def test_same_message_text_and_attachments_keep_distinct_ids(configured):
    app = configured()
    with TestClient(app) as client:
        login(client)
        with client.websocket_connect(
            "/ws/v1/connector", headers={"Authorization": "Bearer connector-test-secret"}
        ) as connector:
            hello_agent(connector)
            add_session(connector)
            for message_id in ("message-a", "message-b"):
                connector.send_json(
                    {
                        "type": "event",
                        "event": {
                            "id": f"event-{message_id}",
                            "cursor": 0,
                            "type": "message.upsert",
                            "agent_id": "hermes-1",
                            "session_id": "session-1",
                            "data": {
                                "id": message_id,
                                "session_id": "session-1",
                                "agent_id": "hermes-1",
                                "role": "assistant",
                                "kind": "message",
                                "text": "same",
                                "attachments": [],
                                "created_at": "2026-09-06T00:00:00Z",
                                "command_id": None,
                                "tool": None,
                            },
                        },
                    }
                )
            messages = client.get(
                "/api/v1/sessions/session-1/messages?agent_id=hermes-1"
            ).json()["items"]
            assert [item["id"] for item in messages] == ["message-a", "message-b"]


def test_unknown_late_command_ack_is_rejected_without_creating_outbox(configured):
    app = configured()
    with TestClient(app) as client, client.websocket_connect(
        "/ws/v1/connector", headers={"Authorization": "Bearer connector-test-secret"}
    ) as connector:
        hello_agent(connector)
        add_session(connector)
        connector.send_json(
            {
                "type": "event",
                "event": {
                    "id": "late-ack",
                    "cursor": 0,
                    "type": "command.upsert",
                    "agent_id": "hermes-1",
                    "session_id": "session-1",
                    "data": {
                        "id": "never-submitted",
                        "agent_id": "hermes-1",
                        "session_id": "session-1",
                        "state": "completed",
                        "error": None,
                    },
                },
            }
        )
        error = connector.receive_json()
        assert error["type"] == "error"
        assert "unknown command" in error["detail"].lower()
        assert client.get("/api/v1/bootstrap").status_code in (401, 503)


def test_unsupported_attempt_is_preserved(configured):
    app = configured()
    with TestClient(app) as client:
        login(client)
        with client.websocket_connect(
            "/ws/v1/connector", headers={"Authorization": "Bearer connector-test-secret"}
        ) as connector:
            hello_agent(connector, capabilities=["history", "events"])
            add_session(connector)
            response = client.post(
                "/api/v1/commands",
                json=command_payload(),
                headers={"Origin": "http://testserver"},
            )
            assert response.status_code == 409
            assert response.json()["detail"]
            commands = client.get(
                "/api/v1/sessions/session-1/commands?agent_id=hermes-1"
            ).json()["items"]
            assert len(commands) == 1
            assert commands[0]["state"] == "failed"
            assert "unsupported" in commands[0]["error"].lower()


def test_attachment_is_bounded_and_command_keeps_attachment(configured, tmp_path):
    app = configured(max_attachment_size=4)
    with TestClient(app) as client:
        login(client)
        unsafe = client.post(
            "/api/v1/attachments",
            files={"file": ("..\\escape.txt", b"ok", "text/plain")},
            headers={"Origin": "http://testserver"},
        )
        assert unsafe.status_code == 400
        oversized = client.post(
            "/api/v1/attachments",
            files={"file": ("note.txt", b"12345", "text/plain")},
            headers={"Origin": "http://testserver"},
        )
        assert oversized.status_code == 413
        uploaded = client.post(
            "/api/v1/attachments",
            files={"file": ("note.txt", b"1234", "text/plain")},
            headers={"Origin": "http://testserver"},
        )
        assert uploaded.status_code == 201
        attachment = uploaded.json()
        assert attachment["url"].startswith("/api/v1/attachments/")
        source = tmp_path / "note.txt"
        source.write_bytes(b"1234")
        path_upload = client.post(
            "/api/v1/attachments",
            files={"file": ("note.txt", b"1234", "text/plain")},
            data={"source_path": str(source)},
            headers={"Origin": "http://testserver"},
        )
        assert path_upload.status_code == 201
        assert app.state.store.get_attachment(path_upload.json()["id"])["source_path"] == str(source)

        with client.websocket_connect(
            "/ws/v1/connector", headers={"Authorization": "Bearer connector-test-secret"}
        ) as connector:
            hello_agent(connector)
            add_session(connector)
            payload = command_payload()
            payload["attachment_ids"] = [attachment["id"]]
            result = client.post(
                "/api/v1/commands", json=payload, headers={"Origin": "http://testserver"}
            )
            assert result.status_code in (200, 201)
            assert result.json()["attachments"][0]["id"] == attachment["id"]
            assert connector.receive_json()["command"]["attachments"][0]["id"] == attachment["id"]


def test_old_cursor_requires_snapshot_and_multiple_clients_receive_live_event(configured):
    app = configured(event_retention=2)
    with TestClient(app) as client:
        login(client)
        with client.websocket_connect(
            "/ws/v1/connector", headers={"Authorization": "Bearer connector-test-secret"}
        ) as connector:
            hello_agent(connector)
            add_session(connector)
            for index in range(3):
                connector.send_json(
                    {
                        "type": "event",
                        "event": {
                            "id": f"message-{index}",
                            "cursor": 0,
                            "type": "message.upsert",
                            "agent_id": "hermes-1",
                            "session_id": "session-1",
                            "data": {
                                "id": f"message-{index}",
                                "session_id": "session-1",
                                "agent_id": "hermes-1",
                                "role": "assistant",
                                "kind": "message",
                                "text": str(index),
                                "attachments": [],
                                "created_at": f"2026-09-06T00:00:0{index}Z",
                                "command_id": None,
                                "tool": None,
                            },
                        },
                    }
                )
        with client.websocket_connect(
            "/ws/v1/events?after=0", headers={"Origin": "http://testserver"}
        ) as first, client.websocket_connect(
            "/ws/v1/events?after=999999", headers={"Origin": "http://testserver"}
        ) as second:
            assert first.receive_json()["type"] == "resync_required"
            assert second.receive_json()["type"] == "resync_required"


def test_restart_persists_transcript_and_outbox(configured):
    app = configured()
    with TestClient(app) as client:
        login(client)
        with client.websocket_connect(
            "/ws/v1/connector", headers={"Authorization": "Bearer connector-test-secret"}
        ) as connector:
            hello_agent(connector)
            add_session(connector)
            response = client.post(
                "/api/v1/commands",
                json=command_payload(),
                headers={"Origin": "http://testserver"},
            )
            assert response.status_code in (200, 201)
            connector.receive_json()
    restarted = configured()
    with TestClient(restarted) as client:
        login(client)
        bootstrap = client.get("/api/v1/bootstrap").json()
        assert [agent["id"] for agent in bootstrap["agents"]] == ["hermes-1"]
        commands = client.get(
            "/api/v1/sessions/session-1/commands?agent_id=hermes-1"
        ).json()["items"]
        assert commands[0]["id"] == "command-1"


def test_disconnect_marks_sent_command_unknown_without_retry(configured):
    app = configured()
    with TestClient(app) as client:
        login(client)
        connector = client.websocket_connect(
            "/ws/v1/connector", headers={"Authorization": "Bearer connector-test-secret"}
        )
        ws = connector.__enter__()
        hello_agent(ws)
        add_session(ws)
        response = client.post(
            "/api/v1/commands", json=command_payload(), headers={"Origin": "http://testserver"}
        )
        assert response.status_code in (200, 201)
        assert ws.receive_json()["command"]["id"] == "command-1"
        connector.__exit__(None, None, None)
        commands = client.get(
            "/api/v1/sessions/session-1/commands?agent_id=hermes-1"
        ).json()["items"]
        assert commands[0]["state"] == "unknown"
        assert "retry" not in json.dumps(commands[0]).lower()


def test_connector_write_failure_returns_the_persisted_unknown_command(configured, monkeypatch):
    app = configured()
    with TestClient(app) as client:
        login(client)
        with client.websocket_connect(
            "/ws/v1/connector", headers={"Authorization": "Bearer connector-test-secret"}
        ) as connector:
            hello_agent(connector)
            add_session(connector)
            connection = app.state.service.connections["hermes-1"]

            async def fail_send(_payload):
                raise OSError("isolated connector write failure")

            monkeypatch.setattr(connection, "send", fail_send)
            response = client.post(
                "/api/v1/commands", json=command_payload(), headers={"Origin": "http://testserver"}
            )
            assert response.status_code == 200
            assert response.json()["state"] == "unknown"
            assert "unknown" in response.json()["error"].lower()


def test_enqueue_is_durable_queue_acceptance_not_submission(configured):
    app = configured()
    with TestClient(app) as client:
        login(client)
        with client.websocket_connect(
            "/ws/v1/connector", headers={"Authorization": "Bearer connector-test-secret"}
        ) as connector:
            hello_agent(connector)
            add_session(connector)
            payload = command_payload()
            payload["id"] = "queued-1"
            payload["action"] = "enqueue"
            response = client.post(
                "/api/v1/commands", json=payload, headers={"Origin": "http://testserver"}
            )
            assert response.status_code == 200
            assert response.json()["state"] == "queued"
            assert connector.receive_json()["command"]["id"] == "queued-1"
            stored = client.get(
                "/api/v1/sessions/session-1/commands?agent_id=hermes-1"
            ).json()["items"]
            assert stored[0]["state"] == "queued"


def test_multiple_browser_clients_receive_one_durable_event(configured):
    app = configured()
    with TestClient(app) as client:
        login(client)
        with client.websocket_connect(
            "/ws/v1/connector", headers={"Authorization": "Bearer connector-test-secret"}
        ) as connector:
            hello_agent(connector)
            add_session(connector)
            cursor = client.get("/api/v1/bootstrap").json()["cursor"]
            with client.websocket_connect(
                f"/ws/v1/events?after={cursor}", headers={"Origin": "http://testserver"}
            ) as first, client.websocket_connect(
                f"/ws/v1/events?after={cursor}", headers={"Origin": "http://testserver"}
            ) as second:
                connector.send_json(
                    {
                        "type": "event",
                        "event": {
                            "id": "live-message-1",
                            "cursor": 0,
                            "type": "message.upsert",
                            "agent_id": "hermes-1",
                            "session_id": "session-1",
                            "data": {
                                "id": "live-message-1",
                                "session_id": "session-1",
                                "agent_id": "hermes-1",
                                "role": "assistant",
                                "kind": "message",
                                "text": "live",
                                "attachments": [],
                                "created_at": "2026-09-06T00:00:00Z",
                                "command_id": None,
                                "tool": None,
                            },
                        },
                    }
                )
                first_event = first.receive_json()
                second_event = second.receive_json()
                assert first_event["id"] == second_event["id"] == "live-message-1"
                assert first_event["type"] == "message.upsert"


def test_history_cursor_is_opaque_and_pages_chronologically(configured):
    app = configured()
    with TestClient(app) as client:
        login(client)
        with client.websocket_connect(
            "/ws/v1/connector", headers={"Authorization": "Bearer connector-test-secret"}
        ) as connector:
            hello_agent(connector)
            add_session(connector)
            for index in range(3):
                connector.send_json(
                    {
                        "type": "event",
                        "event": {
                            "id": f"history-{index}",
                            "cursor": 0,
                            "type": "message.upsert",
                            "agent_id": "hermes-1",
                            "session_id": "session-1",
                            "data": {
                                "id": f"history-{index}",
                                "session_id": "session-1",
                                "agent_id": "hermes-1",
                                "role": "assistant",
                                "kind": "message",
                                "text": str(index),
                                "attachments": [],
                                "created_at": f"2026-09-06T00:00:0{index}Z",
                                "command_id": None,
                                "tool": None,
                            },
                        },
                    }
                )
            # WebSocket send queues a frame; wait until all prior events are applied.
            connector.send_json({"type": "ping"})
            assert connector.receive_json() == {"type": "pong"}
            first = client.get(
                "/api/v1/sessions/session-1/messages?agent_id=hermes-1&limit=2"
            ).json()
            assert [item["id"] for item in first["items"]] == ["history-1", "history-2"]
            assert first["next_cursor"]
            assert first["next_cursor"] not in {"1", "2", "3"}
            older = client.get(
                "/api/v1/sessions/session-1/messages?agent_id=hermes-1&limit=2&before="
                + first["next_cursor"]
            ).json()
            assert [item["id"] for item in older["items"]] == ["history-0"]


def test_shutdown_marks_accepted_submission_unknown(configured):
    app = configured()
    with TestClient(app) as client:
        login(client)
        with client.websocket_connect(
            "/ws/v1/connector", headers={"Authorization": "Bearer connector-test-secret"}
        ) as connector:
            hello_agent(connector)
            add_session(connector)
            response = client.post(
                "/api/v1/commands", json=command_payload(), headers={"Origin": "http://testserver"}
            )
            assert response.status_code == 200
            assert connector.receive_json()["command"]["id"] == "command-1"
            client.portal.call(app.state.service.shutdown)
            command = client.get(
                "/api/v1/sessions/session-1/commands?agent_id=hermes-1"
            ).json()["items"][0]
            assert command["state"] == "unknown"
            assert "shutdown" in command["error"].lower()


def test_static_spa_serves_client_routes_without_shadowing_api(configured, tmp_path: Path):
    static_dir = tmp_path / "static"
    assets_dir = static_dir / "assets"
    assets_dir.mkdir(parents=True)
    (static_dir / "index.html").write_text("<main>星序 SPA</main>", encoding="utf-8")
    (assets_dir / "app.js").write_text("console.log('asset')", encoding="utf-8")
    app = configured(static_dir=static_dir)

    with TestClient(app) as client:
        for route in ("/", "/chat", "/chat/session-1", "/monitor", "/agents"):
            response = client.get(route)
            assert response.status_code == 200, route
            assert "星序 SPA" in response.text
        asset = client.get("/assets/app.js")
        assert asset.text == "console.log('asset')"
        assert "max-age=2592000" in asset.headers.get("cache-control", "")
        (static_dir / "sw.js").write_text("/* sw */", encoding="utf-8")
        sw = client.get("/sw.js")
        assert sw.text == "/* sw */"
        assert "no-cache" in sw.headers.get("cache-control", "")
        assert client.get("/api/v1/not-a-route").status_code == 404


def test_runtime_does_not_advertise_a_detached_codex_process_as_launchable(configured, tmp_path: Path):
    app = configured(
        launch_enabled=True,
        codex_executable=__file__,
        allowed_workspaces=(tmp_path,),
    )
    with TestClient(app) as client:
        login(client)
        items = client.get("/api/v1/runtime").json()["items"]
        codex = next(item for item in items if item["kind"] == "codex")
        assert codex["available"] is False
        assert "connector" in codex["reason"].lower()
        response = client.post(
            "/api/v1/runtime/launch",
            json={"kind": "codex", "workspace": str(tmp_path)},
            headers={"Origin": "http://testserver"},
        )
        assert response.status_code == 409
        assert "connector" in response.json()["detail"].lower()
