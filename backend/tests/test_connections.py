from __future__ import annotations

import asyncio
import io
import json
import subprocess
from pathlib import Path
from unittest.mock import Mock

import pytest
from fastapi.testclient import TestClient

from astrorder.config import Settings
from astrorder.connections import (
    ConnectionError,
    HermesRuntime,
    LocalHermesController,
    build_ssh_validation_argv,
    hermes_command_rejection,
    paginate_native_session_rows,
)
from astrorder.events import EventHub
from astrorder.main import create_app
from astrorder.service import ConnectorConnection, ControlService
from astrorder.ssh_transport import SshNativeRuntime
from astrorder.store import Store


class FakeProcess:
    def __init__(self) -> None:
        self.stdin = io.StringIO()
        self.stdout = io.StringIO()
        self.stderr = io.StringIO()
        self.terminated = False

    def poll(self):
        return None if not self.terminated else 0

    def terminate(self) -> None:
        self.terminated = True

    def wait(self, timeout: float | None = None) -> int:
        del timeout
        return 0

    def kill(self) -> None:
        self.terminated = True


def test_local_hermes_connect_is_owned_and_does_not_return_connector_secret(tmp_path: Path):
    plugin_root = tmp_path / ".hermes" / "plugins" / "astrorder-hermes"
    plugin_root.mkdir(parents=True)
    (plugin_root / "plugin.yaml").write_text("name: astrorder-hermes\n", encoding="utf-8")
    (plugin_root / "__init__.py").write_text("# test plugin\n", encoding="utf-8")
    runtime = HermesRuntime(
        executable=tmp_path / "hermes.exe",
        python=tmp_path / "venv" / "Scripts" / "python.exe",
        version="Hermes Agent v-test",
    )
    launched: list[tuple[list[str], dict[str, object]]] = []
    process = FakeProcess()

    def popen(argv, **kwargs):
        launched.append((list(argv), kwargs))
        return process

    settings = Settings(
        port=30002,
        browser_secret="browser-test-secret",
        connector_secret="connector-test-secret",
        database_url=f"sqlite:///{(tmp_path / 'state.sqlite3').as_posix()}",
        attachments_dir=tmp_path / "attachments",
    )
    profile_plugins_dir = tmp_path / "active-profile" / "plugins"
    controller = LocalHermesController(
        settings,
        project_root=tmp_path,
        profile_plugins_dir=profile_plugins_dir,
        runtime_finder=lambda: runtime,
        command_runner=lambda argv, **_kwargs: subprocess.CompletedProcess(argv, 0),
        popen_factory=popen,
    )

    snapshot = controller.connect()

    assert snapshot["state"] == "connecting"
    assert snapshot["agent_id"]
    assert launched[0][0] == [str(runtime.python), "-u", "-m", "tui_gateway.entry"]
    environment = launched[0][1]["env"]
    assert environment["ASTRORDER_CONNECTOR_ENDPOINT"] == "ws://127.0.0.1:30002/ws/v1/connector"
    assert environment["ASTRORDER_HERMES_AGENT_ID"] == snapshot["agent_id"]
    assert "connector-test-secret" not in json.dumps(snapshot)
    assert environment["ASTRORDER_CONNECTOR_SECRET"] == "connector-test-secret"
    installed_plugin = profile_plugins_dir / "astrorder-hermes"
    assert (installed_plugin / "plugin.yaml").read_text(encoding="utf-8") == "name: astrorder-hermes\n"
    assert (installed_plugin / "__init__.py").read_text(encoding="utf-8") == "# test plugin\n"

    controller.sync_connection(True)
    assert controller.snapshot()["state"] == "connected"
    controller.disconnect()
    assert process.terminated is True
    assert controller.snapshot()["state"] == "offline"


