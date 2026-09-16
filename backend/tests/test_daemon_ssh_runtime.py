from __future__ import annotations

import asyncio

import pytest

from astrorder.daemon.session_daemon import SessionDaemon
from astrorder.daemon.ssh_runtime import SshDaemonRuntime, SshDaemonRuntimeRegistry


@pytest.mark.asyncio
async def test_daemon_owned_ssh_runtime_routes_exact_remote_sessions_without_local_pty():
    class FakeSshRuntime:
        def __init__(self) -> None:
            self.start_calls = 0
            self.wait_gateway_calls = 0
            self.stop_calls = 0
            self.commands: list[dict[str, object]] = []

        @property
        def agent_id(self) -> str:
            return "daemon-ssh-hermes"

        def start(self) -> None:
            self.start_calls += 1

        def wait_gateway(self, timeout: float = 20) -> bool:
            self.wait_gateway_calls += 1
            assert timeout == 20
            return True

        def snapshot(self):
            return {"alive": True, "agent_id": self.agent_id}

        def create_session(self, workspace=None, title=None):
            return {"id": "ssh-created-session", "status": "idle", "workspace": workspace, "title": title}

        def submit(self, command):
            self.commands.append(dict(command))
            return "accepted", None

        def stop(self) -> None:
            self.stop_calls += 1

    ssh = FakeSshRuntime()
    runtime = SshDaemonRuntime(lambda: ssh)
    daemon = SessionDaemon(secret="test-only-daemon-secret")
    daemon.register_runtime("ssh", runtime)

    spawned = await daemon._spawn_runtime(
        {
            "action": "session.spawn",
            "session_id": "ssh-existing-session",
            "agent_type": "ssh",
            "params": {},
        }
    )
    sent = await daemon._dispatch_runtime_action(
        "session.send",
        {
            "action": "session.send",
            "session_id": "ssh-existing-session",
            "text": "exact remote daemon message",
        },
    )
    created = await daemon._create_runtime(
        {
            "action": "session.create",
            "agent_type": "ssh",
            "cwd": "/remote/workspace",
            "title": "Daemon remote session",
        }
    )
    interrupted = await daemon._dispatch_runtime_action(
        "session.interrupt",
        {"action": "session.interrupt", "session_id": "ssh-existing-session"},
    )

    assert spawned["result"] == {"status": "idle", "agent_id": "daemon-ssh-hermes"}
    assert sent["result"] == {"status": "running", "accepted": True}
    assert created["result"] == {
        "session_id": "ssh-created-session",
        "status": "idle",
        "agent_id": "daemon-ssh-hermes",
    }
    assert interrupted["result"] == {"status": "running", "accepted": True}
    assert ssh.start_calls == 1
    assert ssh.wait_gateway_calls == 1
    assert ssh.commands == [
        {
            "action": "send",
            "session_id": "ssh-existing-session",
            "text": "exact remote daemon message",
        },
        {"action": "stop", "session_id": "ssh-existing-session"},
    ]

    await daemon.shutdown()
    assert ssh.stop_calls == 1


@pytest.mark.asyncio
async def test_multiplexed_ssh_registry_scopes_children_and_disconnects_by_connection_identity():
    class ChildRuntime:
        def __init__(self, connection_id: str) -> None:
            self.connection_id = connection_id
            self.commands: list[tuple[str, str]] = []
            self.disconnect_calls = 0

        async def spawn(self, request):
            return {
                "status": "idle",
                "agent_id": f"ssh-hermes-{self.connection_id}",
                "connection_id": self.connection_id,
            }

        async def create(self, request):
            return {
                "session_id": f"{self.connection_id}-created",
                "status": "idle",
                "agent_id": f"ssh-hermes-{self.connection_id}",
                "connection_id": self.connection_id,
            }

        async def command(self, action, request):
            self.commands.append((action, request["session_id"]))
            return {"status": "running", "accepted": True}

        async def disconnect(self):
            self.disconnect_calls += 1

        async def shutdown(self):
            await self.disconnect()

    children: dict[str, ChildRuntime] = {}

    def factory(connection_id, settings):
        assert settings == {"host": f"{connection_id}.example", "port": 22}
        child = ChildRuntime(connection_id)
        children[connection_id] = child
        return child

    registry = SshDaemonRuntimeRegistry(factory)
    control = await registry.spawn(
        {
            "session_id": "ssh-a-control",
            "params": {
                "connection_id": "ssh-a",
                "ssh_settings": {"host": "ssh-a.example", "port": 22},
            },
        }
    )
    native = await registry.spawn(
        {
            "session_id": "ssh-a-native",
            "params": {
                "connection_id": "ssh-a",
                "ssh_settings": {"host": "ssh-a.example", "port": 22},
            },
        }
    )
    other = await registry.spawn(
        {
            "session_id": "ssh-b-native",
            "params": {
                "connection_id": "ssh-b",
                "ssh_settings": {"host": "ssh-b.example", "port": 22},
            },
        }
    )

    command = await registry.command("session.send", {"session_id": "ssh-a-native"})
    released = await registry.disconnect_session("ssh-a-control")

    assert control["connection_id"] == "ssh-a"
    assert native["agent_id"] == "ssh-hermes-ssh-a"
    assert other["agent_id"] == "ssh-hermes-ssh-b"
    assert command == {"status": "running", "accepted": True}
    assert children["ssh-a"].commands == [("session.send", "ssh-a-native")]
    assert children["ssh-a"].disconnect_calls == 1
    assert children["ssh-b"].disconnect_calls == 0
    assert set(released) == {"ssh-a-control", "ssh-a-native"}


