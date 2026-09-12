"""Daemon-owned Codex transport tests; all app-server instances are inert fakes."""
from __future__ import annotations

import asyncio
import json
from typing import ClassVar

import pytest
import websockets

from astrorder.daemon.codex_runtime import CodexDaemonRuntime, CodexDaemonRuntimeConfig
from astrorder.daemon.session_daemon import SessionDaemon, create_session_daemon


class FakeCodexAppServer:
    instances: ClassVar[list[FakeCodexAppServer]] = []

    def __init__(self, config, on_notification, *, on_close=None, environment=None):
        self.config = config
        self.on_notification = on_notification
        self.on_close = on_close
        self.environment = environment
        self.calls: list[tuple[str, object]] = []
        self.started = False
        self.stopped = False
        type(self).instances.append(self)

    def start(self) -> None:
        self.started = True

    def stop(self) -> None:
        self.stopped = True

    def send(self, message) -> None:
        self.calls.append(("send", dict(message)))

    def request(self, method, params, timeout=30):
        del timeout
        self.calls.append((method, dict(params)))
        if method == "initialize":
            return {"codexHome": "C:/fixture/.codex"}
        if method == "thread/resume":
            return {
                "thread": {"id": params["threadId"]},
                "model": "fixture-model",
                "modelProvider": "fixture-provider",
            }
        if method == "thread/start":
            return {
                "thread": {"id": "native-created-1", "cwd": params["cwd"]},
                "model": "fixture-model",
                "modelProvider": "fixture-provider",
            }
        if method == "thread/name/set":
            return {}
        if method == "thread/read":
            return {"thread": {"id": params["threadId"], "name": "Daemon-created session"}}
        if method == "turn/start":
            return {"turn": {"id": "turn-1", "status": "inProgress"}}
        if method == "thread/settings/update":
            return {}
        raise AssertionError(method)

    def notify(self, frame: dict[str, object]) -> None:
        self.on_notification(frame)


async def wait_for_status(daemon: SessionDaemon, session_id: str, status: str) -> None:
    deadline = asyncio.get_running_loop().time() + 1
    while daemon.status()[session_id]["status"] != status:
        if asyncio.get_running_loop().time() >= deadline:
            raise AssertionError(f"{session_id} did not reach {status}")
        await asyncio.sleep(0.01)


def test_codex_daemon_factory_requires_authenticated_ipc(tmp_path):
    config = CodexDaemonRuntimeConfig(
        executable="fixture-codex",
        workspace=tmp_path,
        allowed_workspaces=(tmp_path,),
        agent_id="daemon-codex",
        agent_name="Daemon Codex",
    )

    with pytest.raises(ValueError, match="secret"):
        create_session_daemon(capacity=8, codex_config=config)

    daemon = create_session_daemon(
        capacity=8,
        secret="test-only-daemon-secret",
        codex_config=config,
    )
    assert "codex" in daemon._runtime_registry


