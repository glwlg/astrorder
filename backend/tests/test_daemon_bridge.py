"""App Server reconnect/replay tests for the Session Daemon bridge."""
from __future__ import annotations

import asyncio
import json

import pytest

from astrorder.config import Settings
from astrorder.daemon.session_daemon import SessionDaemon
from astrorder.events import EventHub
from astrorder.service import ControlService
from astrorder.store import Store


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


async def wait_for_checkpoint(store, daemon_id: str, session_id: str, seq_id: int) -> None:
    deadline = asyncio.get_running_loop().time() + 1
    while store.get_daemon_checkpoint(daemon_id, session_id) != seq_id:
        if asyncio.get_running_loop().time() >= deadline:
            raise AssertionError(f"checkpoint did not advance to {seq_id}")
        await asyncio.sleep(0.01)


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
async def test_daemon_bridge_replays_only_unacknowledged_frames_after_app_restart(tmp_path):
    from astrorder.daemon.bridge import DaemonBridge

    settings = Settings(
        database_url=f"sqlite:///{tmp_path}/state.sqlite3",
        auto_connect_local_hermes=False,
    )
    daemon = SessionDaemon(capacity=8, daemon_id="daemon-test")
    daemon.record("session-1", "connector.event", session_event())
    server = await daemon.serve("127.0.0.1", 0)
    endpoint = f"ws://127.0.0.1:{server.sockets[0].getsockname()[1]}"

    first_store = Store(settings)
    first_store.upsert_agent(agent())
    first_service = ControlService(first_store, EventHub(), settings)
    try:
        await DaemonBridge(first_store, first_service, endpoint).synchronize_once()
        assert first_store.get_session("daemon-codex", "session-1")["status"] == "running"
        assert first_store.get_daemon_checkpoint("daemon-test", "session-1") == 1
    finally:
        first_store.close()

    daemon.record("session-1", "connector.event", message_event())
    restarted_store = Store(settings)
    restarted_service = ControlService(restarted_store, EventHub(), settings)
    try:
        await DaemonBridge(restarted_store, restarted_service, endpoint).synchronize_once()
        assert restarted_store.list_messages("daemon-codex", "session-1", None, 10)[0][0]["text"] == (
            "daemon replay survived the app restart"
        )
        assert restarted_store.get_daemon_checkpoint("daemon-test", "session-1") == 2
    finally:
        restarted_store.close()
        server.close()
        await server.wait_closed()


@pytest.mark.asyncio
async def test_daemon_bridge_projects_session_null_agent_event_from_runtime_control_wal(tmp_path):
    from astrorder.daemon.bridge import DaemonBridge

    settings = Settings(
        database_url=f"sqlite:///{tmp_path}/agent-control-wal.sqlite3",
        auto_connect_local_hermes=False,
    )
    daemon = SessionDaemon(capacity=8, daemon_id="daemon-agent-control")
    daemon.record(
        "daemon-hermes-control",
        "connector.event",
        {
            "id": "agent-event-from-control-wal",
            "type": "agent.upsert",
            "agent_id": "daemon-codex",
            "session_id": None,
            "data": agent(),
        },
    )
    server = await daemon.serve("127.0.0.1", 0)
    endpoint = f"ws://127.0.0.1:{server.sockets[0].getsockname()[1]}"
    store = Store(settings)
    try:
        await DaemonBridge(store, ControlService(store, EventHub(), settings), endpoint).synchronize_once()
        assert store.get_agent("daemon-codex")["status"] == "ready"
        assert store.get_daemon_checkpoint("daemon-agent-control", "daemon-hermes-control") == 1
    finally:
        store.close()
        server.close()
        await server.wait_closed()