def test_connection_api_persists_safe_ssh_settings_and_rejects_shell_shaped_host(tmp_path: Path):
    app = create_app(
        Settings(
            port=30002,
            browser_secret="browser-test-secret",
            connector_secret="connector-test-secret",
            database_url=f"sqlite:///{(tmp_path / 'state.sqlite3').as_posix()}",
            attachments_dir=tmp_path / "attachments",
            static_dir=tmp_path / "static",
            allowed_origins=("http://testserver",),
            auto_connect_local_hermes=False,
        )
    )
    headers = {"Origin": "http://testserver"}
    payload = {
        "host": "build.example.test",
        "port": 2202,
        "user": "deploy",
        "ssh_config_alias": "build-prod",
        "identity_file": "C:/Users/example/.ssh/id_ed25519",
        "hermes_path": "/opt/hermes/bin/hermes",
        "workspace": "/srv/astrorder",
    }

    with TestClient(app) as client:
        assert client.get("/api/v1/connections").status_code == 401
        assert client.post("/api/v1/connections/local/connect", headers=headers).status_code == 401
        assert client.post(
            "/api/v1/auth/session", json={"token": "browser-test-secret"}, headers=headers
        ).status_code == 200
        invalid = client.put(
            "/api/v1/connections/ssh",
            json={**payload, "host": "build.example.test; echo compromised"},
            headers=headers,
        )
        assert invalid.status_code == 422

        saved = client.put("/api/v1/connections/ssh", json=payload, headers=headers)
        assert saved.status_code == 200, saved.text
        assert saved.json()["state"] == "configured"
        assert saved.json()["settings"] == payload

        loaded = client.get("/api/v1/connections", headers=headers)
        assert loaded.status_code == 200
        assert loaded.json()["ssh"]["settings"] == payload
        assert "secret" not in json.dumps(loaded.json()).lower()


def test_ssh_probe_uses_fixed_openssh_argv_without_shell_interpolation():
    argv = build_ssh_validation_argv(
        ssh_executable="ssh",
        host="build.example.test",
        port=2202,
        user="deploy",
        ssh_config_alias="build-prod",
        identity_file="C:/Users/example/.ssh/id_ed25519",
    )

    assert argv == [
        "ssh",
        "-G",
        "-p",
        "2202",
        "-l",
        "deploy",
        "-i",
        "C:/Users/example/.ssh/id_ed25519",
        "build-prod",
    ]
    assert all(";" not in part and "$(" not in part for part in argv)


@pytest.mark.parametrize('action,observing', [('send', True), ('stop', True), ('stop', False)])
def test_owned_tui_command_uses_native_handler_with_exact_command_identity(tmp_path: Path, action, observing):
    settings = Settings(
        browser_secret="browser-test-secret",
        connector_secret="connector-test-secret",
        database_url=f"sqlite:///{(tmp_path / 'state.sqlite3').as_posix()}",
        attachments_dir=tmp_path / "attachments",
    )
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    agent = store.upsert_agent(
        {
            "id": "local-hermes-opaque",
            "kind": "hermes",
            "name": "本机 Hermes",
            "status": "ready",
            "capabilities": ["chat", "events"],
            "limitation": None,
        }
    )
    store.upsert_session(
        {
            "id": "owned-tui-session",
            "agent_id": agent["id"],
            "title": "owned",
            "workspace": None,
            "status": "idle",
            "updated_at": "2026-09-07T00:00:00Z",
        }
    )

    class FakeWebSocket:
        async def send_json(self, _payload):
            return None

    connection = ConnectorConnection(websocket=FakeWebSocket(), agent=agent)
    if observing:
        service.connections[agent["id"]] = connection
    delivered: list[dict[str, object]] = []

    async def native_submit(command: dict[str, object]) -> tuple[str, str | None]:
        delivered.append(dict(command))
        return "accepted", None

    service.register_native_command_handler(agent["id"], native_submit)
    command = {
        "id": "opaque-command-9c0e",
        "agent_id": agent["id"],
        "session_id": "owned-tui-session",
        "action": action,
        "text": "same text is not an identity",
        "attachment_ids": [],
        "target_id": "owned-tui-session" if action == "stop" else None,
    }

    result = asyncio.run(service.submit_browser_command(command))

    assert result["id"] == command["id"]
    assert result["state"] == "accepted"
    assert delivered[0]["id"] == command["id"]
    assert delivered[0]["session_id"] == command["session_id"]
    if observing:
        assert connection.sent == {(command["session_id"], command["id"])}
    store.close()


