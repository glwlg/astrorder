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
    monkeypatch.setattr(service, "graceful_shutdown", lambda port, secret: calls.append((port, secret)))
    assert service.stop_daemon(
        port=30009,
        secret="test-only-daemon-secret",
        metadata_path=tmp_path / "session-daemon.json",
    ) == 222
    assert calls == [(30009, "test-only-daemon-secret")]


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
