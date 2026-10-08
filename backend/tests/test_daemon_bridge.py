"""App Server reconnect/replay tests for the Session Daemon bridge."""
from __future__ import annotations

import json

import pytest

from astrorder.config import Settings
from astrorder.daemon.bridge import DaemonBridge
from astrorder.core.events import EventHub
from astrorder.service import ControlService
from astrorder.store import Store


@pytest.mark.asyncio
async def test_overflow_blocks_live_projection_and_preserves_checkpoint(tmp_path):
    """The App must not acknowledge a tail whose replay prefix was lost."""
    settings = Settings(database_url=f"sqlite:///{tmp_path}/overflow.db", auto_connect_local_hermes=False)
    store = Store(settings)

    class Bridge(DaemonBridge):
        async def _request(self, socket, action, fields, **kwargs):
            if action == "daemon.status":
                return {"daemon_id": "overflow", "sessions": {"s": {"status": "idle"}}, "connectors": []}
            return {"daemon_id": "overflow", "sessions": {"s": {"overflow": True, "frames": []}}}

    bridge = Bridge(store, ControlService(store, EventHub(), settings), "ws://127.0.0.1:1")
    delivered = []
    bridge.register_native_frame_handler("native.test", lambda *args: delivered.append(args))
    try:
        report = await bridge._synchronize(None)
        assert report.overflowed_sessions == ("s",)
        bridge._project_live_frame(json.dumps({"session_id": "s", "seq_id": 3, "timestamp": 1.0,
                                               "event": "native.test", "payload": {}}))
        assert delivered == []
        assert store.get_daemon_checkpoint("overflow", "s") == 0
    finally:
        store.close()


@pytest.mark.asyncio
async def test_connected_agent_is_restored_from_go_status_without_replaying_hello(tmp_path):
    settings = Settings(database_url=f"sqlite:///{tmp_path}/connector.db", auto_connect_local_hermes=False)
    store = Store(settings)
    connected = {**agent(), "kind": "hermes"}
    store.upsert_agent({**connected, "status": "disconnected"})

    class Bridge(DaemonBridge):
        async def _request(self, socket, action, fields, **kwargs):
            return {"daemon_id": "connected", "sessions": {}, "connectors": [connected]}

    bridge = Bridge(store, ControlService(store, EventHub(), settings), "ws://127.0.0.1:1")
    try:
        await bridge._synchronize(None)
        assert store.get_agent(connected["id"])["status"] == "ready"
    finally:
        store.close()


def agent() -> dict[str, object]:
    return {
        "id": "daemon-codex",
        "kind": "codex",
        "name": "Daemon Codex",
        "status": "ready",
        "capabilities": ["chat", "events"],
        "limitation": None,
        "source_id": "daemon-codex",
        "runtime_id": "daemon-codex",
        "control_state": "owned",
    }


def session_event() -> dict[str, object]:
    return {
        "id": "native-session-event",
        "type": "session.upsert",
        "agent_id": "daemon-codex",
        "session_id": "session-1",
        "data": {
            "id": "session-1",
            "agent_id": "daemon-codex",
            "title": "Daemon-owned session",
            "workspace": None,
            "status": "running",
            "updated_at": "2026-09-11T12:00:00Z",
            "source_id": "daemon-codex",
            "source_session_id": "session-1",
            "history_state": "live",
            "control_state": "owned",
        },
    }


def message_event() -> dict[str, object]:
    return {
        "id": "native-message-event",
        "type": "message.upsert",
        "agent_id": "daemon-codex",
        "session_id": "session-1",
        "data": {
            "id": "message-1",
            "session_id": "session-1",
            "agent_id": "daemon-codex",
            "role": "assistant",
            "kind": "message",
            "text": "daemon replay survived the app restart",
            "attachments": [],
            "created_at": "2026-09-11T12:00:01Z",
            "command_id": None,
            "tool": None,
        },
    }