def test_local_tui_command_maps_durable_session_identity_to_live_gateway_id(tmp_path: Path):
    controller = LocalHermesController(
        Settings(
            browser_secret="browser-test-secret",
            connector_secret="connector-test-secret",
            database_url=f"sqlite:///{(tmp_path / 'state.sqlite3').as_posix()}",
            attachments_dir=tmp_path / "attachments",
        ),
        project_root=tmp_path,
        runtime_finder=lambda: None,
    )
    controller._state = "connected"
    controller._process = FakeProcess()
    controller._runtime_session_id = "durable-session-key"
    controller._tui_session_id = "live-tui-session-id"
    calls: list[tuple[str, dict[str, object]]] = []

    def rpc(method: str, params: dict[str, object], timeout: float = 15):
        del timeout
        calls.append((method, params))
        return {"result": {"accepted": True}}

    controller._rpc = rpc
    state, error = controller.submit_tui_command(
        {"session_id": "durable-session-key", "text": "opaque command"}
    )

    assert (state, error) == ("accepted", None)
    assert calls == [
        (
            "prompt.submit",
            {"session_id": "live-tui-session-id", "text": "opaque command", "surface": "hud"},
        )
    ]


def test_local_tui_image_is_attached_as_bytes_before_submit(tmp_path: Path):
    raw = b"\x89PNG\r\n\x1a\nimage"
    (tmp_path / "safe.blob").write_bytes(raw)
    controller = LocalHermesController(
        Settings(
            browser_secret="browser-test-secret",
            connector_secret="connector-test-secret",
            database_url=f"sqlite:///{(tmp_path / 'state.sqlite3').as_posix()}",
            attachments_dir=tmp_path,
        ),
        project_root=tmp_path,
        runtime_finder=lambda: None,
    )
    controller._state = "connected"
    controller._process = FakeProcess()
    controller._runtime_session_id = "durable-session-key"
    controller._tui_session_id = "live-tui-session-id"
    store = Mock()
    store.get_attachment.return_value = {"storage_name": "safe.blob", "media_type": "image/png", "name": "shot.png"}
    controller.store = store
    calls: list[tuple[str, dict[str, object]]] = []

    def rpc(method: str, params: dict[str, object], timeout: float = 15):
        del timeout
        calls.append((method, params))
        if method == "image.attach_bytes":
            return {"result": {"attached": True, "path": "/native/shot.png"}}
        if method == "prompt.submit":
            return {"result": {"accepted": True}}
        raise AssertionError(method)

    controller._rpc = rpc
    state, error = controller.submit_tui_command(
        {"session_id": "durable-session-key", "text": "看图", "attachments": [{"id": "upload-id"}]}
    )
    assert (state, error) == ("accepted", None)
    assert calls[0][0] == "image.attach_bytes"
    assert "content_base64" in calls[0][1]
    assert calls[1] == ("prompt.submit", {"session_id": "live-tui-session-id", "text": "看图", "surface": "hud"})
    assert str(tmp_path) not in str(calls)
    assert "C:\\" not in str(calls)


def test_hermes_command_rejection_names_desktop_owner_instead_of_generic_failure():
    message = (
        "Session 20260909_155350_370016 already has a live owner (desktop, pid 27196). "
        "Only one surface at a time may run a session, because a second one would "
        "reason from a transcript that does not include the first one's work."
    )
    text = hermes_command_rejection(
        {"error": {"code": 4090, "message": message, "data": {"reason": "SESSION_NOT_OWNED"}}}
    )
    assert "桌面端占用" in text
    assert "pid 27196" not in text
    generic = hermes_command_rejection({"error": {"code": 32, "message": "unknown method"}})
    assert generic == "本机 Hermes 拒绝了命令投递：unknown method"


def test_local_tui_owned_session_keeps_native_rejection_reason(tmp_path: Path):
    controller = LocalHermesController(
        Settings(
            browser_secret="browser-test-secret",
            connector_secret="connector-test-secret",
            database_url=f"sqlite:///{(tmp_path / 'state.sqlite3').as_posix()}",
            attachments_dir=tmp_path / "attachments",
        ),
        project_root=tmp_path,
        runtime_finder=lambda: None,
    )
    controller._state = "connected"
    controller._process = FakeProcess()
    controller._runtime_session_id = "durable-session-key"
    controller._tui_session_id = "live-tui-session-id"

    def rpc(method: str, params: dict[str, object], timeout: float = 15):
        del timeout, params
        assert method == "prompt.submit"
        return {
            "error": {
                "code": 4090,
                "message": "Session durable-session-key already has a live owner (desktop, pid 1).",
                "data": {"reason": "SESSION_NOT_OWNED"},
            }
        }

    controller._rpc = rpc
    state, error = controller.submit_tui_command(
        {"session_id": "durable-session-key", "text": "消息发送测试"}
    )
    assert state == "failed"
    assert error is not None
    assert "桌面端占用" in error


