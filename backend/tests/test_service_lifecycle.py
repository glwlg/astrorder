from __future__ import annotations

import importlib.util
import sys
from pathlib import Path

import pytest

SCRIPT = Path(__file__).resolve().parents[2] / "scripts" / "service_lifecycle.py"
spec = importlib.util.spec_from_file_location("service_lifecycle", SCRIPT)
assert spec is not None and spec.loader is not None
service_lifecycle = importlib.util.module_from_spec(spec)
spec.loader.exec_module(service_lifecycle)

SCRIPTS_DIR = SCRIPT.parent
if str(SCRIPTS_DIR) not in sys.path:
    sys.path.insert(0, str(SCRIPTS_DIR))
RESTART_SCRIPT = SCRIPTS_DIR / "restart_service.py"
restart_spec = importlib.util.spec_from_file_location("restart_service", RESTART_SCRIPT)
assert restart_spec is not None and restart_spec.loader is not None
restart_service = importlib.util.module_from_spec(restart_spec)
restart_spec.loader.exec_module(restart_service)


def test_listening_pids_matches_exact_port_and_ignores_daemon_and_connections():
    output = """
  TCP    127.0.0.1:30001        0.0.0.0:0              LISTENING       111
  TCP    127.0.0.1:30009        0.0.0.0:0              LISTENING       222
  TCP    127.0.0.1:300010       0.0.0.0:0              LISTENING       333
  TCP    127.0.0.1:30001        127.0.0.1:65432        ESTABLISHED     444
"""
    assert service_lifecycle.listening_pids(output, 30001) == [111]
    assert service_lifecycle.listening_pids(output, 30009) == [222]


def test_status_commands_do_not_create_windows_console(monkeypatch):
    calls = []

    class Completed:
        stdout = ""
        returncode = 0

    monkeypatch.setattr(
        service_lifecycle.subprocess,
        "run",
        lambda *args, **kwargs: calls.append((args, kwargs)) or Completed(),
    )
    service_lifecycle.current_listening_pids(30001)
    service_lifecycle.command_line_for_pid(123)

    assert all(
        call[1]["creationflags"] == service_lifecycle.HIDDEN_PROCESS_FLAGS for call in calls
    )


def test_select_owned_app_pid_requires_exact_production_entrypoint():
    assert service_lifecycle.select_owned_app_pid(
        [111],
        command_line=lambda pid: "C:/workspace/backend/.venv/Scripts/python.exe scripts/run_production.py" if pid == 111 else None,
    ) == 111
    assert service_lifecycle.select_owned_app_pid(
        [111], command_line=lambda _pid: "python unrelated_server.py"
    ) is None
    assert service_lifecycle.select_owned_app_pid(
        [111, 112], command_line=lambda _pid: "python scripts/run_production.py"
    ) is None


def test_select_owned_daemon_pid_never_accepts_app_server_marker():
    assert service_lifecycle.select_owned_daemon_pid(
        [222],
        command_line=lambda _pid: "python -m astrorder.daemon.session_daemon --port 30009",
    ) == 222
    assert service_lifecycle.select_owned_daemon_pid(
        [222], command_line=lambda _pid: "python scripts/run_production.py"
    ) is None


def test_restart_refuses_to_kill_an_unverified_listener(monkeypatch):
    monkeypatch.setattr(restart_service, "current_listening_pids", lambda _port: [111])
    monkeypatch.setattr(restart_service, "command_line_for_pid", lambda _pid: "python unrelated.py")
    monkeypatch.setattr(
        restart_service,
        "terminate_verified_pid",
        lambda _pid: pytest.fail("must not terminate an unverified listener"),
    )
    monkeypatch.setattr(
        restart_service.subprocess,
        "Popen",
        lambda *_args, **_kwargs: pytest.fail("must not start over an unverified listener"),
    )

    with pytest.raises(RuntimeError, match="Refusing restart"):
        restart_service.restart()


def test_restart_terminates_only_verified_app_listener_and_never_queries_daemon(monkeypatch, tmp_path):
    calls: list[object] = []
    states = iter([[111], [], [444]])
    monkeypatch.setattr(restart_service, "current_listening_pids", lambda port: calls.append(port) or next(states))
    monkeypatch.setattr(
        restart_service,
        "command_line_for_pid",
        lambda _pid: "python scripts/run_production.py",
    )
    monkeypatch.setattr(restart_service, "terminate_verified_pid", lambda pid: calls.append(("terminate", pid)))
    runtime = tmp_path / ".runtime"
    runtime.mkdir()
    monkeypatch.setattr(restart_service, "ROOT", tmp_path)

    class Process:
        pid = 444

    def popen(argv, **kwargs):
        calls.append(("popen", argv, kwargs))
        return Process()

    monkeypatch.setattr(restart_service.subprocess, "Popen", popen)

    assert restart_service.restart() == 444
    assert calls[:3] == [30001, ("terminate", 111), 30001]
    assert calls[3] == ("popen", [str(tmp_path / "backend/.venv/Scripts/python.exe"), "scripts/run_production.py"], calls[3][2])
    assert calls[4] == 30001
    assert 30009 not in calls
    popen = next(call for call in calls if isinstance(call, tuple) and call[0] == "popen")
    assert popen[2]["creationflags"] & 0x01000000
