from __future__ import annotations

import pytest

from astrorder.daemon.bridge import DaemonBridgeError
from astrorder.daemon.bridge.hermes_projection import HermesCommandFrameRouter


def test_native_completion_without_command_does_not_block_connector_replay(tmp_path):
    from astrorder.config import Settings
    from astrorder.core.events import EventHub
    from astrorder.daemon.bridge import DaemonBridge
    from astrorder.service import ControlService
    from astrorder.store import Store

    settings = Settings(database_url=f"sqlite:///{tmp_path}/app.db", auto_connect_local_hermes=False)
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    bridge = DaemonBridge(store, service, "ws://127.0.0.1:1")
    router = HermesCommandFrameRouter(bridge, store, service)
    try:
        # Real Go message.complete has native turn data, not an App command ID.
        bridge._project_frames("epoch", "native-session", 0, [{
            "session_id": "native-session", "seq_id": 1,
            "event": "hermes.command_complete", "payload": {
                "agent_id": "remote-hermes", "session_id": "native-session",
                "status": "complete", "persisted_turn": {"complete": True},
                "text": "native reply",
            },
        }])
        bridge._project_frames("epoch", "control", 0, [{
            "session_id": "control", "seq_id": 1,
            "event": "connector.hello", "payload": {
                "id": "remote-hermes", "kind": "hermes", "name": "WSL · Hermes",
                "status": "ready", "capabilities": ["chat", "events"],
            },
        }])
        assert store.get_daemon_checkpoint("epoch", "native-session") == 1
        assert store.get_agent("remote-hermes")["status"] == "ready"
        assert store.get_session("remote-hermes", "native-session") is None
        with pytest.raises(DaemonBridgeError, match="command identity"):
            router._on_completion("native-session", {
                "agent_id": "remote-hermes", "session_id": "native-session",
                "command_id": "", "status": "complete", "persisted_turn": {},
            })
    finally:
        router.close()
        store.close()


def test_hermes_completion_router_completes_exact_command_and_rejects_unknown_identity():
    class FakeBridge:
        def __init__(self) -> None:
            self.handler = None

        def register_native_frame_handler(self, event, handler):
            assert event == "hermes.command_complete"
            self.handler = handler
            return lambda: None

    class FakeStore:
        def __init__(self) -> None:
            self.command = {
                "id": "command-exact-id",
                "agent_id": "daemon-hermes",
                "session_id": "native-hermes-session",
                "state": "accepted",
            }
            self.session = {
                "id": "native-hermes-session",
                "agent_id": "daemon-hermes",
                "status": "running",
            }

        def get_command(self, agent_id, session_id, command_id):
            if (agent_id, session_id, command_id) == (
                "daemon-hermes",
                "native-hermes-session",
                "command-exact-id",
            ):
                return dict(self.command)
            return None

        def set_command_state(self, agent_id, session_id, command_id, state, error):
            assert (agent_id, session_id, command_id, state, error) == (
                "daemon-hermes",
                "native-hermes-session",
                "command-exact-id",
                "completed",
                None,
            )
            self.command = {**self.command, "state": state, "error": error}
            return dict(self.command)

        def update_session(self, agent_id, session_id, updates):
            assert (agent_id, session_id, updates) == (
                "daemon-hermes",
                "native-hermes-session",
                {"status": "idle"},
            )
            self.session = {**self.session, **updates}
            return dict(self.session)

    class FakeService:
        def __init__(self) -> None:
            self.events = []

        def _server_event(self, event_type, *, agent_id, session_id, data):
            self.events.append((event_type, agent_id, session_id, dict(data)))

    bridge = FakeBridge()
    store = FakeStore()
    service = FakeService()
    HermesCommandFrameRouter(bridge, store, service)

    bridge.handler(
        "native-hermes-session",
        {
            "agent_id": "daemon-hermes",
            "session_id": "native-hermes-session",
            "command_id": "command-exact-id",
        },
    )

    assert store.command["state"] == "completed"
    assert store.session["status"] == "idle"
    assert service.events == [
        (
            "command.upsert",
            "daemon-hermes",
            "native-hermes-session",
            store.command,
        ),
        (
            "session.upsert",
            "daemon-hermes",
            "native-hermes-session",
            store.session,
        ),
    ]

    store.session["status"] = "running"
    service.events.clear()
    bridge.handler(
        "native-hermes-session",
        {
            "agent_id": "daemon-hermes",
            "session_id": "native-hermes-session",
            "command_id": "command-exact-id",
        },
    )
    assert store.session["status"] == "idle"
    assert [event[0] for event in service.events] == ["session.upsert"]

    with pytest.raises(DaemonBridgeError, match="not found"):
        bridge.handler(
            "native-hermes-session",
            {
                "agent_id": "daemon-hermes",
                "session_id": "native-hermes-session",
                "command_id": "unknown-command",
            },
        )
