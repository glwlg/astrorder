from __future__ import annotations

import asyncio
from datetime import datetime

import pytest

from astrorder.daemon.ssh_control import DaemonSshController, ssh_runtime_control_id


def test_daemon_ssh_controller_connects_and_creates_by_exact_connection_identity():
    class FakeBridge:
        def __init__(self) -> None:
            self.calls: list[tuple[str, dict[str, object]]] = []

        async def request_control(self, action, fields):
            self.calls.append((action, dict(fields)))
            if action == "session.spawn":
                return {
                    "result": {
                        "status": "idle",
                        "agent_id": "ssh-hermes-remote-a",
                        "source_id": "hermes-ssh-opaque-source",
                        "runtime_id": "ssh-hermes-remote-a",
                        "connection_id": "remote-a",
                    }
                }
            if action == "session.create":
                return {
                    "result": {
                        "session_id": "native-remote-session-id",
                        "status": "idle",
                        "agent_id": "ssh-hermes-remote-a",
                        "source_id": "hermes-ssh-opaque-source",
                        "connection_id": "remote-a",
                    }
                }
            raise AssertionError(f"unexpected daemon action: {action}")

    bridge = FakeBridge()
    settings = {"host": "remote.example", "port": 22, "user": "operator"}
    controller = DaemonSshController(bridge, connection_id="remote-a", ssh_settings=settings)

    connected = controller.start()
    created = controller.create_session(
        "/srv/project",
        "Daemon remote",
        provider="ocx",
        model="google-antigravity/gemini-3.8-flash",
        effort="high",
    )
    updated_at = created.pop("updated_at")

    assert datetime.fromisoformat(updated_at).tzinfo is not None
    assert connected["alive"] is True
    assert connected["agent_id"] == "ssh-hermes-remote-a"
    assert connected["connection_id"] == "remote-a"
    assert created == {
        "id": "native-remote-session-id",
        "agent_id": "ssh-hermes-remote-a",
        "title": "Daemon remote",
        "workspace": "/srv/project",
        "status": "idle",
        "source_id": "hermes-ssh-opaque-source",
        "connection_id": "remote-a",
        "source_session_id": "native-remote-session-id",
        "history_state": "live",
        "control_state": "owned",
    }
    assert bridge.calls == [
        (
            "session.spawn",
            {
                "session_id": ssh_runtime_control_id("remote-a"),
                "agent_type": "ssh",
                "params": {
                    "runtime_control": True,
                    "connection_id": "remote-a",
                    "ssh_settings": settings,
                },
            },
        ),
        (
            "session.create",
            {
                "agent_type": "ssh",
                "cwd": "/srv/project",
                "title": "Daemon remote",
                "provider": "ocx",
                "model": "google-antigravity/gemini-3.8-flash",
                "effort": "high",
                "params": {"connection_id": "remote-a", "ssh_settings": settings},
            },
        ),
    ]


@pytest.mark.asyncio
async def test_daemon_ssh_controller_submits_after_exact_connection_session_binding():
    class FakeBridge:
        def __init__(self) -> None:
            self.calls: list[tuple[str, dict[str, object]]] = []

        async def request_control(self, action, fields):
            self.calls.append((action, dict(fields)))
            if action == "session.spawn":
                return {
                    "result": {
                        "status": "idle",
                        "agent_id": "ssh-hermes-remote-a",
                        "connection_id": "remote-a",
                    }
                }
            if action == "session.send":
                return {"result": {"status": "running", "accepted": True}}
            raise AssertionError(f"unexpected daemon action: {action}")

    bridge = FakeBridge()
    settings = {"host": "remote.example", "port": 22}
    controller = DaemonSshController(bridge, connection_id="remote-a", ssh_settings=settings)
    await asyncio.to_thread(controller.start)

    state, detail = await controller.submit(
        {
            "id": "command-id",
            "agent_id": "ssh-hermes-remote-a",
            "session_id": "native-remote-session-id",
            "action": "send",
            "text": "exact remote prompt",
            "attachments": [],
        }
    )

    assert (state, detail) == ("accepted", None)
    assert bridge.calls[1:] == [
        (
            "session.spawn",
            {
                "session_id": "native-remote-session-id",
                "agent_type": "ssh",
                "params": {"connection_id": "remote-a", "ssh_settings": settings},
            },
        ),
        (
            "session.send",
            {
                "session_id": "native-remote-session-id",
                "text": "exact remote prompt",
                "command_id": "command-id",
            },
        ),
    ]


def test_daemon_ssh_controller_routes_native_rpc_through_daemon():
    class FakeBridge:
        async def request_control(self, action, fields):
            assert action == "runtime.request"
            assert fields["agent_type"] == "ssh"
            assert fields["method"] == "approval.pending"
            assert fields["request_params"] == {"session_id": "native-1"}
            return {"result": {"result": {"approvals": []}}}

    controller = DaemonSshController(
        FakeBridge(),
        connection_id="remote-a",
        ssh_settings={"host": "remote.example", "port": 22},
    )

    assert controller.rpc("approval.pending", {"session_id": "native-1"}) == {
        "result": {"approvals": []}
    }