def test_service_startup_marks_persisted_connectors_disconnected(tmp_path: Path):
    settings = Settings(
        browser_secret="browser-test-secret",
        connector_secret="connector-test-secret",
        database_url=f"sqlite:///{(tmp_path / 'state.sqlite3').as_posix()}",
        attachments_dir=tmp_path / "attachments",
    )
    store = Store(settings)
    store.upsert_agent(
        {
            "id": "previous-local-agent",
            "kind": "hermes",
            "name": "Previous local Hermes",
            "status": "ready",
            "capabilities": ["chat"],
            "limitation": None,
        }
    )
    service = ControlService(store, EventHub(), settings)

    service.mark_persisted_connectors_disconnected()

    assert store.get_agent("previous-local-agent")["status"] == "disconnected"
    store.close()


def test_legacy_ssh_row_migrates_to_stable_id_and_multiple_rows_are_isolated(tmp_path: Path):
    database = tmp_path / "legacy.sqlite3"
    import sqlite3

    with sqlite3.connect(database) as db:
        db.executescript(
            """
            CREATE TABLE ssh_connections (
                id VARCHAR(64) PRIMARY KEY,
                host VARCHAR(253), port INTEGER NOT NULL, user VARCHAR(64),
                ssh_config_alias VARCHAR(128), identity_file TEXT, hermes_path TEXT,
                workspace TEXT, state VARCHAR(32) NOT NULL, detail TEXT,
                updated_at DATETIME NOT NULL
            );
            INSERT INTO ssh_connections
                (id, host, port, user, ssh_config_alias, state, detail, updated_at)
            VALUES ('default', 'legacy.example.test', 22, 'alice', 'legacy',
                    'validated', 'legacy', '2026-09-07 00:00:00');
            """
        )

    settings = Settings(
        browser_secret="browser-test-secret",
        connector_secret="connector-test-secret",
        database_url=f"sqlite:///{database.as_posix()}",
        attachments_dir=tmp_path / "attachments",
    )
    store = Store(settings)
    rows = store.list_ssh_connections()
    assert len(rows) == 1
    migrated_id = rows[0]["id"]
    assert migrated_id != "default"

    second = store.save_ssh_connection(
        {
            "display_name": "second",
            "host": "second.example.test",
            "port": 2222,
            "user": "bob",
            "ssh_config_alias": None,
            "identity_file": None,
            "hermes_path": "/opt/hermes",
            "workspace": "/srv/second",
            "profile_name": "default",
        },
        state="configured",
        detail="saved",
    )
    assert second["id"] != migrated_id
    assert {row["id"] for row in store.list_ssh_connections()} == {migrated_id, second["id"]}

    store.save_ssh_connection(
        {
            **rows[0]["settings"],
            "display_name": "updated legacy",
            "host": "updated.example.test",
        },
        connection_id=migrated_id,
        state="configured",
        detail="updated",
    )
    assert store.get_ssh_connection(second["id"])["settings"]["host"] == "second.example.test"
    assert store.get_ssh_connection(migrated_id)["settings"]["host"] == "updated.example.test"
    store.close()

    reopened = Store(settings)
    assert {row["id"] for row in reopened.list_ssh_connections()} == {second["id"], migrated_id}
    reopened.close()


def test_two_ssh_connection_states_do_not_share_disconnect_or_alias_updates(tmp_path: Path):
    settings = Settings(
        browser_secret="browser-test-secret",
        connector_secret="connector-test-secret",
        database_url=f"sqlite:///{(tmp_path / 'state.sqlite3').as_posix()}",
        attachments_dir=tmp_path / "attachments",
    )
    store = Store(settings)
    first = store.save_ssh_connection(
        {
            "display_name": "first",
            "host": "first.example.test",
            "port": 22,
            "user": "alice",
            "ssh_config_alias": "first",
            "identity_file": None,
            "hermes_path": None,
            "workspace": "/srv/first",
            "profile_name": "default",
        },
        state="connected",
        detail="live",
    )
    second = store.save_ssh_connection(
        {
            "display_name": "second",
            "host": "second.example.test",
            "port": 22,
            "user": "bob",
            "ssh_config_alias": "second",
            "identity_file": None,
            "hermes_path": None,
            "workspace": "/srv/second",
            "profile_name": "default",
        },
        state="connected",
        detail="live",
    )

    store.update_ssh_connection_state(first["id"], "disconnected", "first stopped")
    store.save_ssh_connection(
        {**second["settings"], "ssh_config_alias": "second-renamed"},
        connection_id=second["id"],
        state="connected",
        detail="second remains live",
    )

    assert store.get_ssh_connection(first["id"])["state"] == "disconnected"
    assert store.get_ssh_connection(second["id"])["state"] == "connected"
    assert store.get_ssh_connection(second["id"])["settings"]["ssh_config_alias"] == "second-renamed"
    store.close()


