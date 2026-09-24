"""Daemon-owned Codex transport tests; all app-server instances are inert fakes."""
from __future__ import annotations

import asyncio
import json
import time
from typing import ClassVar

import pytest
import websockets
from astrorder_codex_connector.app_server import CodexRpcRejected

from astrorder.daemon.runtimes.codex.runtime import (
    CodexDaemonRuntime,
    CodexDaemonRuntimeConfig,
    forward_codex_desktop_stops,
)
from astrorder.daemon.session_daemon import (
    DaemonProtocolError,
    SessionDaemon,
    create_session_daemon,
)


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

    def is_alive(self) -> bool:
        return self.started and not self.stopped

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
        if method in {"thread/start", "thread/fork"}:
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
        if method == "turn/steer":
            return {}
        if method == "thread/settings/update":
            return {}
        if method == "thread/delete":
            return {}
        if method == "thread/list":
            return {"data": []}
        raise AssertionError(method)

    def notify(self, frame: dict[str, object]) -> None:
        self.on_notification(frame)


async def wait_for_status(daemon: SessionDaemon, session_id: str, status: str) -> None:
    deadline = asyncio.get_running_loop().time() + 1
    while daemon.status()[session_id]["status"] != status:
        if asyncio.get_running_loop().time() >= deadline:
            raise AssertionError(f"{session_id} did not reach {status}")
        await asyncio.sleep(0.01)


@pytest.mark.asyncio
async def test_codex_runtime_routes_desktop_submission_without_app_server(tmp_path):
    calls = []

    async def submit(thread_id, inputs):
        calls.append((thread_id, inputs))
        return {"accepted": True, "transport": "codex-desktop-cdp"}

    runtime = CodexDaemonRuntime(
        CodexDaemonRuntimeConfig(
            executable="fixture-codex",
            workspace=tmp_path,
            allowed_workspaces=(tmp_path,),
            agent_id="daemon-codex",
            agent_name="Daemon Codex",
        ),
        emit=lambda *_args, **_kwargs: None,
        desktop_submit=submit,
    )

    result = await runtime.query(
        {
            "method": "desktop/submit",
            "request_params": {
                "threadId": "thread-1",
                "input": [{"type": "text", "text": "继续"}],
            },
        }
    )

    assert result == {"accepted": True, "transport": "codex-desktop-cdp"}
    assert calls == [("thread-1", [{"type": "text", "text": "继续"}])]


@pytest.mark.asyncio
async def test_codex_desktop_stop_is_forwarded_as_native_completion(tmp_path):
    home = tmp_path / ".codex"
    events = home / "astrorder-observer" / "events"
    events.mkdir(parents=True)
    config = CodexDaemonRuntimeConfig(
        executable="fixture-codex",
        workspace=tmp_path,
        allowed_workspaces=(tmp_path,),
        agent_id="daemon-codex",
        agent_name="Daemon Codex",
        environment={"CODEX_HOME": str(home)},
    )
    stopping = asyncio.Event()
    forwarded = asyncio.Event()
    calls = []

    async def emit(*args, **kwargs):
        calls.append((args, kwargs))
        forwarded.set()
        return {}

    task = asyncio.create_task(
        forward_codex_desktop_stops(config, emit, stopping, poll_interval=0.01)
    )
    await asyncio.sleep(0.02)
    event_id = "a" * 32
    (events / f"{event_id}.json").write_text(
        json.dumps(
            {
                "id": event_id,
                "event": "Stop",
                "session_id": "thread-1",
                "turn_id": "turn-1",
                "observed_at": time.time(),
            }
        ),
        encoding="utf-8",
    )
    await asyncio.wait_for(forwarded.wait(), timeout=1)
    stopping.set()
    await task

    assert calls[0][0][:2] == ("thread-1", "codex.notification")
    assert calls[0][0][2]["frame"]["params"]["turn"] == {
        "id": "turn-1",
        "status": "completed",
    }


