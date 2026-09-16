from __future__ import annotations

import json
import sqlite3
import subprocess
from pathlib import Path

import pytest

from astrorder.config import Settings
from astrorder.connections import ConnectionError, paginate_native_session_rows
from astrorder.native_sessions import discover_native_sessions
from astrorder.ssh_transport import (
    _REMOTE_BRIDGE,
    _REMOTE_INSTALL,
    SshNativeRuntime,
    build_bootstrap_stdin,
    build_remote_stdin_bootstrap_command,
)
from astrorder.store import Store


def _settings(tmp_path: Path) -> Settings:
    return Settings(
        browser_secret="browser-test-secret",
        connector_secret="connector-test-secret",
        database_url=f"sqlite:///{(tmp_path / 'state.sqlite3').as_posix()}",
        attachments_dir=tmp_path / "attachments",
    )


def _ssh_runtime(tmp_path: Path, command_runner):
    return SshNativeRuntime(
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
        "ssh-repair-test",
        30102,
        Path(__file__).parents[2],
        Path(__file__).parents[2] / ".hermes" / "plugins" / "astrorder-hermes",
        connector_secret="[REDACTED]",
        command_runner=command_runner,
    )


def test_encoded_bootstrap_executes_through_real_remote_shell_and_interpreter() -> None:
    command = __import__("astrorder.ssh_transport", fromlist=["build_remote_python_command"]).build_remote_python_command(
        "import json; print(json.dumps({'ok': True, 'marker': 'decoded'}))",
        interpreter="python",
    )
    completed = subprocess.run(
        ["sh", "-c", command],
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        check=False,
    )

    assert completed.returncode == 0, completed.stderr
    assert json.loads(completed.stdout.strip()) == {"ok": True, "marker": "decoded"}


def test_stdin_bootstrap_keeps_large_script_out_of_remote_command_line() -> None:
    command = build_remote_stdin_bootstrap_command(interpreter="python", fallback_interpreter=None)
    completed = subprocess.run(
        ["sh", "-c", command],
        input=build_bootstrap_stdin(
            "import json,sys; print(json.dumps(json.load(sys.stdin), sort_keys=True))",
            {"marker": "stdin", "value": 7},
        ),
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        check=False,
    )

    assert completed.returncode == 0, completed.stderr
    assert json.loads(completed.stdout.strip()) == {"marker": "stdin", "value": 7}


def test_ssh_runtime_explicitly_preserves_system_host_key_verification(tmp_path: Path) -> None:
    runtime = _ssh_runtime(tmp_path, lambda argv, **_: subprocess.CompletedProcess(argv, 0, "", ""))

    argv = runtime._base_ssh_argv()

    assert any(argv[index : index + 2] == ["-o", "StrictHostKeyChecking=ask"] for index in range(len(argv) - 1))
    assert "StrictHostKeyChecking=no" not in argv


def test_remote_bootstrap_never_silently_enables_profile_global_plugin() -> None:
    assert '["plugins", "enable", "astrorder-hermes"]' not in _REMOTE_INSTALL
    assert "project_activation_required" in _REMOTE_INSTALL


def test_remote_bridge_loads_enabled_plugins_before_starting_gateway() -> None:
    assert _REMOTE_BRIDGE.index("discover_and_load()") < _REMOTE_BRIDGE.index("tui_gateway.entry")


def test_install_preserves_structured_bootstrap_failure_detail(tmp_path: Path) -> None:
    def failed(argv, **_kwargs):
        return subprocess.CompletedProcess(
            argv,
            2,
            json.dumps(
                {
                    "ok": False,
                    "code": "runtime_missing",
                    "detail": "remote Hermes executable was not found",
                }
            )
            + "\n",
            "Traceback omitted",
        )

    runtime = _ssh_runtime(tmp_path, failed)

    with pytest.raises(ConnectionError) as exc_info:
        runtime._install()

    assert "runtime_missing" in str(exc_info.value)
    assert "remote Hermes executable was not found" in str(exc_info.value)
    assert "Traceback" not in str(exc_info.value)


