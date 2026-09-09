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


def test_post_llm_does_not_reemit_user_input_after_reply():
    transport = FakeTransport()
    bridge = HermesBridge(FakeContext(), HermesConnectorConfig(
        endpoint='ws://127.0.0.1:30002/ws/v1/connector', secret='test-only',
        agent_id='hermes-test', agent_name='Hermes test'), transport=transport)
    bridge._on_post_llm_call('session', '你好', '你好！')
    rows = [e['data'] for e in transport.events if e['type'] == 'message.upsert']
    assert [r['role'] for r in rows] == ['assistant']


def test_stream_reasoning_and_tool_callbacks_emit_transcript_activity():
    transport = FakeTransport()
    bridge = HermesBridge(FakeContext(), HermesConnectorConfig(
        endpoint='ws://127.0.0.1:30002/ws/v1/connector', secret='test-only',
        agent_id='hermes-test', agent_name='Hermes test'), transport=transport)
    bridge._on_stream_delta('session', turn_id='turn', delta='检查', kind='reasoning')
    bridge._on_stream_delta('session', turn_id='turn', delta='数据', kind='reasoning')
    bridge._on_pre_tool_call(session_id='session', tool_name='execute_code', tool_call_id='call')
    bridge._on_post_tool_call(session_id='session', tool_name='execute_code', tool_call_id='call', result='done')
    rows = [e['data'] for e in transport.events if e['type'] == 'message.upsert']
    assert [r['kind'] for r in rows] == ['thinking', 'thinking', 'tool', 'tool']
    assert rows[0]['id'] == rows[1]['id']
    assert rows[1]['text'] == '检查数据'
    assert rows[2]['id'] == rows[3]['id']
    assert rows[3]['tool']['status'] == 'completed'
    assert rows[3]['text'] == 'done'


def test_hermes_public_hook_registration_and_text_command():
    context = FakeContext()
    transport = FakeTransport()
    bridge = HermesBridge(
        context,
        HermesConnectorConfig(
            endpoint="ws://127.0.0.1:30002/ws/v1/connector",
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
        "pre_tool_call",
        "post_tool_call",
        "subagent_start",
        "subagent_stop",
    }
    context.hooks["on_session_start"]("session-1", platform="cli")
    assert transport.agent["capabilities"] == ["chat", "events", "task_events"]
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


def test_hermes_registers_a_connecting_agent_before_any_session_starts():
    context = FakeContext()
    transport = FakeTransport()

    HermesBridge(
        context,
        HermesConnectorConfig(
            endpoint="ws://127.0.0.1:30002/ws/v1/connector",
            secret="test-only",
            agent_id="hermes-test",
            agent_name="Hermes test",
        ),
        transport=transport,
    ).register()

    assert transport.started is True
    assert transport.agent["status"] == "connecting"
    assert transport.agent["capabilities"] == ["chat", "events", "task_events"]
    assert not any(event["type"] == "session.upsert" for event in transport.events)


def test_hermes_does_not_claim_attachment_or_command_id_support():
    context = FakeContext()
    transport = FakeTransport()
    bridge = HermesBridge(
        context,
        HermesConnectorConfig(
            endpoint="ws://127.0.0.1:30002/ws/v1/connector",
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


def test_hermes_emits_public_tool_and_subagent_task_events_without_scraping_output():
    context = FakeContext()
    transport = FakeTransport()
    HermesBridge(
        context,
        HermesConnectorConfig(
            endpoint="ws://127.0.0.1:30002/ws/v1/connector",
            secret="test-only",
            agent_id="hermes-test",
            agent_name="Hermes test",
        ),
        transport=transport,
    ).register()

    context.hooks["pre_tool_call"](
        tool_name="pytest", args={"command": "pytest -q"}, session_id="session-1", tool_call_id="tool-1"
    )
    context.hooks["post_tool_call"](
        tool_name="pytest", result={"summary": "2 passed"}, session_id="session-1", tool_call_id="tool-1"
    )
    context.hooks["subagent_start"](
        session_id="session-1", parent_turn_id="turn-1", child_session_id="child-1", child_role="reviewer", child_goal="review changes"
    )
    context.hooks["subagent_stop"](
        session_id="session-1", parent_turn_id="turn-1", child_session_id="child-1", child_role="reviewer", child_status="completed", child_summary="done"
    )

    tasks = [event["data"] for event in transport.events if event["type"] == "task.upsert"]
    assert [task["status"] for task in tasks[:2]] == ["running", "completed"]
    assert tasks[0]["session_id"] == "session-1"
    assert tasks[-1]["kind"] == "subagent"
    assert "pytest" in tasks[0]["title"]


def test_hermes_stream_coalesces_only_by_reliable_turn_id():
    context = FakeContext()
    transport = FakeTransport()
    bridge = HermesBridge(
        context,
        HermesConnectorConfig(
            endpoint="ws://127.0.0.1:30002/ws/v1/connector",
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
            endpoint="ws://127.0.0.1:30002/ws/v1/connector",
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
