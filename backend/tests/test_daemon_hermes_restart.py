from __future__ import annotations

import asyncio

import pytest

from astrorder.config import Settings
from astrorder.daemon.bridge import DaemonBridge
from astrorder.daemon.runtimes.hermes.control import DaemonHermesController
from astrorder.daemon.runtimes.hermes.runtime import HermesDaemonRuntime
from astrorder.daemon.session_daemon import SessionDaemon
from astrorder.core.events import EventHub
from astrorder.service import ControlService
from astrorder.store import Store


@pytest.mark.asyncio
async def test_hermes_daemon_owner_survives_app_proxy_restart_and_reattaches_exact_identity(tmp_path):
    class FakeController:
        def __init__(self) -> None:
            self.connect_calls = 0
            self.shutdown_calls = 0
            self.commands: list[dict[str, object]] = []

        def connect(self):
            self.connect_calls += 1
            return self.snapshot()

        def snapshot(self):
            return {
                "state": "connected",
                "agent_id": "local-hermes-default",
                "source_id": "hermes-local-opaque-source",
                "profile_name": "default",
                "runtime_id": "local-hermes-default",
            }

        def create_session(self, workspace=None, title=None):
            del workspace, title
            raise AssertionError("not used")

        def submit_tui_command(self, command):
            self.commands.append(dict(command))
            return "accepted", None

        def shutdown(self):
            self.shutdown_calls += 1

    controller = FakeController()
    daemon = SessionDaemon(secret="test-only-daemon-secret")
    daemon.register_runtime("hermes", HermesDaemonRuntime(lambda: controller))
    server = await daemon.serve("127.0.0.1", 0)
    port = server.sockets[0].getsockname()[1]
    settings = Settings(
        database_url=f"sqlite:///{tmp_path}/hermes-restart.sqlite3",
        browser_secret="browser-test",
        connector_secret="connector-test",
        auto_connect_local_hermes=False,
    )
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    endpoint = f"ws://127.0.0.1:{port}"

    try:
        first = DaemonHermesController(
            DaemonBridge(store, service, endpoint, secret="test-only-daemon-secret")
        )
        await asyncio.to_thread(first.connect)
        first_result = await first.submit_tui_command(
            {
                "id": "command-one",
                "agent_id": "local-hermes-default",
                "session_id": "native-hermes-session",
                "action": "send",
                "text": "first daemon-owned prompt",
                "attachments": [],
            }
        )

        # This new bridge/proxy represents a restarted App Server. The daemon and
        # its controller remain live and must serve the same opaque identities.
        restarted = DaemonHermesController(
            DaemonBridge(store, service, endpoint, secret="test-only-daemon-secret")
        )
        await asyncio.to_thread(restarted.connect)
        second_result = await restarted.submit_tui_command(
            {
                "id": "command-two",
                "agent_id": "local-hermes-default",
                "session_id": "native-hermes-session",
                "action": "send",
                "text": "second daemon-owned prompt",
                "attachments": [],
            }
        )

        assert first_result == ("accepted", None)
        assert second_result == ("accepted", None)
        assert restarted.snapshot()["source_id"] == "hermes-local-opaque-source"
        assert controller.connect_calls == 1
        assert controller.commands == [
            {
                "id": "command-one",
                "action": "send",
                "session_id": "native-hermes-session",
                "text": "first daemon-owned prompt",
            },
            {
                "id": "command-two",
                "action": "send",
                "session_id": "native-hermes-session",
                "text": "second daemon-owned prompt",
            },
        ]
    finally:
        server.close()
        await server.wait_closed()
        await daemon.shutdown()
        store.close()

    assert controller.shutdown_calls == 1