def test_native_pagination_uses_explicit_total_and_visibility_flags() -> None:
    calls: list[dict[str, object]] = []
    pages = {
        0: [{"id": "native-1"}, {"id": "native-2"}],
        2: [{"id": "native-3"}],
    }

    def rpc(method: str, params: dict[str, object], **_: object) -> dict[str, object]:
        assert method == "session.list"
        calls.append(dict(params))
        offset = int(params["offset"])
        return {"result": {"sessions": pages[offset], "total": 3}}

    rows, complete = paginate_native_session_rows(
        rpc,
        page_size=2,
        include_archived=True,
        include_hidden=True,
    )

    assert complete is True
    assert [row["id"] for row in rows] == ["native-1", "native-2", "native-3"]
    assert [call["offset"] for call in calls] == [0, 2]
    assert all(call["include_archived"] is True for call in calls)
    assert all(call["include_hidden"] is True for call in calls)


def test_native_discovery_reads_nested_project_sessions_and_zero_session_projects() -> None:
    calls: list[tuple[str, dict[str, object]]] = []

    def rpc(method: str, params: dict[str, object], **_: object) -> dict[str, object]:
        calls.append((method, dict(params)))
        if method == "session.list":
            return {"result": {"sessions": [{"id": "native-a", "title": "A"}], "total": 1}}
        if method == "projects.tree":
            return {
                "result": {
                    "projects": [
                        {"id": "project-a", "name": "A", "path": "/work/a", "sessionCount": 1},
                        {"id": "project-empty", "name": "Empty", "path": "/work/empty", "sessionCount": 0},
                    ]
                }
            }
        if method == "projects.project_sessions":
            assert params["session_limit"] == 10000
            return {
                "result": {
                    "project": {
                        "id": "project-a",
                        "name": "A",
                        "path": "/work/a",
                        "repos": [
                            {
                                "id": "/work/a",
                                "groups": [
                                    {"id": "/work/a::branch::main", "sessions": [{"id": "native-a"}]}
                                ],
                            }
                        ],
                    }
                }
            }
        raise AssertionError(method)

    result = discover_native_sessions(
        rpc,
        source_id="source-one",
        agent_id="runtime-one",
        connection_id=None,
        profile_name="default",
        default_workspace=None,
        page_size=100,
    )

    assert result.complete is True
    assert result.native_count == 1
    assert result.project_count == 2
    assert {project["project_id"] for project in result.projects} == {"project-a", "project-empty"}
    assert next(project for project in result.projects if project["project_id"] == "project-empty")["session_count"] == 0
    assert result.sessions[0]["project_id"] == "project-a"
    assert result.sessions[0]["workspace"] == "/work/a"
    assert [method for method, _ in calls].count("projects.project_sessions") == 2


def test_native_project_catalog_is_persisted_without_creating_sessions(tmp_path: Path) -> None:
    store = Store(_settings(tmp_path))
    store.upsert_projects(
        [
            {
                "source_id": "ssh-source",
                "connection_id": "connection-one",
                "agent_id": "agent-one",
                "profile_name": "default",
                "project_id": "empty-project",
                "project_name": "Empty project",
                "workspace": "/work/empty",
                "session_count": 0,
            }
        ]
    )

    reopened = Store(_settings(tmp_path))
    assert reopened.list_projects()[0]["project_id"] == "empty-project"
    assert reopened.list_projects()[0]["session_count"] == 0
    assert reopened.list_sessions() == []