@pytest.mark.asyncio
async def test_daemon_bridge_leaves_checkpoint_at_zero_when_wal_overflowed(tmp_path):
    from astrorder.daemon.bridge import DaemonBridge

    settings = Settings(
        database_url=f"sqlite:///{tmp_path}/overflow.sqlite3",
        auto_connect_local_hermes=False,
    )
    daemon = SessionDaemon(capacity=1, daemon_id="daemon-overflow")
    daemon.record("session-1", "connector.event", session_event())
    daemon.record("session-1", "connector.event", message_event())
    server = await daemon.serve("127.0.0.1", 0)
    endpoint = f"ws://127.0.0.1:{server.sockets[0].getsockname()[1]}"
    store = Store(settings)
    store.upsert_agent(agent())
    try:
        report = await DaemonBridge(store, ControlService(store, EventHub(), settings), endpoint).synchronize_once()
        assert report.overflowed_sessions == ("session-1",)
        assert store.get_daemon_checkpoint("daemon-overflow", "session-1") == 0
        assert store.get_session("daemon-codex", "session-1") is None
    finally:
        store.close()
        server.close()
        await server.wait_closed()


@pytest.mark.asyncio
async def test_daemon_bridge_does_not_project_live_tail_after_an_overflow(tmp_path):
    from astrorder.daemon.bridge import DaemonBridge

    settings = Settings(
        database_url=f"sqlite:///{tmp_path}/overflow-live.sqlite3",
        auto_connect_local_hermes=False,
    )
    daemon = SessionDaemon(capacity=1, daemon_id="daemon-overflow-live")
    daemon.record("session-1", "connector.event", session_event())
    daemon.record("session-1", "connector.event", message_event())
    server = await daemon.serve("127.0.0.1", 0)
    endpoint = f"ws://127.0.0.1:{server.sockets[0].getsockname()[1]}"
    store = Store(settings)
    store.upsert_agent(agent())
    bridge = DaemonBridge(store, ControlService(store, EventHub(), settings), endpoint)
    try:
        report = await bridge.synchronize_once()
        frame = daemon.record("session-1", "connector.event", message_event())

        assert report.overflowed_sessions == ("session-1",)
        bridge._project_live_frame(json.dumps(frame))
        assert store.get_daemon_checkpoint("daemon-overflow-live", "session-1") == 0
        assert store.get_session("daemon-codex", "session-1") is None
    finally:
        store.close()
        server.close()
        await server.wait_closed()


@pytest.mark.asyncio
async def test_daemon_bridge_projects_live_frames_after_its_initial_sync(tmp_path):
    from astrorder.daemon.bridge import DaemonBridge

    settings = Settings(
        database_url=f"sqlite:///{tmp_path}/live.sqlite3",
        auto_connect_local_hermes=False,
    )
    daemon = SessionDaemon(capacity=8, daemon_id="daemon-live")
    daemon.record("session-1", "connector.event", session_event())
    server = await daemon.serve("127.0.0.1", 0)
    endpoint = f"ws://127.0.0.1:{server.sockets[0].getsockname()[1]}"
    store = Store(settings)
    store.upsert_agent(agent())
    stopping = asyncio.Event()
    bridge = DaemonBridge(store, ControlService(store, EventHub(), settings), endpoint)
    task = asyncio.create_task(bridge.run(stopping))
    try:
        await wait_for_checkpoint(store, "daemon-live", "session-1", 1)
        await daemon.publish("session-1", "connector.event", message_event())
        await wait_for_checkpoint(store, "daemon-live", "session-1", 2)
        assert store.list_messages("daemon-codex", "session-1", None, 10)[0][0]["id"] == "message-1"
    finally:
        stopping.set()
        await asyncio.wait_for(task, timeout=1)
        store.close()
        server.close()
        await server.wait_closed()


