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


def test_missing_go_binary_never_falls_back_to_python(monkeypatch):
    monkeypatch.delenv("ASTRORDER_SESSION_DAEMON_EXECUTABLE", raising=False)
    with pytest.raises(RuntimeError, match="only Go"):
        service.daemon_launch(port=30109)


def test_go_launch_uses_configured_binary_and_private_environment(monkeypatch, tmp_path):
    binary = tmp_path / "astrorder-sessiond.exe"
    binary.write_bytes(b"fixture")
    config = tmp_path / "runtimes.json"
    config.write_text('{"runtimes": []}')
    monkeypatch.setenv("ASTRORDER_SESSION_DAEMON_EXECUTABLE", str(binary))
    monkeypatch.setenv("ASTRORDER_SESSION_DAEMON_CONFIG", str(config))
    monkeypatch.setenv("ASTRORDER_SESSION_DAEMON_DB", str(tmp_path / "daemon.db"))
    monkeypatch.setenv("ASTRORDER_CONNECTOR_SECRET", "fixture-connector")
    monkeypatch.delenv("ASTRORDER_SESSION_DAEMON_SECRET", raising=False)
    with pytest.raises(RuntimeError, match="SESSION_DAEMON_SECRET"):
        service.daemon_launch(port=30109)
    monkeypatch.setenv("ASTRORDER_SESSION_DAEMON_SECRET", "fixture-daemon")
    argv, environment = service.daemon_launch(port=30109)
    assert argv == [str(binary)]
    assert environment["ASTRORDER_SESSION_DAEMON_PORT"] == "30109"
    assert environment["ASTRORDER_SESSION_DAEMON_CONNECTOR_SECRET"] == "fixture-connector"
    assert "fixture-connector" not in str(argv)
    binary.unlink()
    with pytest.raises(RuntimeError, match="executable"):
        service.daemon_launch(port=30109)


def test_existing_daemon_must_be_verified_before_start_is_reused():
    assert service.existing_verified_daemon(
        [222], lambda _pid: "C:/Astrorder/astrorder-sessiond.exe"
    ) == 222
    with pytest.raises(RuntimeError, match="not a verified Astrorder Session Daemon"):
        service.existing_verified_daemon([222], lambda _pid: "python unrelated.py")


def test_status_is_public_listener_metadata_and_never_contains_secret(tmp_path):
    metadata = service.status_payload(
        port=30009,
        pids=[222],
        command_line=lambda _pid: "C:/Astrorder/astrorder-sessiond.exe",
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
        lambda _pid: "C:/Astrorder/astrorder-sessiond.exe",
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






def test_daemon_process_breaks_away_from_the_callers_job(monkeypatch, tmp_path):
    monkeypatch.setattr(service, "ROOT", tmp_path)
    states = iter(([], [222]))
    monkeypatch.setattr(service, "current_listening_pids", lambda _port: next(states))
    monkeypatch.setattr(
        service,
        "command_line_for_pid",
        lambda _pid: "C:/Astrorder/astrorder-sessiond.exe",
    )
    monkeypatch.setattr(service, "daemon_launch", lambda **_: (["astrorder-sessiond.exe"], {}))
    monkeypatch.setattr(service, "_shared_runtime_dir", lambda: tmp_path)
    calls = []

    class Process:
        pid = 222

        def poll(self):
            return None

    monkeypatch.setattr(service.subprocess, "Popen", lambda *args, **kwargs: calls.append((args, kwargs)) or Process())
    (tmp_path / "backend/.venv/Scripts").mkdir(parents=True)

    assert service.start_daemon(metadata_path=tmp_path / "daemon.json") == 222
    assert calls[0][0][0][0].endswith("astrorder-sessiond.exe")
    assert calls[0][1]["creationflags"] & 0x01000000


def test_losing_start_race_terminates_owned_duplicate(monkeypatch, tmp_path):
    monkeypatch.setattr(service, "ROOT", tmp_path)
    states = iter(([], [333]))
    monkeypatch.setattr(service, "current_listening_pids", lambda _port: next(states))
    monkeypatch.setattr(
        service,
        "command_line_for_pid",
        lambda _pid: "C:/Astrorder/astrorder-sessiond.exe",
    )
    monkeypatch.setattr(service, "daemon_launch", lambda **_: (["astrorder-sessiond.exe"], {}))
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