def test_store_starts_from_legacy_database_and_adds_projects_without_dropping_history(tmp_path: Path) -> None:
    database_path = tmp_path / "legacy.sqlite3"
    with sqlite3.connect(database_path) as legacy:
        legacy.executescript(
            """
            CREATE TABLE agents (
                id VARCHAR(256) PRIMARY KEY,
                kind VARCHAR(32) NOT NULL,
                name VARCHAR(256) NOT NULL,
                status VARCHAR(32) NOT NULL,
                capabilities JSON NOT NULL,
                limitation TEXT,
                updated_at DATETIME NOT NULL
            );
            CREATE TABLE sessions (
                row_id INTEGER PRIMARY KEY AUTOINCREMENT,
                id VARCHAR(256) NOT NULL,
                agent_id VARCHAR(256) NOT NULL,
                title VARCHAR(512) NOT NULL,
                workspace TEXT,
                status VARCHAR(32) NOT NULL,
                updated_at DATETIME NOT NULL,
                CONSTRAINT uq_sessions_agent_id UNIQUE (agent_id, id)
            );
            INSERT INTO agents (id, kind, name, status, capabilities, limitation, updated_at)
            VALUES ('legacy-agent', 'hermes', 'Legacy Hermes', 'disconnected', '["chat"]', NULL, '2026-09-07T00:00:00');
            INSERT INTO sessions (id, agent_id, title, workspace, status, updated_at)
            VALUES ('legacy-session', 'legacy-agent', 'Legacy session', '/legacy', 'idle', '2026-09-07T00:00:00');
            """
        )

    settings = Settings(
        browser_secret="browser-test-secret",
        connector_secret="connector-test-secret",
        database_url=f"sqlite:///{database_path.as_posix()}",
        attachments_dir=tmp_path / "attachments",
    )
    store = Store(settings)

    with store.engine.connect() as db:
        tables = {
            str(row[0])
            for row in db.exec_driver_sql("SELECT name FROM sqlite_master WHERE type = 'table'").fetchall()
        }
    agents = store.list_agents()
    sessions = store.list_sessions()

    assert "projects" in tables
    assert [agent["id"] for agent in agents] == ["legacy-agent"]
    assert [session["id"] for session in sessions] == ["legacy-session"]
    assert sessions[0]["source_id"] == "legacy-agent"
    assert sessions[0]["source_session_id"] == "legacy-session"
    assert store.list_projects() == []
    store.close()


def test_legacy_local_source_reconciliation_preserves_routes_and_profile_boundaries(tmp_path: Path) -> None:
    store = Store(_settings(tmp_path))
    legacy_default = store.upsert_agent(
        {
            "id": "local-hermes-old-default",
            "kind": "hermes",
            "name": "本机 Hermes",
            "status": "disconnected",
            "capabilities": ["chat"],
            "limitation": None,
        }
    )
    legacy_default_two = store.upsert_agent(
        {
            "id": "local-hermes-old-default-2",
            "kind": "hermes",
            "name": "本机 Hermes",
            "status": "disconnected",
            "capabilities": ["chat"],
            "limitation": None,
        }
    )
    other_profile = store.upsert_agent(
        {
            "id": "local-hermes-old-other",
            "kind": "hermes",
            "name": "本机 Hermes",
            "status": "disconnected",
            "capabilities": ["chat"],
            "limitation": None,
            "profile_name": "other-profile",
            "source_id": "hermes-local-other-profile",
            "runtime_id": "local-hermes-old-other",
        }
    )
    for agent, native_id in ((legacy_default, "native-default-1"), (legacy_default_two, "native-default-2"), (other_profile, "native-other")):
        store.upsert_session(
            {
                "id": f"durable-{native_id}",
                "agent_id": agent["id"],
                "title": "same project title",
                "workspace": "/work/shared",
                "status": "idle",
                "updated_at": "2026-09-07T00:00:00Z",
                "source_session_id": native_id,
                "project_id": "project-shared",
                "project_name": "Shared",
            }
        )

    current = store.upsert_agent(
        {
            "id": "local-hermes-current-runtime",
            "kind": "hermes",
            "name": "本机 Hermes",
            "status": "ready",
            "capabilities": ["chat"],
            "limitation": None,
            "source_id": "hermes-local-current-source",
            "profile_name": "default",
            "runtime_id": "local-hermes-current-runtime",
            "control_state": "owned",
        }
    )

    store.reconcile_legacy_local_source(
        source_id=current["source_id"],
        current_agent_id=current["id"],
        profile_name="default",
    )

    sessions = store.list_sessions()
    default_sessions = [item for item in sessions if item["source_id"] == current["source_id"]]
    assert {item["source_session_id"] for item in default_sessions} == {"native-default-1", "native-default-2"}
    assert {item["agent_id"] for item in default_sessions} == {
        legacy_default["id"],
        legacy_default_two["id"],
    }
    assert all(item["project_id"] == "project-shared" for item in default_sessions)
    assert store.get_agent(legacy_default["id"])["control_state"] == "readonly"
    assert store.get_agent(legacy_default_two["id"])["control_state"] == "readonly"
    assert store.get_agent(other_profile["id"])["source_id"] == "hermes-local-other-profile"
    assert store.get_agent(other_profile["id"])["profile_name"] == "other-profile"
    store.close()
