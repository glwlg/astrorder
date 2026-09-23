from __future__ import annotations

import importlib.util
import sys
from pathlib import Path

import pytest

SCRIPT = Path(__file__).resolve().parents[2] / "scripts" / "daemon_service.py"
if str(SCRIPT.parent) not in sys.path:
    sys.path.insert(0, str(SCRIPT.parent))
spec = importlib.util.spec_from_file_location("daemon_service", SCRIPT)
assert spec is not None and spec.loader is not None
service = importlib.util.module_from_spec(spec)
spec.loader.exec_module(service)


def test_daemon_argv_uses_module_and_never_accepts_a_secret_argument(tmp_path):
    argv = service.daemon_argv(
        python_executable="C:/repo/backend/.venv/Scripts/python.exe",
        port=30109,
        runtime_args=["--enable-codex", "--codex-workspace", str(tmp_path)],
    )
    assert argv[:4] == [
        "C:/repo/backend/.venv/Scripts/python.exe",
        "-m",
        "astrorder.daemon.session_daemon",
        "--port",
    ]
    assert "--secret" not in argv
    assert "[REDACTED]" not in argv


def test_existing_daemon_must_be_verified_before_start_is_reused():
    assert service.existing_verified_daemon(
        [222], lambda _pid: "python -m astrorder.daemon.session_daemon --port 30009"
    ) == 222
    with pytest.raises(RuntimeError, match="not a verified Astrorder Session Daemon"):
        service.existing_verified_daemon([222], lambda _pid: "python unrelated.py")


def test_status_is_public_listener_metadata_and_never_contains_secret(tmp_path):
    metadata = service.status_payload(
        port=30009,
        pids=[222],
        command_line=lambda _pid: "python -m astrorder.daemon.session_daemon --port 30009",
        metadata_path=tmp_path / "missing.json",
    )
    assert metadata == {"port": 30009, "listening": True, "pid": 222, "verified": True}
    assert "secret" not in repr(metadata).lower()


def test_stop_verified_daemon_uses_authenticated_graceful_shutdown(monkeypatch, tmp_path):
    states = iter([[222], []])
    monkeypatch.setattr(service, "current_listening_pids", lambda _port: next(states))
    monkeypatch.setattr(
        service,
        "command_line_for_pid",
        lambda _pid: "python -m astrorder.daemon.session_daemon --port 30009",
    )
    calls = []
    monkeypatch.setattr(
        service,
        "graceful_shutdown",
        lambda port, secret, *, confirm_active=False: calls.append(
            (port, secret, confirm_active)
        ),
    )
    assert service.stop_daemon(
        port=30009,
        secret="test-only-daemon-secret",
        metadata_path=tmp_path / "session-daemon.json",
    ) == 222
    assert calls == [(30009, "test-only-daemon-secret", False)]


def test_start_script_passes_explicit_hermes_opt_in_without_secret_argument(monkeypatch):
    script = SCRIPT.with_name("start_daemon.py")
    spec = importlib.util.spec_from_file_location("start_daemon_script", script)
    assert spec is not None and spec.loader is not None
    starter = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(starter)
    captured: dict[str, object] = {}

    def start_daemon(*, port, runtime_args):
        captured["port"] = port
        captured["runtime_args"] = runtime_args
        return 222

    monkeypatch.setattr(starter, "start_daemon", start_daemon)
    starter.main(["--port", "30130", "--enable-hermes"])

    assert captured == {"port": 30130, "runtime_args": ["--enable-hermes"]}


def test_start_script_passes_explicit_ssh_opt_in_without_secret_argument(monkeypatch):
    script = SCRIPT.with_name("start_daemon.py")
    spec = importlib.util.spec_from_file_location("start_daemon_ssh_script", script)
    assert spec is not None and spec.loader is not None
    starter = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(starter)
    captured: dict[str, object] = {}

    def start_daemon(*, port, runtime_args):
        captured["port"] = port
        captured["runtime_args"] = runtime_args
        return 222

    monkeypatch.setattr(starter, "start_daemon", start_daemon)
    starter.main(["--port", "30133", "--enable-ssh"])

    assert captured == {"port": 30133, "runtime_args": ["--enable-ssh"]}


def test_daemon_process_breaks_away_from_the_callers_job(monkeypatch, tmp_path):
    monkeypatch.setattr(service, "ROOT", tmp_path)
    states = iter(([], [222]))
    monkeypatch.setattr(service, "current_listening_pids", lambda _port: next(states))
    monkeypatch.setattr(
        service,
        "command_line_for_pid",
        lambda _pid: "python -m astrorder.daemon.session_daemon --port 30009",
    )
    monkeypatch.setattr(service, "cleanup_stale_daemons", lambda *_args: [])
    monkeypatch.setattr(service, "_shared_runtime_dir", lambda: tmp_path)
    calls = []

    class Process:
        pid = 222

        def poll(self):
            return None

    monkeypatch.setattr(service.subprocess, "Popen", lambda *args, **kwargs: calls.append((args, kwargs)) or Process())
    (tmp_path / "backend/.venv/Scripts").mkdir(parents=True)

    assert service.start_daemon(metadata_path=tmp_path / "daemon.json") == 222
    assert calls[0][0][0][0].endswith("pythonw.exe")
    assert calls[0][1]["creationflags"] & 0x01000000