def test_local_source_identity_is_stable_across_runtime_incarnations(tmp_path: Path):
    profile_plugins = tmp_path / "profile" / "plugins"
    settings = Settings(
        browser_secret="browser-test-secret",
        connector_secret="connector-test-secret",
        database_url=f"sqlite:///{(tmp_path / 'state.sqlite3').as_posix()}",
        attachments_dir=tmp_path / "attachments",
    )
    first = LocalHermesController(settings, project_root=tmp_path, profile_plugins_dir=profile_plugins, runtime_finder=lambda: None)
    second = LocalHermesController(settings, project_root=tmp_path, profile_plugins_dir=profile_plugins, runtime_finder=lambda: None)

    assert first.snapshot()["source_id"] == second.snapshot()["source_id"]
    assert first.snapshot()["source_id"] != first.snapshot()["agent_id"]
    assert first.snapshot()["profile_name"] == second.snapshot()["profile_name"]


def test_native_session_enumeration_uses_opaque_ids_and_paginates():
    calls: list[dict[str, object]] = []
    pages = {
        0: [{"id": "native-1", "title": "same"}, {"id": "native-2", "title": "same"}],
        2: [{"id": "native-3", "title": "same"}],
    }

    def rpc(method: str, params: dict[str, object], timeout: float = 15):
        del timeout
        assert method == "session.list"
        calls.append(dict(params))
        return {"result": {"sessions": pages[int(params["offset"])], "total": 3}}

    rows, complete = paginate_native_session_rows(rpc, page_size=2)

    assert complete is True
    assert [row["id"] for row in rows] == ["native-1", "native-2", "native-3"]
    assert [call["offset"] for call in calls] == [0, 2]


def test_remote_deploy_reports_host_key_failure_without_accepting_key(tmp_path: Path):
    plugin_root = Path(__file__).parents[2] / ".hermes" / "plugins" / "astrorder-hermes"

    def rejected(argv, **_kwargs):
        return subprocess.CompletedProcess(argv, 255, "", "Host key verification failed")

    runtime = SshNativeRuntime(
        {
            "display_name": "target",
            "profile_name": "default",
            "host": "target.example.test",
            "port": 22,
            "user": "alice",
            "ssh_config_alias": None,
            "identity_file": None,
            "hermes_path": None,
            "workspace": None,
        },
        "ssh-test-host-key",
        30102,
        Path(__file__).parents[2],
        plugin_root,
        connector_secret="[REDACTED]",
        command_runner=rejected,
    )

    with pytest.raises(ConnectionError, match="不会自动接受"):
        runtime.start()
    assert runtime.snapshot()["alive"] is False


def test_remote_deploy_is_idempotent_and_does_not_put_secret_in_ssh_argv(tmp_path: Path):
    plugin_root = Path(__file__).parents[2] / ".hermes" / "plugins" / "astrorder-hermes"
    calls: list[tuple[list[str], str]] = []

    def accepted(argv, **kwargs):
        calls.append((list(argv), str(kwargs.get("input", ""))))
        return subprocess.CompletedProcess(
            argv,
            0,
            json.dumps(
                {
                    "ok": True,
                    "os": "Linux",
                    "profile_home": "/home/alice/.hermes",
                    "python_path": "/opt/hermes/bin/python",
                    "hermes_version": "Hermes Agent v-test",
                }
            ),
            "",
        )

    runtime = SshNativeRuntime(
        {
            "display_name": "target",
            "profile_name": "default",
            "host": "target.example.test",
            "port": 22,
            "user": "alice",
            "ssh_config_alias": None,
            "identity_file": None,
            "hermes_path": None,
            "workspace": None,
        },
        "ssh-test-idempotent",
        30102,
        Path(__file__).parents[2],
        plugin_root,
        connector_secret="[REDACTED]",
        command_runner=accepted,
    )

    first = runtime._install()
    second = runtime._install()
    assert first == second
    assert len(calls) == 2
    assert all("[REDACTED]" not in " ".join(argv) for argv, _input in calls)
    assert calls[0][1] == calls[1][1]
