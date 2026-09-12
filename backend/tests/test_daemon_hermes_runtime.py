from __future__ import annotations

import asyncio

import pytest

from astrorder.daemon.hermes_runtime import HermesDaemonRuntime
from astrorder.daemon.session_daemon import SessionDaemon


@pytest.mark.asyncio
async def test_daemon_owned_hermes_controller_routes_exact_sessions_without_store_dependency():
    class FakeController:
        def __init__(self) -> None:
            self.connect_calls = 0
            self.shutdown_calls = 0
            self.commands: list[dict[str, object]] = []

        def connect(self):
            self.connect_calls += 1
            return {"state": "connecting"}

        def snapshot(self):
            return {"state": "connected", "agent_id": "daemon-hermes", "detail": "ready"}

        def create_session(self, workspace=None, title=None):
            return {
                "id": "hermes-created-session",
                "workspace": workspace,
                "title": title,
                "status": "idle",
            }

        def submit_tui_command(self, command):
            self.commands.append(dict(command))
            return "accepted", None

        def shutdown(self):
            self.shutdown_calls += 1

    controller = FakeController()
    runtime = HermesDaemonRuntime(lambda: controller)
    daemon = SessionDaemon(secret="test-only-daemon-secret")
    daemon.register_runtime("hermes", runtime)

    spawned = await daemon._spawn_runtime(
        {
            "action": "session.spawn",
            "session_id": "hermes-existing-session",
            "agent_type": "hermes",
            "params": {},
        }
    )
    sent = await daemon._dispatch_runtime_action(
        "session.send",
        {
            "action": "session.send",
            "session_id": "hermes-existing-session",
            "text": "exact daemon Hermes message",
        },
    )
    created = await daemon._create_runtime(
        {
            "action": "session.create",
            "agent_type": "hermes",
            "cwd": "C:/allowed",
            "title": "Daemon Hermes session",
        }
    )
    interrupted = await daemon._dispatch_runtime_action(
        "session.interrupt",
        {"action": "session.interrupt", "session_id": "hermes-existing-session"},
    )

    assert spawned["result"] == {"status": "idle", "agent_id": "daemon-hermes"}
    assert sent["result"] == {"status": "running", "accepted": True}
    assert created["result"] == {
        "session_id": "hermes-created-session",
        "status": "idle",
        "agent_id": "daemon-hermes",
    }
    assert interrupted["result"] == {"status": "running", "accepted": True}
    assert controller.connect_calls == 1
    assert controller.commands == [
        {
            "action": "send",
            "session_id": "hermes-existing-session",
            "text": "exact daemon Hermes message",
        },
        {"action": "stop", "session_id": "hermes-existing-session"},
    ]

    await daemon.shutdown()
    assert controller.shutdown_calls == 1


@pytest.mark.asyncio
async def test_daemon_owned_hermes_runtime_accepts_native_connecting_state_before_plugin_hello():
    class ConnectingController:
        def connect(self):
            return {"state": "connecting"}

        def snapshot(self):
            return {"state": "connecting", "agent_id": "daemon-hermes", "detail": "starting"}

        def create_session(self, workspace=None, title=None):
            del workspace, title
            raise AssertionError("create_session is not part of this spawn test")

        def submit_tui_command(self, command):
            del command
            raise AssertionError("submit_tui_command is not part of this spawn test")

        def shutdown(self):
            return None

    runtime = HermesDaemonRuntime(ConnectingController)
    daemon = SessionDaemon(secret="test-only-daemon-secret")
    daemon.register_runtime("hermes", runtime)

    response = await daemon._spawn_runtime(
        {
            "action": "session.spawn",
            "session_id": "hermes-connect-pending",
            "agent_type": "hermes",
            "params": {},
        }
    )

    assert response["result"] == {"status": "idle", "agent_id": "daemon-hermes"}


@pytest.mark.asyncio
async def test_daemon_owned_hermes_runtime_returns_controller_identity_metadata():
    class MetadataController:
        def connect(self):
            return {"state": "connected"}

        def snapshot(self):
            return {
                "state": "connected",
                "agent_id": "daemon-hermes",
                "source_id": "hermes-local-opaque-source",
                "profile_name": "default",
            }

        def create_session(self, workspace=None, title=None):
            del workspace, title
            raise AssertionError("not used")

        def submit_tui_command(self, command):
            del command
            raise AssertionError("not used")

        def shutdown(self):
            return None

    runtime = HermesDaemonRuntime(MetadataController)
    daemon = SessionDaemon(secret="test-only-daemon-secret")
    daemon.register_runtime("hermes", runtime)

    response = await daemon._spawn_runtime(
        {
            "action": "session.spawn",
            "session_id": "hermes-identity-binding",
            "agent_type": "hermes",
            "params": {},
        }
    )

    assert response["result"] == {
        "status": "idle",
        "agent_id": "daemon-hermes",
        "source_id": "hermes-local-opaque-source",
        "profile_name": "default",
    }


