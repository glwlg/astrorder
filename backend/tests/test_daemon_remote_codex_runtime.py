from __future__ import annotations

from pathlib import PurePosixPath
from typing import ClassVar

import pytest
from astrorder_codex_connector.app_server import CodexRpcRejected

from astrorder.daemon.remote_codex_runtime import RemoteCodexDaemonRuntime
from astrorder.daemon.ssh_runtime import SshDaemonRuntimeRegistry


class FakeCodexAppServer:
    instances: ClassVar[list[FakeCodexAppServer]] = []

    def __init__(self, config, on_notification, **kwargs):
        self.config = config
        self.on_notification = on_notification
        self.kwargs = kwargs
        self.calls = []
        self.started = False
        self.stopped = False
        type(self).instances.append(self)

    def start(self):
        self.started = True

    def stop(self):
        self.stopped = True

    def send(self, message):
        self.calls.append(("send", dict(message)))

    def request(self, method, params, timeout=30):
        del timeout
        self.calls.append((method, dict(params)))
        if method == "initialize":
            return {"codexHome": "/home/operator/.codex"}
        if method == "thread/resume":
            return {
                "thread": {"id": params["threadId"]},
                "model": "remote-model",
                "modelProvider": "remote-provider",
            }
        raise AssertionError(method)


class PathResumeCodexAppServer(FakeCodexAppServer):
    def request(self, method, params, timeout=30):
        if method == "thread/resume" and "path" not in params:
            self.calls.append((method, dict(params)))
            raise CodexRpcRejected({"code": -32600, "message": "thread not found"})
        return super().request(method, params, timeout)


@pytest.mark.asyncio
async def test_remote_codex_registry_binds_exact_connection_and_preserves_posix_workspace():
    FakeCodexAppServer.instances.clear()
    emitted = []

    async def emit(*args, **kwargs):
        emitted.append((args, kwargs))
        return {}

    def factory(connection_id, raw_settings):
        return RemoteCodexDaemonRuntime(
            connection_id,
            raw_settings,
            emit=emit,
            client_factory=FakeCodexAppServer,
        )

    registry = SshDaemonRuntimeRegistry(factory)
    registry.set_emitter(emit)
    request = {
        "session_id": "remote-thread-1",
        "cwd": "/home/operator/workspace/project",
        "params": {
            "connection_id": "ssh-debian",
            "ssh_settings": {
                "display_name": "Debian",
                "host": "debian.example",
                "port": 22,
                "user": "operator",
                "codex_executable": "/home/operator/.local/bin/codex",
            },
        },
    }

    spawned = await registry.spawn(request)

    assert spawned == {
        "status": "idle",
        "model": "remote-model",
        "provider": "remote-provider",
    }
    client = FakeCodexAppServer.instances[0]
    assert client.started is True
    assert client.config.agent_id == "ssh-codex-ssh-debian"
    assert client.config.agent_name == "Debian · Codex"
    assert client.config.workspace == PurePosixPath("/home/operator/workspace/project")

    await registry.disconnect_session("remote-thread-1")
    assert client.stopped is True


@pytest.mark.asyncio
async def test_remote_codex_registry_rejects_connection_config_changes_while_owned():
    async def emit(*_args, **_kwargs):
        return {}

    registry = SshDaemonRuntimeRegistry(
        lambda connection_id, settings: RemoteCodexDaemonRuntime(
            connection_id,
            settings,
            emit=emit,
            client_factory=FakeCodexAppServer,
        )
    )
    base = {
        "session_id": "remote-thread-1",
        "cwd": "/home/operator/workspace/project",
        "params": {
            "connection_id": "ssh-debian",
            "ssh_settings": {
                "display_name": "Debian",
                "host": "debian.example",
                "port": 22,
                "user": "operator",
                "codex_executable": "/home/operator/.local/bin/codex",
            },
        },
    }
    await registry.spawn(base)

    changed = {
        **base,
        "session_id": "remote-thread-2",
        "params": {
            **base["params"],
            "ssh_settings": {**base["params"]["ssh_settings"], "host": "other.example"},
        },
    }
    with pytest.raises(Exception, match="settings changed"):
        await registry.spawn(changed)

    await registry.shutdown()


@pytest.mark.asyncio
async def test_remote_codex_runtime_uses_verified_rollout_path_after_native_id_lookup_fails(monkeypatch):
    async def emit(*_args, **_kwargs):
        return {}

    runtime = RemoteCodexDaemonRuntime(
        "ssh-debian",
        {
            "display_name": "Debian",
            "host": "debian.example",
            "port": 22,
            "user": "operator",
            "codex_executable": "/home/operator/.local/bin/codex",
        },
        emit=emit,
        client_factory=PathResumeCodexAppServer,
    )
    monkeypatch.setattr(runtime, "_rollout_path", lambda _session_id: "/home/operator/.codex/rollout.jsonl")

    await runtime.spawn({"session_id": "remote-thread-1", "cwd": "/home/operator/workspace/project"})

    client = PathResumeCodexAppServer.instances[-1]
    resume_calls = [params for method, params in client.calls if method == "thread/resume"]
    assert resume_calls == [
        {"threadId": "remote-thread-1", "excludeTurns": True},
        {
            "threadId": "remote-thread-1",
            "path": "/home/operator/.codex/rollout.jsonl",
            "excludeTurns": True,
        },
    ]
    await runtime.shutdown()
