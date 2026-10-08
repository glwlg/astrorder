"""Projection of daemon-owned Codex notifications into App-side handlers."""
from __future__ import annotations


import pytest

from astrorder.config import Settings
from astrorder.daemon.bridge import DaemonBridge, DaemonBridgeError
from astrorder.daemon.bridge.codex_projection import CodexNativeFrameRouter
from astrorder.core.events import EventHub
from astrorder.service import ControlService
from astrorder.store import Store


@pytest.mark.parametrize("registered", [False, True])
def test_codex_replay_projects_tool_result_and_final_answer(tmp_path, registered):
    settings = Settings(
        database_url=f"sqlite:///{tmp_path}/transcript.sqlite3",
        auto_connect_local_hermes=False,
    )
    store = Store(settings)
    store.upsert_agent(
        {
            "id": "daemon-codex",
            "kind": "codex",
            "name": "Daemon Codex",
            "status": "ready",
            "capabilities": ["chat"],
            "source_id": "daemon-codex",
            "runtime_id": "daemon-codex",
            "control_state": "owned",
        }
    )
    store.upsert_session(
        {
            "id": "thread-1",
            "agent_id": "daemon-codex",
            "title": "离线回复",
            "workspace": None,
            "status": "running",
            "updated_at": "2026-10-07T00:00:00Z",
            "source_id": "daemon-codex",
            "source_session_id": "thread-1",
            "history_state": "live",
            "control_state": "owned",
        }
    )
    bridge = DaemonBridge(store, ControlService(store, EventHub(), settings), "ws://127.0.0.1:1")
    router = CodexNativeFrameRouter(bridge)
    bridge._daemon_id = "daemon-test"
    from astrorder.native.codex import CodexConnection
    from astrorder.daemon.clients.codex.control import DaemonCodexController

    connection = CodexConnection(settings, store, bridge.service)
    connection.agent_id = "daemon-codex"
    controller = DaemonCodexController(bridge, router, connection)
    if registered:
        controller.activate()
    else:
        with pytest.raises(DaemonBridgeError, match="not registered"):
            router._handle_frame("thread-1", {
                "agent_id": "daemon-codex",
                "frame": {"method": "item/completed", "params": {
                    "threadId": "thread-1",
                    "item": {"id": "unregistered", "type": "agentMessage", "text": "must wait"},
                }},
            })
        assert store.list_messages("daemon-codex", "thread-1", None, 10)[0] == []
        controller.activate()
    try:
        command, _ = store.create_command(command={"agent_id": "daemon-codex", "session_id": "thread-1", "id": "browser-send", "action": "send", "text": "hello"}, attachments=[], initial_state="accepted")
        router._handle_frame("thread-1", {
            "agent_id": "daemon-codex", "command_id": command["id"],
            "frame": {"method": "item/completed", "params": {
                "threadId": "thread-1", "turnId": "turn-1",
                "item": {"id": "user-echo", "type": "userMessage", "content": [{"type": "text", "text": "hello"}]},
            }},
        })
        assert store.get_message("daemon-codex", "thread-1", "user-echo")["command_id"] == command["id"]
        bridge._project_frame(
            "daemon-test",
            "thread-1",
            1,
            {
                "session_id": "thread-1",
                "seq_id": 1,
                "event": "codex.notification",
                "status": "running",
                "payload": {
                    "agent_id": "daemon-codex",
                    "frame": {
                        "method": "item/completed",
                        "params": {
                            "threadId": "thread-1",
                            "item": {
                                "id": "tool-1",
                                "type": "commandExecution",
                                "command": "echo hi",
                                "aggregatedOutput": "hi",
                                "status": "completed",
                            },
                        },
                    },
                },
            },
        )
        bridge._project_frame(
            "daemon-test",
            "thread-1",
            2,
            {
                "session_id": "thread-1",
                "seq_id": 2,
                "event": "codex.notification",
                "status": "idle",
                "payload": {
                    "agent_id": "daemon-codex",
                    "frame": {
                        "method": "item/completed",
                        "params": {
                            "threadId": "thread-1",
                            "item": {"id": "answer-1", "type": "agentMessage", "text": "完成了"},
                        },
                    },
                },
            },
        )
        messages, _cursor = store.list_messages("daemon-codex", "thread-1", None, 10)
        assert [(row["role"], row["text"]) for row in messages] == [("user", "hello"), ("tool", "hi"), ("assistant", "完成了")]
        assert messages[1]["tool"]["arguments"]["command"] == "echo hi"
        assert store.get_session("daemon-codex", "thread-1")["status"] == "running"
        bridge._project_frame("daemon-test", "thread-1", 3, {
            "session_id": "thread-1", "seq_id": 3, "event": "codex.notification",
            "payload": {"agent_id": "daemon-codex", "frame": {
                "method": "turn/completed", "params": {
                    "threadId": "thread-1", "turn": {"id": "turn-1", "status": "completed"},
                },
            }},
        })
        assert store.get_session("daemon-codex", "thread-1")["status"] == "idle"
    finally:
        controller.close()
        router.close()
        store.close()