@pytest.mark.asyncio
async def test_codex_runtime_preserves_native_query_rejection(tmp_path):
    class RejectingCatalog(FakeCodexAppServer):
        def request(self, method, params, timeout=30):
            if method == "thread/items/list":
                raise CodexRpcRejected(
                    {"code": -32601, "message": "thread/items/list is not supported yet"}
                )
            return super().request(method, params, timeout)

    runtime = CodexDaemonRuntime(
        CodexDaemonRuntimeConfig(
            executable="fixture-codex",
            workspace=tmp_path,
            allowed_workspaces=(tmp_path,),
            agent_id="daemon-codex",
            agent_name="Daemon Codex",
        ),
        emit=lambda *_args, **_kwargs: None,
        client_factory=RejectingCatalog,
    )

    with pytest.raises(DaemonProtocolError, match=r"-32601.*not supported"):
        await runtime.query(
            {
                "method": "thread/items/list",
                "request_params": {"threadId": "thread-1"},
            }
        )

    await runtime.shutdown()

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
async def test_codex_runtime_reconnects_a_closed_transport_before_sending(tmp_path):
    FakeCodexAppServer.instances.clear()
    runtime = CodexDaemonRuntime(
        CodexDaemonRuntimeConfig(
            executable="fixture-codex",
            workspace=tmp_path,
            allowed_workspaces=(tmp_path,),
            agent_id="daemon-codex",
            agent_name="Daemon Codex",
        ),
        emit=lambda *_args, **_kwargs: None,
        client_factory=FakeCodexAppServer,
    )
    await runtime.spawn({"session_id": "native-thread-1"})
    first = FakeCodexAppServer.instances[0]
    first.stopped = True

    result = await runtime.command(
        "session.send",
        {
            "session_id": "native-thread-1",
            "input": [{"type": "text", "text": "after restart"}],
        },
    )

    assert result == {"status": "running", "turn_id": "turn-1", "accepted": True}
    assert len(FakeCodexAppServer.instances) == 2
    assert ("thread/resume", {"threadId": "native-thread-1", "excludeTurns": True}) in FakeCodexAppServer.instances[1].calls


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
                            {"type": "mention", "name": "KubernetesTerminalView.vue", "path": "P:/workspace/src/KubernetesTerminalView.vue"},
                            {"type": "skill", "name": "openai-docs", "path": "C:/Users/luwei/.codex/skills/openai-docs"},
                        ],
                        "params": {
                            "approvalPolicy": "on-request",
                            "sandboxPolicy": {"type": "workspaceWrite"},
                            "model": "fixture-next-model",
                        },
                    }
                )
            )
            sent = json.loads(await socket.recv())
            assert sent["result"] == {"status": "running", "turn_id": "turn-1", "accepted": True}

            await socket.send(
                json.dumps(
                    {
                        "action": "session.steer",
                        "request_id": "steer-1",
                        "session_id": "native-thread-1",
                        "turn_id": "turn-1",
                        "input": [{"type": "text", "text": "补充要求"}],
                    }
                )
            )
            steered = json.loads(await socket.recv())
            assert steered["result"] == {"status": "running", "turn_id": "turn-1", "accepted": True}

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
            assert updated["result"] == {
                "status": "running",
                "accepted": True,
                "model": "fixture-next-model",
                "effort": "high",
            }

            await socket.send(
                json.dumps(
                    {
                        "action": "session.settings",
                        "request_id": "max-settings-1",
                        "session_id": "native-thread-1",
                        "effort": "max",
                    }
                )
            )
            max_updated = json.loads(await socket.recv())
            assert max_updated["result"] == {
                "status": "running",
                "accepted": True,
                "effort": "max",
            }

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
                        {"type": "mention", "name": "KubernetesTerminalView.vue", "path": "P:/workspace/src/KubernetesTerminalView.vue"},
                        {"type": "skill", "name": "openai-docs", "path": "C:/Users/luwei/.codex/skills/openai-docs"},
                    ],
                    "approvalPolicy": "on-request",
                    "sandboxPolicy": {"type": "workspaceWrite"},
                    "model": "fixture-next-model",
                },
            ),
            (
                "turn/steer",
                {
                    "threadId": "native-thread-1",
                    "expectedTurnId": "turn-1",
                    "input": [{"type": "text", "text": "补充要求"}],
                },
            ),
            (
                "thread/settings/update",
                {"threadId": "native-thread-1", "model": "fixture-next-model", "effort": "high"},
            ),
            ("thread/settings/update", {"threadId": "native-thread-1", "effort": "max"}),
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
        async with websockets.connect(endpoint) as socket:
            await socket.send(json.dumps({"action": "daemon.handshake", "secret": "test-only-daemon-secret"}))
            await socket.recv()
            await socket.send(json.dumps({
                "action": "session.delete",
                "request_id": "delete-1",
                "session_id": "native-created-1",
            }))
            deleted = json.loads(await socket.recv())
        assert deleted["result"]["deleted"] == "native-created-1"
        assert "native-created-1" not in daemon.status()
        assert client.stopped is True
    finally:
        await runtime.shutdown()
        server.close()
        await server.wait_closed()

@pytest.mark.asyncio
async def test_ephemeral_codex_fork_omits_incompatible_goal_continuation_flag(tmp_path):
    FakeCodexAppServer.instances.clear()
    runtime = CodexDaemonRuntime(
        CodexDaemonRuntimeConfig(
            executable="fixture-codex",
            workspace=tmp_path,
            allowed_workspaces=(tmp_path,),
            agent_id="daemon-codex",
            agent_name="Daemon Codex",
        ),
        emit=lambda *_args, **_kwargs: None,
        client_factory=FakeCodexAppServer,
    )

    created = await runtime.create(
        {
            "cwd": str(tmp_path),
            "parent_session_id": "source-thread",
            "ephemeral": True,
        }
    )

    assert created["session_id"] == "native-created-1"
    assert (
        "thread/fork",
        {
            "threadId": "source-thread",
            "cwd": str(tmp_path),
            "ephemeral": True,
            "excludeTurns": True,
        },
    ) in FakeCodexAppServer.instances[0].calls
    await runtime.shutdown()
