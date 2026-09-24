import pytest
from starlette.websockets import WebSocketDisconnect

from astrorder.daemon.runtimes.pty.relay import (
    DaemonTerminalRelay,
    daemon_pty_target,
    terminal_runtime_id,
)


def test_terminal_runtime_identity_is_stable_opaque_and_scoped_to_exact_agent_session():
    first = terminal_runtime_id("codex-agent-a", "native-session-a")
    repeated = terminal_runtime_id("codex-agent-a", "native-session-a")
    other_agent = terminal_runtime_id("codex-agent-b", "native-session-a")
    other_session = terminal_runtime_id("codex-agent-a", "native-session-b")

    assert first == repeated
    assert first.startswith("pty-")
    assert first not in {other_agent, other_session}
    assert "codex-agent-a" not in first
    assert "native-session-a" not in first


def test_daemon_pty_target_requires_exact_local_identity_and_never_accepts_ssh():
    assert daemon_pty_target(
        {"id": "native-session", "agent_id": "agent-a", "workspace": "C:/allowed", "connection_id": None}
    ) == ("agent-a", "native-session", "C:/allowed")
    assert daemon_pty_target(
        {"id": "native-session", "agent_id": "agent-a", "workspace": "C:/allowed", "connection_id": "ssh-a"}
    ) is None
    assert daemon_pty_target({"id": "native-session", "agent_id": "agent-a", "workspace": None}) is None


@pytest.mark.asyncio
async def test_terminal_relay_forwards_only_exact_terminal_controls_and_output():
    class Bridge:
        endpoint = "ws://127.0.0.1:30124"

        def __init__(self):
            self.calls = []

        async def request_control(self, action, fields):
            self.calls.append((action, dict(fields)))
            return {"result": {"accepted": True}}

    class Browser:
        def __init__(self):
            self.sent = []

        async def send_text(self, value):
            self.sent.append(value)

    bridge = Bridge()
    browser = Browser()
    runtime_id = terminal_runtime_id("agent-a", "session-a")
    relay = DaemonTerminalRelay(bridge, secret="test-only-daemon-secret")

    await relay._forward_input(runtime_id, "echo daemon")
    await relay._forward_input(runtime_id, '{"type":"resize","cols":120,"rows":40}')
    await relay._send_output(
        browser,
        runtime_id,
        {"session_id": runtime_id, "event": "pty.output", "payload": {"data": "hello"}},
    )
    await relay._send_output(
        browser,
        runtime_id,
        {"session_id": "other", "event": "pty.output", "payload": {"data": "must not forward"}},
    )

    assert bridge.calls == [
        ("session.send", {"session_id": runtime_id, "input": "echo daemon"}),
        ("session.resize", {"session_id": runtime_id, "cols": 120, "rows": 40}),
    ]
    assert browser.sent == ["hello"]


@pytest.mark.asyncio
async def test_terminal_relay_disconnect_detaches_without_closing_daemon_pty():
    class Bridge:
        endpoint = "ws://127.0.0.1:30124"

        def __init__(self):
            self.calls = []

        async def request_control(self, action, fields):
            self.calls.append((action, dict(fields)))
            return {"result": {"accepted": True}}

    class Browser:
        async def accept(self):
            return None

        async def receive_text(self):
            raise WebSocketDisconnect()

    bridge = Bridge()
    relay = DaemonTerminalRelay(bridge, secret="test-only-daemon-secret")

    await relay.serve(
        Browser(), agent_id="agent-a", session_id="session-a", workspace="C:/workspace"
    )

    assert [action for action, _fields in bridge.calls] == ["session.spawn"]


@pytest.mark.asyncio
async def test_terminal_relay_closes_daemon_pty_only_for_explicit_browser_close():
    class Bridge:
        endpoint = "ws://127.0.0.1:30124"

        def __init__(self):
            self.calls = []

        async def request_control(self, action, fields):
            self.calls.append((action, dict(fields)))
            return {"result": {"accepted": True}}

    class Browser:
        received = False

        async def accept(self):
            return None

        async def receive_text(self):
            if self.received:
                raise WebSocketDisconnect()
            self.received = True
            return '{"type":"close"}'

    bridge = Bridge()
    relay = DaemonTerminalRelay(bridge, secret="test-only-daemon-secret")
    runtime_id = terminal_runtime_id("agent-a", "session-a")

    await relay.serve(
        Browser(), agent_id="agent-a", session_id="session-a", workspace="C:/workspace"
    )

    assert bridge.calls == [
        (
            "session.spawn",
            {"session_id": runtime_id, "agent_type": "pty", "cwd": "C:/workspace", "params": {}},
        ),
        ("session.close", {"session_id": runtime_id}),
    ]