@pytest.mark.asyncio
async def test_daemon_owned_hermes_runtime_disconnect_stops_only_its_controller():
    class FixtureController:
        def __init__(self) -> None:
            self.shutdown_calls = 0

        def snapshot(self):
            return {"state": "connected", "agent_id": "daemon-hermes"}

        def connect(self):
            return {"state": "connected"}

        def create_session(self, workspace=None, title=None):
            del workspace, title
            raise AssertionError("not used")

        def submit_tui_command(self, command):
            del command
            raise AssertionError("not used")

        def shutdown(self):
            self.shutdown_calls += 1

    controller = FixtureController()
    runtime = HermesDaemonRuntime(lambda: controller)

    await runtime.spawn({"session_id": "daemon-runtime-control"})
    await runtime.disconnect()

    assert controller.shutdown_calls == 1


def test_daemon_cli_registers_hermes_runtime_only_with_environment_secrets(monkeypatch):
    from astrorder.daemon import session_daemon

    captured: dict[str, object] = {}

    async def capture_runner(host, port, capacity, **kwargs):
        captured.update({"host": host, "port": port, "capacity": capacity, **kwargs})

    monkeypatch.setattr(session_daemon, "_run_forever", capture_runner)
    monkeypatch.setenv("ASTRORDER_SESSION_DAEMON_SECRET", "test-only-daemon-secret")
    monkeypatch.setenv("ASTRORDER_CONNECTOR_SECRET", "connector-test")
    monkeypatch.setenv("ASTRORDER_PORT", "30128")

    session_daemon.main(["--enable-hermes", "--port", "30129"])

    assert captured["host"] == "127.0.0.1"
    assert captured["port"] == 30129
    assert captured["secret"] == "test-only-daemon-secret"
    assert isinstance(captured["hermes_runtime"], HermesDaemonRuntime)
    controller = captured["hermes_runtime"]._factory()
    assert controller.settings.connector_endpoint() == "ws://127.0.0.1:30129/ws/v1/connector"


@pytest.mark.asyncio
async def test_daemon_hermes_runtime_preserves_command_id_and_wals_native_completion():
    emitted: asyncio.Queue[tuple[str, str, dict[str, object], str | None]] = asyncio.Queue()

    async def emit(session_id, event, payload, *, status=None):
        await emitted.put((session_id, event, dict(payload), status))

    class CompletionController:
        def __init__(self) -> None:
            self.callback = None
            self.commands = []

        def set_command_completion_callback(self, callback):
            self.callback = callback

        def snapshot(self):
            return {"state": "connected", "agent_id": "daemon-hermes"}

        def connect(self):
            return self.snapshot()

        def create_session(self, workspace=None, title=None):
            del workspace, title
            raise AssertionError("not used")

        def submit_tui_command(self, command):
            self.commands.append(dict(command))
            return "accepted", None

        def shutdown(self):
            return None

    controller = CompletionController()
    runtime = HermesDaemonRuntime(lambda: controller, emit=emit)
    await runtime.spawn({"session_id": "native-hermes-session"})

    await runtime.command(
        "session.send",
        {
            "session_id": "native-hermes-session",
            "text": "completion probe",
            "command_id": "command-exact-id",
        },
    )
    assert controller.commands == [
        {
            "id": "command-exact-id",
            "action": "send",
            "session_id": "native-hermes-session",
            "text": "completion probe",
        }
    ]

    controller.callback("daemon-hermes", "native-hermes-session", "command-exact-id")
    assert await asyncio.wait_for(emitted.get(), timeout=1) == (
        "native-hermes-session",
        "hermes.command_complete",
        {
            "agent_id": "daemon-hermes",
            "session_id": "native-hermes-session",
            "command_id": "command-exact-id",
        },
        "idle",
    )


def test_local_hermes_controller_reports_exact_native_command_completion_without_store(tmp_path):
    from astrorder.config import Settings
    from astrorder.connections import LocalHermesController

    controller = LocalHermesController(
        Settings(
            database_url=f"sqlite:///{tmp_path}/unused.sqlite3",
            auto_connect_local_hermes=False,
        ),
        runtime_finder=lambda: None,
    )
    controller._agent_id = "local-hermes-default"
    controller._runtime_session_id = "native-hermes-session"
    controller._active_submitted_commands["native-hermes-session"] = "command-exact-id"
    completed = []

    controller.set_command_completion_callback(
        lambda agent_id, session_id, command_id: completed.append(
            (agent_id, session_id, command_id)
        )
    )
    controller._on_message_complete({"session_id": "private-tui-handle"})

    assert completed == [
        ("local-hermes-default", "native-hermes-session", "command-exact-id")
    ]


def test_local_hermes_gateway_ready_does_not_create_or_prompt_a_smoke_session(tmp_path):
    from astrorder.config import Settings
    from astrorder.connections import LocalHermesController

    settings = Settings(
        database_url=f"sqlite:///{tmp_path}/hermes-gateway-ready.sqlite3",
        browser_secret="browser-test",
        connector_secret="connector-test",
        auto_connect_local_hermes=False,
    )
    controller = LocalHermesController(settings)
    rpc_calls: list[tuple[str, dict[str, object]]] = []
    discoveries: list[object] = []
    controller._state = "connecting"
    controller._gateway_ready.set()
    controller.discover_native_sessions = lambda: {"sessions": []}
    controller.set_discovery_callback(discoveries.append)
    controller._rpc = lambda method, params, timeout=15: (
        rpc_calls.append((method, params)) or None
    )

    controller._initialize_owned_session()

    assert controller.snapshot()["state"] == "connected"
    assert discoveries == [{"sessions": []}]
    assert rpc_calls == []
