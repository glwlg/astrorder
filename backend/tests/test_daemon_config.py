from threading import Event

import pytest
from fastapi.testclient import TestClient

from astrorder.config import Settings
from astrorder.daemon.bridge import DaemonBridge
from astrorder.main import create_app
from astrorder.native_codex import CodexConnection


def test_settings_reads_explicit_session_daemon_bridge_configuration(monkeypatch, tmp_path):
    monkeypatch.setenv("ASTRORDER_BROWSER_SECRET", "browser-test")
    monkeypatch.setenv("ASTRORDER_CONNECTOR_SECRET", "connector-test")
    monkeypatch.setenv("ASTRORDER_DATABASE_URL", f"sqlite:///{tmp_path}/state.sqlite3")
    monkeypatch.setenv("ASTRORDER_SESSION_DAEMON_ENABLED", "true")
    monkeypatch.setenv("ASTRORDER_SESSION_DAEMON_ENDPOINT", "ws://127.0.0.1:30123")
    monkeypatch.setenv("ASTRORDER_SESSION_DAEMON_SECRET", "test-only-daemon-secret")
    monkeypatch.setenv("ASTRORDER_DAEMON_CODEX_ENABLED", "true")

    settings = Settings.from_env()

    assert settings.session_daemon_enabled is True
    assert settings.session_daemon_endpoint == "ws://127.0.0.1:30123"
    assert settings.session_daemon_secret == "test-only-daemon-secret"
    assert settings.daemon_codex_enabled is True


@pytest.mark.parametrize(
    "overrides",
    [
        {"daemon_codex_enabled": True},
        {"daemon_codex_enabled": True, "session_daemon_enabled": True},
    ],
)
def test_daemon_codex_requires_an_enabled_authenticated_session_daemon(overrides, tmp_path):
    values = {
        "database_url": f"sqlite:///{tmp_path}/invalid-daemon-codex.sqlite3",
        "browser_secret": "browser-test",
        "connector_secret": "connector-test",
        "auto_connect_local_hermes": False,
    }
    values.update(overrides)
    with pytest.raises(ValueError, match="daemon Codex"):
        Settings(**values)


@pytest.mark.parametrize(
    "overrides",
    [
        {"daemon_pty_enabled": True},
        {"daemon_pty_enabled": True, "session_daemon_enabled": True},
    ],
)
def test_daemon_pty_requires_an_enabled_authenticated_session_daemon(overrides, tmp_path):
    values = {
        "database_url": f"sqlite:///{tmp_path}/invalid-daemon-pty.sqlite3",
        "browser_secret": "browser-test",
        "connector_secret": "connector-test",
        "auto_connect_local_hermes": False,
    }
    values.update(overrides)
    with pytest.raises(ValueError, match="daemon PTY"):
        Settings(**values)


@pytest.mark.parametrize(
    "overrides",
    [
        {"daemon_hermes_enabled": True},
        {"daemon_hermes_enabled": True, "session_daemon_enabled": True},
    ],
)
def test_daemon_hermes_requires_an_enabled_authenticated_session_daemon(overrides, tmp_path):
    values = {
        "database_url": f"sqlite:///{tmp_path}/invalid-daemon-hermes.sqlite3",
        "browser_secret": "browser-test",
        "connector_secret": "connector-test",
        "auto_connect_local_hermes": False,
    }
    values.update(overrides)
    with pytest.raises(ValueError, match="daemon Hermes"):
        Settings(**values)


@pytest.mark.parametrize(
    "overrides",
    [
        {"daemon_ssh_enabled": True},
        {"daemon_ssh_enabled": True, "session_daemon_enabled": True},
    ],
)
def test_daemon_ssh_requires_an_enabled_authenticated_session_daemon(overrides, tmp_path):
    values = {
        "database_url": f"sqlite:///{tmp_path}/invalid-daemon-ssh.sqlite3",
        "browser_secret": "browser-test",
        "connector_secret": "connector-test",
        "auto_connect_local_hermes": False,
    }
    values.update(overrides)
    with pytest.raises(ValueError, match="daemon SSH"):
        Settings(**values)


def test_enabled_daemon_bridge_runs_for_the_app_lifespan(monkeypatch, tmp_path):
    started = Event()
    stopped = Event()

    async def run_until_stopped(_bridge, stopping):
        started.set()
        await stopping.wait()
        stopped.set()

    monkeypatch.setattr(DaemonBridge, "run", run_until_stopped)
    app = create_app(
        Settings(
            database_url=f"sqlite:///{tmp_path}/state.sqlite3",
            attachments_dir=tmp_path / "attachments",
            browser_secret="browser-test",
            connector_secret="connector-test",
            auto_connect_local_hermes=False,
            session_daemon_enabled=True,
            session_daemon_endpoint="ws://127.0.0.1:30123",
        )
    )

    with TestClient(app):
        assert started.wait(timeout=1)
        assert isinstance(app.state.daemon_bridge, DaemonBridge)
        assert app.state.daemon_bridge.request_timeout == 30.0
    assert stopped.wait(timeout=1)