@pytest.mark.asyncio
async def test_daemon_owned_codex_runtime_keeps_native_transport_and_wals_notifications(tmp_path):
    FakeCodexAppServer.instances.clear()
    daemon = SessionDaemon(
        capacity=8,
        daemon_id="daemon-codex-runtime",
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

    try:
        async with websockets.connect(endpoint) as socket:
            await socket.send(
                json.dumps(
                    {
                        "action": "daemon.handshake",
                        "request_id": "handshake-1",
                        "secret": "test-only-daemon-secret",
                    }
                )
            )
            assert json.loads(await socket.recv())["action"] == "daemon.handshake.result"
            await socket.send(
                json.dumps(
                    {
                        "action": "session.spawn",
                        "request_id": "spawn-1",
                        "session_id": "native-thread-1",
                        "agent_type": "codex",
                        "params": {},
                    }
                )
            )
            spawned = json.loads(await socket.recv())
            assert spawned["result"] == {
                "status": "idle",
                "model": "fixture-model",
                "provider": "fixture-provider",
            }

            await socket.send(
                json.dumps(
                    {
                        "action": "session.send",
                        "request_id": "send-1",
                        "session_id": "native-thread-1",
                        "input": [
                            {"type": "text", "text": "same text is not an identity"},
                            {"type": "image", "url": "data:image/png;base64,aGVsbG8="},
                        ],
                        "params": {
                            "approvalPolicy": "on-request",
                            "sandboxPolicy": {"type": "workspaceWrite"},
                        },
                    }
                )
            )
            sent = json.loads(await socket.recv())
            assert sent["result"] == {"status": "running", "turn_id": "turn-1", "accepted": True}

            await socket.send(
                json.dumps(
                    {
                        "action": "session.settings",
                        "request_id": "settings-1",
                        "session_id": "native-thread-1",
                        "model": "fixture-next-model",
                        "effort": "high",
                    }
                )
            )
            updated = json.loads(await socket.recv())
            assert updated["result"] == {"status": "running", "accepted": True}

        client = FakeCodexAppServer.instances[0]
        assert client.started is True
        assert client.calls == [
            (
                "initialize",
                {
                    "clientInfo": {
                        "name": "astrorder-daemon",
                        "title": "Astrorder Session Daemon",
                        "version": "0.1.0",
                    },
                    "capabilities": {"experimentalApi": True},
                },
            ),
            ("send", {"method": "initialized", "params": {}}),
            ("thread/resume", {"threadId": "native-thread-1", "excludeTurns": True}),
            (
                "turn/start",
                {
                    "threadId": "native-thread-1",
                    "input": [
                        {"type": "text", "text": "same text is not an identity"},
                        {"type": "image", "url": "data:image/png;base64,aGVsbG8="},
                    ],
                    "approvalPolicy": "on-request",
                    "sandboxPolicy": {"type": "workspaceWrite"},
                },
            ),
            (
                "thread/settings/update",
                {"threadId": "native-thread-1", "model": "fixture-next-model", "effort": "high"},
            ),
        ]
        assert daemon.status()["native-thread-1"]["status"] == "running"

        client.notify(
            {
                "method": "turn/completed",
                "params": {
                    "threadId": "native-thread-1",
                    "turn": {"id": "turn-1", "status": "completed"},
                },
            }
        )
        await wait_for_status(daemon, "native-thread-1", "idle")
        replay = daemon.sync({"native-thread-1": 0})["native-thread-1"]
        assert replay["frames"][-1]["event"] == "codex.notification"
        assert replay["frames"][-1]["payload"] == {
            "agent_id": "daemon-codex",
            "frame": {
                "method": "turn/completed",
                "params": {
                    "threadId": "native-thread-1",
                    "turn": {"id": "turn-1", "status": "completed"},
                },
            }
        }
    finally:
        await runtime.shutdown()
        assert FakeCodexAppServer.instances[0].stopped is True
        server.close()
        await server.wait_closed()


@pytest.mark.asyncio
async def test_daemon_owned_codex_runtime_creates_native_thread_without_app_server_owner(tmp_path):
    FakeCodexAppServer.instances.clear()
    daemon = SessionDaemon(capacity=8, secret="test-only-daemon-secret")
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
    try:
        async with websockets.connect(endpoint) as socket:
            await socket.send(json.dumps({"action": "daemon.handshake", "secret": "test-only-daemon-secret"}))
            await socket.recv()
            await socket.send(
                json.dumps(
                    {
                        "action": "session.create",
                        "request_id": "create-1",
                        "agent_type": "codex",
                        "cwd": str(tmp_path),
                        "title": "Daemon-created session",
                        "ephemeral": True,
                    }
                )
            )
            created = json.loads(await socket.recv())
        assert created["action"] == "session.create.result"
        assert created["result"] == {
            "session_id": "native-created-1",
            "status": "idle",
            "model": "fixture-model",
            "provider": "fixture-provider",
        }
        assert daemon.status()["native-created-1"]["status"] == "idle"
        client = FakeCodexAppServer.instances[0]
        assert client.config.thread_id is None
        assert ("thread/start", {"cwd": str(tmp_path), "ephemeral": True, "persistExtendedHistory": False}) in client.calls
        assert not any(
            method in {"thread/name/set", "thread/read"} for method, _params in client.calls
        )
    finally:
        await runtime.shutdown()
        server.close()
        await server.wait_closed()
