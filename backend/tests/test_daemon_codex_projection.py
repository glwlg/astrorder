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