def test_daemon_checkpoint_is_persistent_and_scoped_to_daemon_instance(tmp_path):
    settings = Settings(
        database_url=f"sqlite:///{tmp_path}/checkpoint.sqlite3",
        auto_connect_local_hermes=False,
    )
    store = Store(settings)
    try:
        store.set_daemon_checkpoint("daemon-one", "session-1", 7)
        store.set_daemon_checkpoint("daemon-two", "session-1", 2)
    finally:
        store.close()

    reopened = Store(settings)
    try:
        assert reopened.get_daemon_checkpoint("daemon-one", "session-1") == 7
        assert reopened.get_daemon_checkpoint("daemon-two", "session-1") == 2
        assert reopened.list_daemon_checkpoints("daemon-one") == {"session-1": 7}
    finally:
        reopened.close()


@pytest.mark.asyncio
async def test_daemon_bridge_retires_missing_owned_sessions_after_daemon_restart(tmp_path):
    class RestartedDaemonSocket:
        request = None

        async def send(self, raw):
            self.request = json.loads(raw)

        async def recv(self):
            action = self.request["action"]
            payload = {
                "action": f"{action}.result",
                "request_id": self.request["request_id"],
                "daemon_id": "daemon-new",
            }
            if action == "daemon.status":
                payload.update({"sessions": {}, "connectors": []})
            else:
                payload["sessions"] = {}
            return json.dumps(payload)

    settings = Settings(
        database_url=f"sqlite:///{tmp_path}/daemon-restart.sqlite3",
        auto_connect_local_hermes=False,
    )
    store = Store(settings)
    store.upsert_agent(agent())
    store.upsert_session(session_event()["data"])
    store.create_command(
        command={
            "id": "command-1",
            "agent_id": "daemon-codex",
            "session_id": "session-1",
            "action": "send",
            "text": "继续",
            "attachment_ids": [],
            "target_id": None,
        },
        attachments=[],
        initial_state="running",
    )
    store.upsert_task(
        {
            "id": "task-1",
            "agent_id": "daemon-codex",
            "session_id": "session-1",
            "kind": "tool",
            "title": "running tool",
            "status": "running",
            "created_at": "2026-09-15T03:00:00Z",
            "updated_at": "2026-09-15T03:00:00Z",
        }
    )
    bridge = DaemonBridge(store, ControlService(store, EventHub(), settings), "ws://127.0.0.1:1")
    bridge._daemon_id = "daemon-old"
    restarts: list[str] = []
    bridge.register_restart_handler(lambda: restarts.append("rebind"))
    store.list_commands = lambda *_args: (_ for _ in ()).throw(AssertionError("per-session scan"))
    store.list_tasks = lambda *_args: (_ for _ in ()).throw(AssertionError("per-session scan"))
    try:
        await bridge._synchronize(RestartedDaemonSocket())

        assert restarts == ["rebind"]
        assert store.get_session("daemon-codex", "session-1")["status"] == "idle"
        command = store.get_command("daemon-codex", "session-1", "command-1")
        assert command["state"] == "unknown"
        assert "小内核已重启" in command["error"]
        assert store.get_task("daemon-codex", "session-1", "task-1")["status"] == "unknown"
    finally:
        store.close()


def test_bridge_projects_lost_ownership_without_resending(tmp_path):
    settings = Settings(
        database_url=f"sqlite:///{tmp_path}/ownership.sqlite3",
        auto_connect_local_hermes=False,
    )
    store = Store(settings)
    store.upsert_agent(agent())
    store.upsert_session(session_event()["data"])
    store.create_command(
        command={
            "id": "command-1",
            "agent_id": "daemon-codex",
            "session_id": "session-1",
            "action": "send",
            "text": "继续",
            "attachment_ids": [],
            "target_id": None,
        },
        attachments=[],
        initial_state="running",
    )
    bridge = DaemonBridge(store, ControlService(store, EventHub(), settings), "ws://127.0.0.1:1")
    bridge._daemon_id = "daemon-test"
    try:
        bridge._project_frame(
            "daemon-test",
            "session-1",
            1,
            {
                "session_id": "session-1",
                "seq_id": 1,
                "event": "runtime.ownership_lost",
                "status": "error",
                "payload": {
                    "adopted": False,
                    "previous_status": "waiting_approval",
                    "reason": "native process handle does not survive daemon restart",
                },
            },
        )
        session = store.get_session("daemon-codex", "session-1")
        command = store.get_command("daemon-codex", "session-1", "command-1")
        assert session["status"] == "error"
        assert command["state"] == "unknown"
        assert "不会自动重发" in command["error"]
        assert store.list_messages("daemon-codex", "session-1", None, 10)[0] == []
        assert store.get_daemon_checkpoint("daemon-test", "session-1") == 1
    finally:
        store.close()