def test_daemon_cli_registers_ssh_registry_only_with_environment_secrets(monkeypatch):
    from astrorder.daemon import session_daemon

    captured: dict[str, object] = {}

    async def capture_runner(host, port, capacity, **kwargs):
        captured.update({"host": host, "port": port, "capacity": capacity, **kwargs})

    monkeypatch.setattr(session_daemon, "_run_forever", capture_runner)
    monkeypatch.setenv("ASTRORDER_SESSION_DAEMON_SECRET", "test-only-daemon-secret")
    monkeypatch.setenv("ASTRORDER_CONNECTOR_SECRET", "connector-test")

    session_daemon.main(["--enable-ssh", "--port", "30131"])

    assert captured["port"] == 30131
    assert captured["secret"] == "test-only-daemon-secret"
    assert isinstance(captured["ssh_runtime"], SshDaemonRuntimeRegistry)
    child = captured["ssh_runtime"]._factory("remote-a", {"host": "remote.example", "port": 22})
    assert child._factory().local_port == 30131


@pytest.mark.asyncio
async def test_daemon_ssh_runtime_preserves_command_id_and_wals_native_completion():
    emitted: asyncio.Queue[tuple[str, str, dict[str, object], str | None]] = asyncio.Queue()

    async def emit(session_id, event, payload, *, status=None):
        await emitted.put((session_id, event, dict(payload), status))

    class CompletionController:
        agent_id = "ssh-hermes-remote-a"

        def __init__(self) -> None:
            self.callback = None
            self.commands = []

        def set_command_completion_callback(self, callback):
            self.callback = callback

        def start(self):
            return None

        def snapshot(self):
            return {"alive": True, "agent_id": self.agent_id, "id": "remote-a"}

        def wait_gateway(self, timeout=20):
            return True

        def create_session(self, workspace=None, title=None):
            del workspace, title
            raise AssertionError("not used")

        def submit(self, command):
            self.commands.append(dict(command))
            return "accepted", None

        def stop(self):
            return None

    controller = CompletionController()
    runtime = SshDaemonRuntime(lambda: controller, emit=emit)
    await runtime.spawn({"session_id": "native-remote-session"})
    await runtime.command(
        "session.send",
        {
            "session_id": "native-remote-session",
            "text": "completion probe",
            "command_id": "ssh-command-exact-id",
        },
    )

    assert controller.commands == [
        {
            "id": "ssh-command-exact-id",
            "action": "send",
            "session_id": "native-remote-session",
            "text": "completion probe",
        }
    ]
    controller.callback("ssh-hermes-remote-a", "native-remote-session", "ssh-command-exact-id")
    assert await asyncio.wait_for(emitted.get(), timeout=1) == (
        "native-remote-session",
        "hermes.command_complete",
        {
            "agent_id": "ssh-hermes-remote-a",
            "session_id": "native-remote-session",
            "command_id": "ssh-command-exact-id",
        },
        "idle",
    )


def test_ssh_native_runtime_reports_exact_command_completion_without_store(tmp_path):
    from astrorder.ssh_transport import SshNativeRuntime

    runtime = SshNativeRuntime(
        {"host": "remote.example", "port": 22},
        "remote-a",
        30009,
        tmp_path,
        tmp_path,
        connector_secret="connector-test",
    )
    runtime._runtime_session_id = "native-remote-session"
    runtime._active_submitted_commands["native-remote-session"] = "ssh-command-exact-id"
    completed = []

    runtime.set_command_completion_callback(
        lambda agent_id, session_id, command_id: completed.append(
            (agent_id, session_id, command_id)
        )
    )
    runtime._on_message_complete({"session_id": "private-remote-handle"})

    assert completed == [
        (runtime.agent_id, "native-remote-session", "ssh-command-exact-id")
    ]


@pytest.mark.asyncio
async def test_daemon_owned_ssh_runtime_routes_approval_queries():
    class Controller:
        agent_id = "ssh-hermes-remote-a"

        def start(self):
            return None

        def wait_gateway(self, timeout=20):
            return True

        def snapshot(self):
            return {"alive": True, "agent_id": self.agent_id}

        def rpc(self, method, params):
            return {"result": {"method": method, "params": params}}

        def stop(self):
            return None

    runtime = SshDaemonRuntime(Controller)

    result = await runtime.query(
        {"method": "approval.pending", "request_params": {"session_id": "native-1"}}
    )

    assert result == {
        "result": {"method": "approval.pending", "params": {"session_id": "native-1"}}
    }