def test_daemon_request_timeout_defaults_to_cold_native_startup_budget(tmp_path):
    settings = Settings(
        database_url=f"sqlite:///{tmp_path}/daemon-timeout.sqlite3",
        browser_secret="browser-test",
        connector_secret="connector-test",
        auto_connect_local_hermes=False,
    )

    assert settings.session_daemon_request_timeout == 30.0


def test_daemon_codex_opt_in_wires_router_without_enabling_it_by_default(monkeypatch, tmp_path):
    connected = Event()

    async def run_until_stopped(_bridge, stopping):
        await stopping.wait()

    def connect_projection(connection):
        connection.state = "connected"
        connected.set()
        return connection.snapshot()

    monkeypatch.setattr(DaemonBridge, "run", run_until_stopped)
    monkeypatch.setattr(CodexConnection, "connect", connect_projection)
    app = create_app(
        Settings(
            database_url=f"sqlite:///{tmp_path}/daemon-codex.sqlite3",
            attachments_dir=tmp_path / "attachments",
            browser_secret="browser-test",
            connector_secret="connector-test",
            auto_connect_local_hermes=False,
            session_daemon_enabled=True,
            daemon_codex_enabled=True,
            session_daemon_endpoint="ws://127.0.0.1:30123",
            session_daemon_secret="test-only-daemon-secret",
        )
    )

    with TestClient(app):
        assert connected.wait(timeout=1)
        assert app.state.codex.snapshot()["daemon_mode"] is True
        assert app.state.codex.snapshot()["state"] == "connected"
        assert app.state.codex_native_frame_router is not None


def test_daemon_pty_opt_in_wires_terminal_relay(monkeypatch, tmp_path):
    async def run_until_stopped(_bridge, stopping):
        await stopping.wait()

    monkeypatch.setattr(DaemonBridge, "run", run_until_stopped)
    app = create_app(
        Settings(
            database_url=f"sqlite:///{tmp_path}/daemon-pty.sqlite3",
            attachments_dir=tmp_path / "attachments",
            browser_secret="browser-test",
            connector_secret="connector-test",
            auto_connect_local_hermes=False,
            session_daemon_enabled=True,
            daemon_pty_enabled=True,
            session_daemon_endpoint="ws://127.0.0.1:30124",
            session_daemon_secret="test-only-daemon-secret",
        )
    )

    with TestClient(app):
        assert app.state.daemon_terminal_relay is not None


def test_daemon_hermes_opt_in_wires_daemon_owned_local_proxy(monkeypatch, tmp_path):
    async def run_until_stopped(_bridge, stopping):
        await stopping.wait()

    monkeypatch.setattr(DaemonBridge, "run", run_until_stopped)
    app = create_app(
        Settings(
            database_url=f"sqlite:///{tmp_path}/daemon-hermes.sqlite3",
            attachments_dir=tmp_path / "attachments",
            browser_secret="browser-test",
            connector_secret="connector-test",
            auto_connect_local_hermes=False,
            session_daemon_enabled=True,
            daemon_hermes_enabled=True,
            session_daemon_endpoint="ws://127.0.0.1:30126",
            session_daemon_secret="test-only-daemon-secret",
        )
    )

    with TestClient(app):
        assert app.state.daemon_hermes_controller is not None
        assert app.state.connections.local is app.state.daemon_hermes_controller
        assert app.state.connections.local.daemon_owned is True
        assert app.state.hermes_command_frame_router is not None


def test_daemon_ssh_opt_in_wires_connection_factory(monkeypatch, tmp_path):
    async def run_until_stopped(_bridge, stopping):
        await stopping.wait()

    monkeypatch.setattr(DaemonBridge, "run", run_until_stopped)
    app = create_app(
        Settings(
            database_url=f"sqlite:///{tmp_path}/daemon-ssh.sqlite3",
            attachments_dir=tmp_path / "attachments",
            browser_secret="browser-test",
            connector_secret="connector-test",
            auto_connect_local_hermes=False,
            session_daemon_enabled=True,
            daemon_ssh_enabled=True,
            session_daemon_endpoint="ws://127.0.0.1:30132",
            session_daemon_secret="test-only-daemon-secret",
        )
    )

    with TestClient(app):
        assert app.state.daemon_ssh_factory is not None
        assert app.state.connections._daemon_ssh_factory is app.state.daemon_ssh_factory
        assert app.state.hermes_command_frame_router is not None
