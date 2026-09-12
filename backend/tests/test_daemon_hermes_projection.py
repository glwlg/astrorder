from __future__ import annotations

import pytest

from astrorder.daemon.bridge import DaemonBridgeError
from astrorder.daemon.hermes_projection import HermesCommandFrameRouter


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
    assert service.events == [
        (
            "command.upsert",
            "daemon-hermes",
            "native-hermes-session",
            store.command,
        )
    ]
    with pytest.raises(DaemonBridgeError, match="not found"):
        bridge.handler(
            "native-hermes-session",
            {
                "agent_id": "daemon-hermes",
                "session_id": "native-hermes-session",
                "command_id": "unknown-command",
            },
        )
