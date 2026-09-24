from __future__ import annotations

import asyncio
import threading
from typing import ClassVar

import pytest

from astrorder.daemon.runtimes.pty.runtime import PtyDaemonRuntime, PtyDaemonRuntimeConfig
from astrorder.daemon.session_daemon import SessionDaemon, create_session_daemon


class FakeTerminal:
    instances: ClassVar[list[FakeTerminal]] = []

    def __init__(self, *, workspace=None, ssh_argv=None, remote_workspace=None):
        self.workspace = workspace
        self.ssh_argv = ssh_argv
        self.remote_workspace = remote_workspace
        self.started = False
        self.closed = False
        self.writes: list[str] = []
        self.resizes: list[tuple[int, int]] = []
        self.release = threading.Event()
        type(self).instances.append(self)

    async def start(self):
        self.started = True

    def read_sync(self):
        self.release.wait(timeout=1)
        if self.release.is_set():
            self.release.clear()
            return "daemon terminal output"
        return ""

    def write_sync(self, value):
        self.writes.append(value)

    def resize(self, cols, rows):
        self.resizes.append((cols, rows))

    def close(self):
        self.closed = True
        self.release.set()


def test_pty_daemon_factory_requires_authenticated_ipc(tmp_path):
    config = PtyDaemonRuntimeConfig(allowed_workspaces=(tmp_path,))
    with pytest.raises(ValueError, match="secret"):
        create_session_daemon(pty_config=config)

    daemon = create_session_daemon(secret="test-only-daemon-secret", pty_config=config)
    assert "pty" in daemon._runtime_registry


@pytest.mark.asyncio
async def test_daemon_owned_pty_keeps_terminal_process_and_wals_output(tmp_path):
    FakeTerminal.instances.clear()
    daemon = SessionDaemon(capacity=8, secret="test-only-daemon-secret")
    runtime = PtyDaemonRuntime(
        PtyDaemonRuntimeConfig(allowed_workspaces=(tmp_path,)),
        emit=daemon.publish,
        terminal_factory=FakeTerminal,
    )
    daemon.register_runtime("pty", runtime)

    spawned = await daemon._spawn_runtime(
        {
            "action": "session.spawn",
            "request_id": "spawn-1",
            "session_id": "terminal-session-1",
            "agent_type": "pty",
            "cwd": str(tmp_path),
            "params": {},
        }
    )
    assert spawned["result"] == {"status": "running"}
    terminal = FakeTerminal.instances[0]
    assert terminal.started is True

    sent = await daemon._dispatch_runtime_action(
        "session.send",
        {"action": "session.send", "session_id": "terminal-session-1", "input": "echo daemon"},
    )
    resized = await daemon._dispatch_runtime_action(
        "session.resize",
        {"action": "session.resize", "session_id": "terminal-session-1", "cols": 140, "rows": 45},
    )
    interrupted = await daemon._dispatch_runtime_action(
        "session.interrupt",
        {"action": "session.interrupt", "session_id": "terminal-session-1"},
    )
    assert sent["result"] == {"status": "running", "accepted": True}
    assert resized["result"] == {"status": "running", "accepted": True}
    assert interrupted["result"] == {"status": "running", "accepted": True}
    assert terminal.writes == ["echo daemon", "\x03"]
    assert terminal.resizes == [(140, 45)]

    terminal.release.set()
    deadline = asyncio.get_running_loop().time() + 1
    while not daemon.sync({"terminal-session-1": 0}).get("terminal-session-1", {}).get("frames"):
        assert asyncio.get_running_loop().time() < deadline
        await asyncio.sleep(0.01)
    replay = daemon.sync({"terminal-session-1": 0})["terminal-session-1"]
    assert replay["frames"][0]["event"] == "pty.output"
    assert replay["frames"][0]["payload"] == {"data": "daemon terminal output"}

    closed = await daemon._dispatch_runtime_action(
        "session.close",
        {"action": "session.close", "session_id": "terminal-session-1"},
    )
    assert closed["result"] == {"status": "idle", "closed": True}
    assert terminal.closed is True
    assert "terminal-session-1" not in daemon._session_runtimes
    assert "terminal-session-1" not in daemon.status()

    await daemon.shutdown()
    assert terminal.closed is True


@pytest.mark.asyncio
async def test_daemon_owned_pty_spawn_reattaches_to_the_same_running_runtime(tmp_path):
    FakeTerminal.instances.clear()
    daemon = SessionDaemon(capacity=8, secret="test-only-daemon-secret")
    runtime = PtyDaemonRuntime(
        PtyDaemonRuntimeConfig(allowed_workspaces=(tmp_path,)),
        emit=daemon.publish,
        terminal_factory=FakeTerminal,
    )
    daemon.register_runtime("pty", runtime)
    request = {
        "action": "session.spawn",
        "request_id": "spawn-1",
        "session_id": "terminal-session-1",
        "agent_type": "pty",
        "cwd": str(tmp_path),
        "params": {},
    }

    first = await daemon._spawn_runtime(request)
    second = await daemon._spawn_runtime({**request, "request_id": "spawn-2"})

    assert first["result"] == {"status": "running"}
    assert second["result"] == {"status": "running", "attached": True}
    assert len(FakeTerminal.instances) == 1

    await daemon.shutdown()
