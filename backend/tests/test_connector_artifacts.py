from __future__ import annotations

from pathlib import Path

import pytest
from connectors.codex.astrorder_codex_connector import CodexBridge
from connectors.codex.astrorder_codex_connector.config import CodexConnectorConfig
from connectors.codex.astrorder_codex_connector.protocol import CodexAppServerProtocol
from connectors.hermes.astrorder_hermes_plugin import HermesBridge
from connectors.hermes.astrorder_hermes_plugin.config import HermesConnectorConfig


class FakeContext:
    def __init__(self):
        self.hooks = {}
        self.injected = []

    def register_hook(self, name, callback):
        self.hooks[name] = callback

    def inject_message(self, content, role="user", *, session_key=None):
        self.injected.append((content, role, session_key))
        return True


class FakeTransport:
    def __init__(self, command_callback=None):
        self.agent = None
        self.events = []
        self.command_callback = command_callback
        self.started = False

    def start(self, agent):
        self.started = True
        self.agent = agent

    def send_event(self, event):
        self.events.append(event)

    def stop(self):
        self.started = False


def test_hermes_public_hook_registration_and_text_command():
    context = FakeContext()
    transport = FakeTransport()
    bridge = HermesBridge(
        context,
        HermesConnectorConfig(
            endpoint="ws://127.0.0.1:8765/ws/v1/connector",
            secret="test-only",
            agent_id="hermes-test",
            agent_name="Hermes test",
        ),
        transport=transport,
    ).register()
    assert set(context.hooks) == {
        "on_session_start",
        "on_session_end",
        "on_session_finalize",
        "post_llm_call",
        "on_stream_start",
        "on_stream_delta",
        "on_stream_end",
    }
    context.hooks["on_session_start"]("session-1", platform="cli")
    assert transport.agent["capabilities"] == ["chat", "events"]
    assert any(event["type"] == "session.upsert" for event in transport.events)
    bridge._on_command(
        {
            "id": "command-1",
            "agent_id": "hermes-test",
            "session_id": "session-1",
            "action": "send",
            "state": "accepted",
            "text": "hello",
            "attachments": [],
            "error": None,
        }
    )
    assert context.injected == [("hello", "user", None)]
    updates = [event for event in transport.events if event["type"] == "command.upsert"]
    assert updates[-1]["data"]["id"] == "command-1"
    assert updates[-1]["data"]["state"] == "accepted"


def test_hermes_does_not_claim_attachment_or_command_id_support():
    context = FakeContext()
    transport = FakeTransport()
    bridge = HermesBridge(
        context,
        HermesConnectorConfig(
            endpoint="ws://127.0.0.1:8765/ws/v1/connector",
            secret="test-only",
            agent_id="hermes-test",
            agent_name="Hermes test",
        ),
        transport=transport,
    )
    bridge._session_id = "session-1"
    bridge._on_command(
        {
            "id": "command-2",
            "agent_id": "hermes-test",
            "session_id": "session-1",
            "action": "send",
            "text": "image",
            "attachments": [{"id": "attachment-1"}],
        }
    )
    update = transport.events[-1]
    assert update["data"]["state"] == "failed"
    assert "attachment" in update["data"]["error"].lower()


def test_hermes_stream_coalesces_only_by_reliable_turn_id():
    context = FakeContext()
    transport = FakeTransport()
    bridge = HermesBridge(
        context,
        HermesConnectorConfig(
            endpoint="ws://127.0.0.1:8765/ws/v1/connector",
            secret="test-only",
            agent_id="hermes-test",
            agent_name="Hermes test",
        ),
        transport=transport,
    )
    bridge._on_stream_start(session_id="session-1", turn_id="turn-1")
    bridge._on_stream_delta(session_id="session-1", turn_id="turn-1", delta="a")
    bridge._on_stream_delta(session_id="session-1", turn_id="turn-1", delta="b")
    bridge._on_stream_end(session_id="session-1", turn_id="turn-1", final_text="ab")
    messages = [event for event in transport.events if event["type"] == "message.upsert"]
    assert len({event["data"]["id"] for event in messages}) == 1
    assert messages[-1]["data"]["text"] == "ab"
    assert all(event["data"]["command_id"] is None for event in messages)


def test_codex_protocol_uses_documented_app_server_methods():
    protocol = CodexAppServerProtocol()
    initialize = protocol.initialize()
    assert initialize["method"] == "initialize"
    assert initialize["params"]["clientInfo"]["name"] == "astrorder_codex_connector"
    assert protocol.initialized() == {"method": "initialized", "params": {}}
    assert protocol.start_thread("C:/workspace") ["method"] == "thread/start"
    assert protocol.start_turn("thread-1", "hello", "C:/workspace")["method"] == "turn/start"
    assert protocol.interrupt_turn("thread-1", "turn-1")["method"] == "turn/interrupt"
    notification = protocol.notification(
        {"method": "item/agentMessage/delta", "params": {"turnId": "turn-1", "delta": "x"}}
    )
    assert notification is not None
    assert notification.method == "item/agentMessage/delta"


class FakeAppServer:
    def __init__(self):
        self.requests = []
        self.sent = []
        self.notifications = None

    def start(self):
        return None

    def send(self, message):
        self.sent.append(message)

    def request(self, method, params):
        self.requests.append((method, params))
        if method == "thread/start":
            return {"thread": {"id": "thread-1"}}
        if method == "turn/start":
            return {"turn": {"id": "turn-1"}}
        return {}

    def stop(self):
        return None


def test_codex_companion_maps_app_server_stream_and_cancel():
    transport = FakeTransport()
    app_server = FakeAppServer()
    bridge = CodexBridge(
        CodexConnectorConfig(
            endpoint="ws://127.0.0.1:8765/ws/v1/connector",
            secret="test-only",
            agent_id="codex-test",
            agent_name="Codex test",
            executable="codex",
            workspace=Path("C:/workspace"),
            allowed_workspaces=(Path("C:/"),),
        ),
        app_server=app_server,
        transport=transport,
    )
    assert bridge.start() == "thread-1"
    command = {
        "id": "command-1",
        "agent_id": "codex-test",
        "session_id": "thread-1",
        "action": "send",
        "text": "hello",
        "attachments": [],
        "error": None,
    }
    bridge._on_command(command)
    assert app_server.requests[-1][0] == "turn/start"
    bridge._on_notification(
        {"method": "item/agentMessage/delta", "params": {"turnId": "turn-1", "delta": "answer"}}
    )
    bridge._on_notification(
        {
            "method": "turn/completed",
            "params": {"turn": {"id": "turn-1", "status": "completed"}},
        }
    )
    updates = [event for event in transport.events if event["type"] == "command.upsert"]
    assert updates[-1]["data"]["state"] == "completed"
    assert all(event["data"]["command_id"] is None for event in transport.events if event["type"] == "message.upsert")


def test_connector_sources_are_isolated_from_server_and_require_explicit_secret(monkeypatch):
    root = Path(__file__).parents[2] / "connectors"
    source = "\n".join(path.read_text(encoding="utf-8") for path in root.rglob("*.py"))
    assert "astrorder.main" not in source
    monkeypatch.delenv("ASTRORDER_CONNECTOR_SECRET", raising=False)
    with pytest.raises(ValueError, match="ASTRORDER_CONNECTOR_SECRET"):
        HermesConnectorConfig.from_env()