@pytest.mark.asyncio
async def test_daemon_bridge_forwards_explicit_runtime_control_requests(tmp_path):
    from astrorder.daemon.bridge import DaemonBridge

    class FixtureRuntime:
        async def spawn(self, request):
            assert request["session_id"] == "control-session"
            return {"status": "idle", "handle": "opaque-handle"}

        async def command(self, action, request):
            assert action == "session.approve"
            assert request["request_id"] != "approval-request"
            assert request["decision"] == "accept"
            return {"status": "running", "accepted": True}

    settings = Settings(
        database_url=f"sqlite:///{tmp_path}/control.sqlite3",
        auto_connect_local_hermes=False,
    )
    daemon = SessionDaemon(
        capacity=8,
        daemon_id="daemon-control",
        secret="test-only-daemon-secret",
    )
    daemon.register_runtime("fixture", FixtureRuntime())
    server = await daemon.serve("127.0.0.1", 0)
    endpoint = f"ws://127.0.0.1:{server.sockets[0].getsockname()[1]}"
    store = Store(settings)
    bridge = DaemonBridge(
        store,
        ControlService(store, EventHub(), settings),
        endpoint,
        secret="test-only-daemon-secret",
    )
    try:
        spawned = await bridge.request_control(
            "session.spawn",
            {
                "session_id": "control-session",
                "agent_type": "fixture",
                "params": {},
            },
        )
        approved = await bridge.request_control(
            "session.approve",
            {
                "session_id": "control-session",
                "approval_id": "approval-request",
                "decision": "accept",
            },
        )

        assert spawned["daemon_id"] == "daemon-control"
        assert spawned["result"] == {"status": "idle", "handle": "opaque-handle"}
        assert approved["result"] == {"status": "running", "accepted": True}
        assert daemon.status()["control-session"]["status"] == "running"
    finally:
        store.close()
        server.close()
        await server.wait_closed()


@pytest.mark.asyncio
async def test_daemon_bridge_acknowledges_native_frame_only_after_handler_projects_it(tmp_path):
    from astrorder.daemon.bridge import DaemonBridge, DaemonBridgeError

    settings = Settings(
        database_url=f"sqlite:///{tmp_path}/native-frame.sqlite3",
        auto_connect_local_hermes=False,
    )
    daemon = SessionDaemon(capacity=8, daemon_id="daemon-native-frame")
    daemon.record(
        "native-thread-1",
        "codex.notification",
        {
            "agent_id": "daemon-codex",
            "frame": {"method": "turn/completed", "params": {"threadId": "native-thread-1"}},
        },
    )
    server = await daemon.serve("127.0.0.1", 0)
    endpoint = f"ws://127.0.0.1:{server.sockets[0].getsockname()[1]}"
    store = Store(settings)
    bridge = DaemonBridge(store, ControlService(store, EventHub(), settings), endpoint)
    received: list[tuple[str, dict[str, object]]] = []
    try:
        with pytest.raises(DaemonBridgeError, match="native frame handler"):
            await bridge.synchronize_once()
        assert store.get_daemon_checkpoint("daemon-native-frame", "native-thread-1") == 0

        unregister = bridge.register_native_frame_handler(
            "codex.notification",
            lambda session_id, payload: received.append((session_id, dict(payload))),
        )
        report = await bridge.synchronize_once()
        unregister()

        assert report.replayed_frames == 1
        assert received == [
            (
                "native-thread-1",
                {
                    "agent_id": "daemon-codex",
                    "frame": {
                        "method": "turn/completed",
                        "params": {"threadId": "native-thread-1"},
                    },
                },
            )
        ]
        assert store.get_daemon_checkpoint("daemon-native-frame", "native-thread-1") == 1
    finally:
        store.close()
        server.close()
        await server.wait_closed()


@pytest.mark.asyncio
async def test_daemon_bridge_handshakes_before_replaying_a_secret_protected_daemon(tmp_path):
    from astrorder.daemon.bridge import DaemonBridge

    settings = Settings(
        database_url=f"sqlite:///{tmp_path}/handshake.sqlite3",
        auto_connect_local_hermes=False,
    )
    daemon = SessionDaemon(
        capacity=8,
        daemon_id="daemon-handshake",
        secret="test-only-daemon-secret",
    )
    daemon.record("session-1", "connector.event", session_event())
    server = await daemon.serve("127.0.0.1", 0)
    endpoint = f"ws://127.0.0.1:{server.sockets[0].getsockname()[1]}"
    store = Store(settings)
    store.upsert_agent(agent())
    try:
        report = await DaemonBridge(
            store,
            ControlService(store, EventHub(), settings),
            endpoint,
            secret="test-only-daemon-secret",
        ).synchronize_once()

        assert report.daemon_id == "daemon-handshake"
        assert store.get_daemon_checkpoint("daemon-handshake", "session-1") == 1
    finally:
        store.close()
        server.close()
        await server.wait_closed()
