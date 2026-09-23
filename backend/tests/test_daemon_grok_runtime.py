import asyncio

import pytest

from astrorder.config import Settings
from astrorder.daemon.grok_runtime import GrokDaemonRuntime, GrokDaemonRuntimeConfig
from astrorder.core.events import EventHub
from connectors.grok.connection import GrokProjection
from astrorder.service import ControlService
from astrorder.store import Store


class FakeClient:
    def __init__(self, _config, on_notification, **_kwargs):
        self.on_notification = on_notification
        self.stopped = False

    def start(self):
        pass

    def stop(self):
        self.stopped = True

    def send(self, _frame):
        pass

    def request(self, method, params, timeout=30):
        if method == "initialize":
            return {"_meta": {"modelState": {
                "currentModelId": "grok-4.6",
                "availableModels": [{
                    "modelId": "grok-4.6", "name": "Grok 4.6",
                    "_meta": {"reasoningEfforts": [{"value": "high"}]},
                }],
            }}}
        if method == "session/load":
            return {
                "sessionId": params["sessionId"],
                "configOptions": [
                    {"id": "model", "currentValue": "grok-4.6"},
                    {"id": "reasoning_effort", "currentValue": "high"},
                ],
            }
        if method in {"session/set_model", "session/set_config_option"}:
            return {}
        if method == "session/prompt":
            self.on_notification({
                "method": "session/update",
                "params": {
                    "sessionId": params["sessionId"],
                    "update": {"sessionUpdate": "agent_message_chunk", "content": {"text": "ok"}},
                },
                "_meta": {"promptId": "p1"},
            })
            return {"stopReason": "end_turn"}
        raise AssertionError(method)


class FailedClient(FakeClient):
    def request(self, method, params, timeout=30):
        if method == "session/prompt":
            self.on_notification({
                "method": "_x.ai/session/update",
                "params": {
                    "sessionId": params["sessionId"],
                    "update": {
                        "sessionUpdate": "retry_state",
                        "type": "failed",
                        "message": "upstream stream ended before a terminal frame",
                    },
                },
            })
            raise RuntimeError("Grok rejected request (-32603; Internal error)")
        return super().request(method, params, timeout)


@pytest.mark.asyncio
async def test_grok_runtime_emits_agent_scoped_updates_and_completion(tmp_path):
    executable = tmp_path / "grok.exe"
    executable.touch()
    events = []

    async def emit(session_id, event, payload, **kwargs):
        events.append((session_id, event, payload, kwargs))

    runtime = GrokDaemonRuntime(
        GrokDaemonRuntimeConfig(str(executable), tmp_path, (tmp_path,)),
        emit=emit,
        client_factory=FakeClient,
    )
    await runtime.spawn({"session_id": "s1", "cwd": str(tmp_path)})
    result = await runtime.command(
        "session.send", {"session_id": "s1", "command_id": "c1", "prompt": "hello"}
    )
    assert result["status"] == "running"
    for _ in range(100):
        if any(event == "grok.completed" for _, event, _, _ in events):
            break
        await asyncio.sleep(0.01)
    assert [event for _, event, _, _ in events] == ["grok.notification", "grok.completed"]
    assert all(payload["agent_id"] == "local-grok" for _, _, payload, _ in events)
    assert (await runtime.command("session.model.set", {
        "session_id": "s1", "provider": "grok", "model": "grok-4.6",
    }))["model"] == "grok-4.6"
    assert (await runtime.command("session.reasoning.set", {
        "session_id": "s1", "effort": "high",
    }))["effort"] == "high"
    await runtime.shutdown()


@pytest.mark.asyncio
async def test_grok_runtime_reports_native_failure_detail(tmp_path):
    executable = tmp_path / "grok.exe"
    executable.touch()
    events = []

    async def emit(session_id, event, payload, **kwargs):
        events.append((session_id, event, payload, kwargs))

    runtime = GrokDaemonRuntime(
        GrokDaemonRuntimeConfig(str(executable), tmp_path, (tmp_path,)),
        emit=emit,
        client_factory=FailedClient,
    )
    await runtime.spawn({"session_id": "s1", "cwd": str(tmp_path)})
    await runtime.command(
        "session.send", {"session_id": "s1", "command_id": "c1", "prompt": "hello"}
    )
    for _ in range(100):
        completed = next((payload for _, event, payload, _ in events if event == "grok.completed"), None)
        if completed:
            break
        await asyncio.sleep(0.01)
    assert completed["error"] == "upstream stream ended before a terminal frame"
    await runtime.shutdown()


def test_grok_projection_aggregates_stream_chunks(tmp_path):
    class Bridge:
        def __init__(self):
            self.handlers = {}

        def register_native_frame_handler(self, event, handler):
            self.handlers[event] = handler
            return lambda: self.handlers.pop(event, None)

    settings = Settings(database_url=f"sqlite:///{tmp_path}/grok.sqlite3")
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    store.upsert_agent({
        "id": "local-grok", "kind": "grok", "name": "Grok Build",
        "status": "ready", "capabilities": ["chat"],
    })
    store.upsert_session({
        "id": "s1", "agent_id": "local-grok", "title": "test", "status": "idle",
        "updated_at": "2026-01-01T00:00:00Z",
    })
    bridge = Bridge()
    projection = GrokProjection(bridge, store, service)
    for text in ("hel", "lo"):
        bridge.handlers["grok.notification"]("s1", {
            "agent_id": "local-grok",
            "frame": {
                "params": {"update": {"sessionUpdate": "agent_message_chunk", "content": {"text": text}}},
                "_meta": {"promptId": "p1"},
            },
        })
    messages, _ = store.list_messages("local-grok", "s1", None, 10)
    assert [message["text"] for message in messages] == ["hello"]
    projection.close()
    store.close()


