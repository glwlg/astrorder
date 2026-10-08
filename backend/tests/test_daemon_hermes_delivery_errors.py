from __future__ import annotations

import pytest

from astrorder.daemon.bridge import DaemonBridgeError
from astrorder.daemon.clients.hermes.control import DaemonHermesController
from astrorder.daemon.clients.ssh.control import DaemonSshController


@pytest.mark.asyncio
@pytest.mark.parametrize("remote", [False, True])
@pytest.mark.parametrize("native_error,expected", [
    ("prompt.submit failed: RPC error 4090: This chat is open in another Hermes window/terminal. Use it there, or start a new chat here.\nDetails: surface=desktop", "failed"),
    ("prompt.submit failed: RPC error 4090: Session x already has a live owner desktop", "failed"),
    ("context deadline exceeded", "unknown"),
    ("Operation is pending or its native outcome is unknown", "unknown"),
])
async def test_native_ownership_refusal_is_not_delivery_uncertainty(remote, native_error, expected):
    agent_id = "ssh-hermes-remote-a" if remote else "local-hermes-default"

    class Bridge:
        calls = []

        async def request_control(self, action, fields):
            self.calls.append(action)
            if action == "session.spawn":
                return {"result": {"agent_id": agent_id, "status": "idle", "connection_id": "remote-a"}}
            assert action == "session.send"
            raise DaemonBridgeError(native_error)

    bridge = Bridge()
    if remote:
        controller = DaemonSshController(bridge, connection_id="remote-a", ssh_settings={"host": "remote.example"})
        submit = controller.submit
    else:
        controller = DaemonHermesController(bridge)
        submit = controller.submit_tui_command
    controller._agent_id = agent_id
    state, detail = await submit({"id": "command", "session_id": "session", "agent_id": agent_id,
                                  "action": "send", "text": "user message", "attachments": []})
    assert state == expected
    if expected == "failed":
        assert "桌面" in detail and "占用" in detail
    assert bridge.calls == ["session.spawn", "session.send"]
