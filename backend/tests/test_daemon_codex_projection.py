"""Projection of daemon-owned Codex notifications into App-side handlers."""
from __future__ import annotations

import asyncio

import pytest

from astrorder.config import Settings
from astrorder.daemon.bridge import DaemonBridge, DaemonBridgeError
from astrorder.daemon.bridge.codex_projection import CodexNativeFrameRouter
from astrorder.daemon.runtimes.codex.runtime import CodexDaemonRuntime, CodexDaemonRuntimeConfig
from astrorder.daemon.session_daemon import SessionDaemon
from astrorder.core.events import EventHub
from astrorder.service import ControlService
from astrorder.store import Store


@pytest.mark.asyncio
async def test_codex_native_frame_router_routes_only_matching_agent_and_thread(tmp_path):
    settings = Settings(
        database_url=f"sqlite:///{tmp_path}/projection.sqlite3",
        auto_connect_local_hermes=False,
    )
    daemon = SessionDaemon(capacity=8, daemon_id="daemon-projection")
    daemon.record(
        "thread-a",
        "codex.notification",
        {
            "agent_id": "codex-a",
            "frame": {
                "method": "turn/completed",
                "params": {"threadId": "thread-a", "turn": {"id": "turn-a", "status": "completed"}},
            },
        },
    )
    daemon.record(
        "thread-b",
        "codex.notification",
        {
            "agent_id": "codex-missing",
            "frame": {
                "method": "turn/completed",
                "params": {"threadId": "thread-b", "turn": {"id": "turn-b", "status": "completed"}},
            },
        },
    )
    server = await daemon.serve("127.0.0.1", 0)
    endpoint = f"ws://127.0.0.1:{server.sockets[0].getsockname()[1]}"
    store = Store(settings)
    bridge = DaemonBridge(store, ControlService(store, EventHub(), settings), endpoint)
    delivered: list[dict[str, object]] = []
    router = CodexNativeFrameRouter(bridge)
    unregister = router.register("codex-a", lambda frame: delivered.append(dict(frame)))
    try:
        with pytest.raises(DaemonBridgeError, match="Codex native frame agent is not registered"):
            await bridge.synchronize_once()

        assert delivered == [
            {
                "method": "turn/completed",
                "params": {"threadId": "thread-a", "turn": {"id": "turn-a", "status": "completed"}},
            }
        ]
        assert store.get_daemon_checkpoint("daemon-projection", "thread-a") == 1
        assert store.get_daemon_checkpoint("daemon-projection", "thread-b") == 0
    finally:
        unregister()
        router.close()
        store.close()
        server.close()
        await server.wait_closed()


@pytest.mark.asyncio
async def test_daemon_owned_codex_notification_replays_through_authenticated_bridge(tmp_path):
    class FakeCodexAppServer:
        instance = None

        def __init__(self, _config, on_notification, **_kwargs):
            self.on_notification = on_notification
            self.stopped = False
            type(self).instance = self

        def start(self):
            return None

        def stop(self):
            self.stopped = True

        def send(self, _message):
            return None

        def request(self, method, params, timeout=30):
            del timeout
            if method == "initialize":
                return {"codexHome": "C:/fixture/.codex"}
            if method == "thread/resume":
                return {"thread": {"id": params["threadId"]}}
            if method == "turn/start":
                return {"turn": {"id": "turn-1", "status": "inProgress"}}
            raise AssertionError(method)

    settings = Settings(
        database_url=f"sqlite:///{tmp_path}/vertical.sqlite3",
        auto_connect_local_hermes=False,
    )
    daemon = SessionDaemon(
        capacity=8,
        daemon_id="daemon-vertical",
        secret="test-only-daemon-secret",
    )
    runtime = CodexDaemonRuntime(
        CodexDaemonRuntimeConfig(
            executable="fixture-codex",
            workspace=tmp_path,
            allowed_workspaces=(tmp_path,),
            agent_id="daemon-codex",
            agent_name="Daemon Codex",
        ),
        emit=daemon.publish,
        client_factory=FakeCodexAppServer,
    )
    daemon.register_runtime("codex", runtime)
    server = await daemon.serve("127.0.0.1", 0)
    endpoint = f"ws://127.0.0.1:{server.sockets[0].getsockname()[1]}"
    store = Store(settings)
    bridge = DaemonBridge(
        store,
        ControlService(store, EventHub(), settings),
        endpoint,
        secret="test-only-daemon-secret",
    )
    router = CodexNativeFrameRouter(bridge)
    delivered: list[dict[str, object]] = []
    unregister = router.register("daemon-codex", lambda frame: delivered.append(dict(frame)))
    try:
        await bridge.request_control(
            "session.spawn",
            {"session_id": "thread-1", "agent_type": "codex", "params": {}},
        )
        await bridge.request_control(
            "session.send",
            {"session_id": "thread-1", "prompt": "finish safely", "params": {}},
        )
        FakeCodexAppServer.instance.on_notification(
            {
                "method": "turn/completed",
                "params": {
                    "threadId": "thread-1",
                    "turn": {"id": "turn-1", "status": "completed"},
                },
            }
        )

        deadline = asyncio.get_running_loop().time() + 1
        while daemon.status()["thread-1"]["max_seq_id"] != 1:
            if asyncio.get_running_loop().time() >= deadline:
                raise AssertionError("Codex notification was not appended to the daemon WAL")
            await asyncio.sleep(0.01)
        report = await bridge.synchronize_once()

        assert report.replayed_frames == 1
        assert delivered == [
            {
                "method": "turn/completed",
                "params": {
                    "threadId": "thread-1",
                    "turn": {"id": "turn-1", "status": "completed"},
                },
            }
        ]
        assert store.get_daemon_checkpoint("daemon-vertical", "thread-1") == 1
    finally:
        unregister()
        router.close()
        await runtime.shutdown()
        store.close()
        server.close()
        await server.wait_closed()