def test_bridge_records_orphan_liveness_without_resending(tmp_path):
    settings = Settings(
        database_url=f"sqlite:///{tmp_path}/orphan.sqlite3",
        auto_connect_local_hermes=False,
    )
    store = Store(settings)
    store.upsert_agent(agent())
    store.upsert_session(session_event()["data"])
    store.create_command(
        command={
            "id": "command-1",
            "agent_id": "daemon-codex",
            "session_id": "session-1",
            "action": "send",
            "text": "继续",
            "attachment_ids": [],
            "target_id": None,
        },
        attachments=[],
        initial_state="running",
    )
    bridge = DaemonBridge(store, ControlService(store, EventHub(), settings), "ws://127.0.0.1:1")
    bridge._daemon_id = "daemon-test"
    try:
        bridge._project_frame(
            "daemon-test",
            "session-1",
            1,
            {
                "session_id": "session-1",
                "seq_id": 1,
                "event": "runtime.ownership_lost",
                "status": "error",
                "payload": {
                    "adopted": False,
                    "previous_status": "running",
                    "orphan_alive": True,
                    "reason": "native process handle does not survive daemon restart",
                },
            },
        )
        session = store.get_session("daemon-codex", "session-1")
        command = store.get_command("daemon-codex", "session-1", "command-1")
        assert session["status"] == "error"
        assert session["runtime_owner"] == {"orphan_alive": True, "adopted": False}
        assert command["state"] == "unknown"
        assert "不会自动重发" in command["error"]
        assert store.list_messages("daemon-codex", "session-1", None, 10)[0] == []
    finally:
        store.close()


def test_bridge_records_runtime_owner_without_changing_activity(tmp_path):
    settings = Settings(
        database_url=f"sqlite:///{tmp_path}/owner.sqlite3",
        auto_connect_local_hermes=False,
    )
    store = Store(settings)
    store.upsert_agent(agent())
    store.upsert_session(session_event()["data"])
    bridge = DaemonBridge(store, ControlService(store, EventHub(), settings), "ws://127.0.0.1:1")
    bridge._daemon_id = "daemon-test"
    try:
        bridge._project_frame(
            "daemon-test",
            "session-1",
            1,
            {
                "session_id": "session-1",
                "seq_id": 1,
                "event": "runtime.owner",
                "status": "running",
                "payload": {"owner_pid": 4242},
            },
        )
        assert store.get_session("daemon-codex", "session-1")["status"] == "running"
        assert store.list_messages("daemon-codex", "session-1", None, 10)[0] == []
        assert store.get_daemon_checkpoint("daemon-test", "session-1") == 1
    finally:
        store.close()


def test_bridge_replays_attachment_without_storing_a_copy(tmp_path):
    settings = Settings(
        database_url=f"sqlite:///{tmp_path}/attachment.sqlite3",
        auto_connect_local_hermes=False,
    )
    store = Store(settings)
    store.upsert_agent(agent())
    store.upsert_session(session_event()["data"])
    bridge = DaemonBridge(store, ControlService(store, EventHub(), settings), "ws://127.0.0.1:1")
    bridge._daemon_id = "daemon-test"
    delivered = []
    bridge.register_native_frame_handler(
        "attachment.accepted",
        lambda session_id, payload: delivered.append((session_id, payload)),
    )
    try:
        bridge._project_frame(
            "daemon-test",
            "session-1",
            1,
            {
                "session_id": "session-1",
                "seq_id": 1,
                "event": "attachment.accepted",
                "status": "idle",
                "payload": {
                    "name": "notes.txt",
                    "media_type": "text/plain",
                    "content_base64": "bm90ZXM=",
                },
            },
        )
        assert delivered == [
            ("session-1", {"name": "notes.txt", "media_type": "text/plain", "content_base64": "bm90ZXM="})
        ]
        assert store.list_messages("daemon-codex", "session-1", None, 10)[0] == []
        assert store.get_session("daemon-codex", "session-1")["status"] == "running"
        assert store.get_daemon_checkpoint("daemon-test", "session-1") == 1
    finally:
        store.close()
