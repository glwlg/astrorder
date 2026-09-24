from __future__ import annotations

import asyncio

import pytest

from astrorder.config import Settings
from astrorder.daemon.bridge import DaemonBridge
from astrorder.daemon.session_daemon import SessionDaemon
from astrorder.daemon.runtimes.ssh.control import DaemonSshController
from astrorder.daemon.runtimes.ssh.runtime import SshDaemonRuntime, SshDaemonRuntimeRegistry
from astrorder.core.events import EventHub
from astrorder.service import ControlService
from astrorder.store import Store


@pytest.mark.asyncio
async def test_ssh_daemon_owner_survives_app_proxy_restart_for_exact_connection(tmp_path):
    class FakeSshController:
        def __init__(self, connection_id: str) -> None:
            self.connection_id = connection_id
            self.start_calls = 0
            self.stop_calls = 0
            self.commands: list[dict[str, object]] = []

        @property
        def agent_id(self) -> str:
            return f"ssh-hermes-{self.connection_id}"

        def start(self):
            self.start_calls += 1

        def snapshot(self):
            return {
                "id": self.connection_id,
                "alive": True,
                "agent_id": self.agent_id,
                "runtime_id": self.agent_id,
                "source_id": f"hermes-ssh-{self.connection_id}",
            }

        def wait_gateway(self, timeout=20):
            return True

        def create_session(self, workspace=None, title=None):
            del workspace, title
            raise AssertionError("not used")

        def submit(self, command):
            self.commands.append(dict(command))
            return "accepted", None

        def stop(self):
            self.stop_calls += 1

    controllers: dict[str, FakeSshController] = {}

    def factory(connection_id, settings):
        assert settings == {"host": "remote.example", "port": 22}
        controller = FakeSshController(connection_id)
        controllers[connection_id] = controller
        return SshDaemonRuntime(lambda: controller)

    daemon = SessionDaemon(secret="test-only-daemon-secret")
    daemon.register_runtime("ssh", SshDaemonRuntimeRegistry(factory))
    server = await daemon.serve("127.0.0.1", 0)
    port = server.sockets[0].getsockname()[1]
    settings = Settings(
        database_url=f"sqlite:///{tmp_path}/ssh-restart.sqlite3",
        browser_secret="browser-test",
        connector_secret="connector-test",
        auto_connect_local_hermes=False,
    )
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    endpoint = f"ws://127.0.0.1:{port}"
    ssh_settings = {"host": "remote.example", "port": 22}

    try:
        first = DaemonSshController(
            DaemonBridge(store, service, endpoint, secret="test-only-daemon-secret"),
            connection_id="remote-a",
            ssh_settings=ssh_settings,
        )
        await asyncio.to_thread(first.start)
        first_result = await first.submit(
            {
                "id": "ssh-command-one",
                "agent_id": "ssh-hermes-remote-a",
                "session_id": "native-remote-session",
                "action": "send",
                "text": "first remote daemon prompt",
                "attachments": [],
            }
        )

        restarted = DaemonSshController(
            DaemonBridge(store, service, endpoint, secret="test-only-daemon-secret"),
            connection_id="remote-a",
            ssh_settings=ssh_settings,
        )
        await asyncio.to_thread(restarted.start)
        second_result = await restarted.submit(
            {
                "id": "ssh-command-two",
                "agent_id": "ssh-hermes-remote-a",
                "session_id": "native-remote-session",
                "action": "send",
                "text": "second remote daemon prompt",
                "attachments": [],
            }
        )

        controller = controllers["remote-a"]
        assert first_result == ("accepted", None)
        assert second_result == ("accepted", None)
        assert controller.start_calls == 1
        assert controller.commands == [
            {
                "id": "ssh-command-one",
                "action": "send",
                "session_id": "native-remote-session",
                "text": "first remote daemon prompt",
            },
            {
                "id": "ssh-command-two",
                "action": "send",
                "session_id": "native-remote-session",
                "text": "second remote daemon prompt",
            },
        ]
    finally:
        server.close()
        await server.wait_closed()
        await daemon.shutdown()
        store.close()

    assert controllers["remote-a"].stop_calls == 1