@pytest.mark.asyncio
async def test_codex_completion_recorded_while_app_is_offline_replays_once(tmp_path):
    class FakeCodexAppServer:
        instance = None

        def __init__(self, _config, on_notification, **_kwargs):
            self.on_notification = on_notification
            type(self).instance = self

        def start(self):
            return None

        def stop(self):
            return None

        def send(self, _message):
            return None

        def request(self, method, params, timeout=30):
            del timeout
            if method == "initialize":
                return {"codexHome": "C:/fixture/.codex"}
            if method == "thread/resume":
                return {"thread": {"id": params["threadId"]}}
            if method == "turn/start":
                return {"turn": {"id": "turn-offline", "status": "inProgress"}}
            raise AssertionError(method)

    settings = Settings(
        database_url=f"sqlite:///{tmp_path}/offline.sqlite3",
        auto_connect_local_hermes=False,
    )
    daemon = SessionDaemon(capacity=8, daemon_id="daemon-offline", secret="test-only-daemon-secret")
    runtime = CodexDaemonRuntime(
        CodexDaemonRuntimeConfig(
            executable="fixture-codex",
            workspace=tmp_path,
            allowed_workspaces=(tmp_path,),
            agent_id="daemon-codex",
            agent_name="Daemon Codex",
        ),
        emit=daemon.publish,
        client_factory=FakeCodexAppServer,
    )
    daemon.register_runtime("codex", runtime)
    server = await daemon.serve("127.0.0.1", 0)
    endpoint = f"ws://127.0.0.1:{server.sockets[0].getsockname()[1]}"
    store = Store(settings)
    bridge = DaemonBridge(store, ControlService(store, EventHub(), settings), endpoint, secret="test-only-daemon-secret")
    router = CodexNativeFrameRouter(bridge)
    delivered: list[dict[str, object]] = []
    unregister = router.register("daemon-codex", lambda frame: delivered.append(dict(frame)))
    try:
        await bridge.request_control("session.spawn", {"session_id": "thread-offline", "agent_type": "codex", "params": {}})
        await bridge.request_control("session.send", {"session_id": "thread-offline", "prompt": "finish while offline", "params": {}})
        first = await bridge.synchronize_once()
        assert first.replayed_frames == 0
        delivered.clear()

        FakeCodexAppServer.instance.on_notification(
            {"method": "turn/completed", "params": {"threadId": "thread-offline", "turn": {"id": "turn-offline", "status": "completed"}}}
        )
        deadline = asyncio.get_running_loop().time() + 1
        while daemon.status()["thread-offline"]["max_seq_id"] != 1:
            if asyncio.get_running_loop().time() >= deadline:
                raise AssertionError("offline completion was not retained by the daemon")
            await asyncio.sleep(0.01)

        report = await bridge.synchronize_once()
        again = await bridge.synchronize_once()
        assert report.replayed_frames == 1
        assert again.replayed_frames == 0
        assert delivered == [
            {"method": "turn/completed", "params": {"threadId": "thread-offline", "turn": {"id": "turn-offline", "status": "completed"}}}
        ]
        assert store.get_daemon_checkpoint("daemon-offline", "thread-offline") == 1
        assert store.list_messages("daemon-codex", "thread-offline", None, 10)[0] == []
    finally:
        unregister()
        router.close()
        await runtime.shutdown()
        store.close()
        server.close()
        await server.wait_closed()


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
    from astrorder.daemon.runtimes.codex.control import DaemonCodexController

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