def test_grok_available_commands_are_normalized():
    assert GrokDaemonRuntime._commands({
        "sessionUpdate": "available_commands_update",
        "availableCommands": [{
            "name": "/model",
            "description": "Select model",
            "input": {"hint": "model name"},
        }],
    }) == [{
        "name": "model",
        "description": "Select model",
        "input_hint": "model name",
    }]


def test_grok_projection_differentiates_turns_and_merges_tools(tmp_path):
    class Bridge:
        def __init__(self):
            self.handlers = {}

        def register_native_frame_handler(self, event, handler):
            self.handlers[event] = handler
            return lambda: self.handlers.pop(event, None)

    settings = Settings(database_url=f"sqlite:///{tmp_path}/grok.sqlite3")
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    store.upsert_agent({
        "id": "local-grok", "kind": "grok", "name": "Grok Build",
        "status": "ready", "capabilities": ["chat"],
    })
    store.upsert_session({
        "id": "s1", "agent_id": "local-grok", "title": "test", "status": "idle",
        "updated_at": "2026-01-01T00:00:00Z",
    })
    bridge = Bridge()
    projection = GrokProjection(bridge, store, service)

    # Turn 0 user message chunk
    bridge.handlers["grok.notification"]("s1", {
        "agent_id": "local-grok",
        "frame": {
            "params": {
                "update": {
                    "sessionUpdate": "user_message_chunk",
                    "content": {"type": "text", "text": "hello"},
                    "_meta": {"promptIndex": 0},
                },
                "_meta": {"eventId": "ev-0", "agentTimestampMs": 1787280753000},
            },
        },
    })
    # Turn 0 tool call
    bridge.handlers["grok.notification"]("s1", {
        "agent_id": "local-grok",
        "frame": {
            "params": {
                "update": {
                    "sessionUpdate": "tool_call",
                    "toolCallId": "call-1",
                    "title": "run_terminal_command",
                    "rawInput": {"cmd": "ls"},
                },
            },
        },
    })
    # Turn 0 tool call update without title/rawInput
    bridge.handlers["grok.notification"]("s1", {
        "agent_id": "local-grok",
        "frame": {
            "params": {
                "update": {
                    "sessionUpdate": "tool_call_update",
                    "toolCallId": "call-1",
                    "status": "completed",
                    "content": [{"type": "text", "text": "file.txt"}],
                },
            },
        },
    })
    # Turn 1 user message chunk
    bridge.handlers["grok.notification"]("s1", {
        "agent_id": "local-grok",
        "frame": {
            "params": {
                "update": {
                    "sessionUpdate": "user_message_chunk",
                    "content": {"type": "text", "text": "second turn"},
                    "_meta": {"promptIndex": 1},
                },
                "_meta": {"eventId": "ev-1", "agentTimestampMs": 1787280754000},
            },
        },
    })
    # Turn 1 assistant message chunk
    bridge.handlers["grok.notification"]("s1", {
        "agent_id": "local-grok",
        "frame": {
            "params": {
                "update": {
                    "sessionUpdate": "agent_message_chunk",
                    "content": {"type": "text", "text": "reply 2"},
                },
                "_meta": {"promptId": "pid-2", "agentTimestampMs": 1787280755000},
            },
        },
    })

    messages, _ = store.list_messages("local-grok", "s1", None, 10)
    assert len(messages) == 4
    msg_map = {m["id"]: m for m in messages}
    assert msg_map["grok:prompt-0:user"]["text"] == "hello"
    assert msg_map["grok:prompt-1:user"]["text"] == "second turn"
    assert msg_map["grok:pid-2:message"]["text"] == "reply 2"
    tool_msg = msg_map["grok:tool:call-1"]
    assert tool_msg["tool"]["name"] == "run_terminal_command"
    assert tool_msg["tool"]["arguments"] == {"cmd": "ls"}
    assert tool_msg["tool"]["status"] == "completed"

    projection.close()
    store.close()


def test_grok_connection_mutate_session(tmp_path):
    from connectors.grok.connection import GrokConnection

    deleted = []
    conn = GrokConnection(
        None, None, None,
        executable="grok",
        session_reader=lambda _: [],
        session_deleter=lambda sid: deleted.append(sid),
    )
    conn._summaries["s1"] = {"title": "old"}
    conn._summaries["history"] = {"title": "history"}
    conn._attached.add("s1")
    assert conn.open_ids() == ["s1"]
    conn.mutate_session("s1", {"title": "new"})
    assert conn._summaries["s1"]["title"] == "new"

    conn.mutate_session("s1", None)
    assert "s1" not in conn._summaries
    assert deleted == ["s1"]