def test_losing_start_race_terminates_owned_duplicate(monkeypatch, tmp_path):
    monkeypatch.setattr(service, "ROOT", tmp_path)
    states = iter(([], [333]))
    monkeypatch.setattr(service, "current_listening_pids", lambda _port: next(states))
    monkeypatch.setattr(
        service,
        "command_line_for_pid",
        lambda _pid: "python -m astrorder.daemon.session_daemon --port 30009",
    )
    monkeypatch.setattr(service, "cleanup_stale_daemons", lambda *_args: [])
    monkeypatch.setattr(service, "_parent_pid", lambda _pid: 999)
    monkeypatch.setattr(service, "_shared_runtime_dir", lambda: tmp_path)

    class Process:
        pid = 222
        stopped = False

        def poll(self):
            return 0 if self.stopped else None

        def terminate(self):
            self.stopped = True

        def wait(self, timeout):
            return 0

    process = Process()
    monkeypatch.setattr(service.subprocess, "Popen", lambda *_args, **_kwargs: process)
    (tmp_path / "backend/.venv/Scripts").mkdir(parents=True)

    assert service.start_daemon(metadata_path=tmp_path / "daemon.json") == 333
    assert process.stopped is True


def test_venv_launcher_keeps_verified_listener_child(monkeypatch, tmp_path):
    monkeypatch.setattr(service, "ROOT", tmp_path)
    states = iter(([], [333]))
    monkeypatch.setattr(service, "current_listening_pids", lambda _port: next(states))
    monkeypatch.setattr(
        service,
        "command_line_for_pid",
        lambda _pid: "python -m astrorder.daemon.session_daemon --port 30009",
    )
    monkeypatch.setattr(service, "cleanup_stale_daemons", lambda *_args: [])
    monkeypatch.setattr(service, "_shared_runtime_dir", lambda: tmp_path)
    monkeypatch.setattr(service, "_parent_pid", lambda _pid: 222)

    class Process:
        pid = 222
        stopped = False

        def poll(self):
            return None

        def terminate(self):
            self.stopped = True

    process = Process()
    monkeypatch.setattr(service.subprocess, "Popen", lambda *_args, **_kwargs: process)
    (tmp_path / "backend/.venv/Scripts").mkdir(parents=True)

    assert service.start_daemon(metadata_path=tmp_path / "daemon.json") == 333
    assert process.stopped is False


def test_stale_daemon_cleanup_only_terminates_verified_non_listener(monkeypatch):
    stale = "pythonw -m astrorder.daemon.session_daemon --port 30009 --enable-codex"
    monkeypatch.setattr(service, "_daemon_processes", lambda: [
        {"ProcessId": 222, "CommandLine": stale},
        {"ProcessId": 333, "CommandLine": stale},
        {"ProcessId": 444, "CommandLine": "pythonw unrelated.py --port 30009"},
    ])
    monkeypatch.setattr(service, "current_listening_pids", lambda _port: [222])
    monkeypatch.setattr(service, "_parent_pid", lambda _pid: None)
    monkeypatch.setattr(
        service,
        "command_line_for_pid",
        lambda pid: f'"{stale}"  ' if pid == 333 else None,
    )
    terminated = []
    monkeypatch.setattr(service, "terminate_verified_pid", terminated.append)

    assert service.cleanup_stale_daemons(30009, 222) == [333]
    assert terminated == [333]


def test_stale_cleanup_keeps_venv_launcher_parent(monkeypatch):
    daemon = "pythonw -m astrorder.daemon.session_daemon --port 30009"
    monkeypatch.setattr(service, "_daemon_processes", lambda: [
        {"ProcessId": 222, "CommandLine": daemon},
        {"ProcessId": 333, "CommandLine": daemon},
    ])
    monkeypatch.setattr(service, "_parent_pid", lambda _pid: 222)
    monkeypatch.setattr(service, "current_listening_pids", lambda _port: [333])
    monkeypatch.setattr(service, "command_line_for_pid", lambda _pid: daemon)
    terminated = []
    monkeypatch.setattr(service, "terminate_verified_pid", terminated.append)

    assert service.cleanup_stale_daemons(30009, 333) == []
    assert terminated == []


def test_stale_daemon_cleanup_rejects_changed_process_identity(monkeypatch):
    stale = "pythonw -m astrorder.daemon.session_daemon --port 30009"
    monkeypatch.setattr(
        service,
        "_daemon_processes",
        lambda: [{"ProcessId": 333, "CommandLine": stale}],
    )
    monkeypatch.setattr(service, "current_listening_pids", lambda _port: [])
    monkeypatch.setattr(
        service,
        "command_line_for_pid",
        lambda _pid: "pythonw -m astrorder.daemon.session_daemon --port 30010",
    )
    terminated = []
    monkeypatch.setattr(service, "terminate_verified_pid", terminated.append)

    assert service.cleanup_stale_daemons(30009, 222) == []
    assert terminated == []
