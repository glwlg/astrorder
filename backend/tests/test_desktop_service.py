from __future__ import annotations

import importlib.util
import sys
from pathlib import Path

SCRIPTS = Path(__file__).resolve().parents[2] / "scripts"
if str(SCRIPTS) not in sys.path:
    sys.path.insert(0, str(SCRIPTS))
spec = importlib.util.spec_from_file_location("desktop_service", SCRIPTS / "desktop_service.py")
assert spec is not None and spec.loader is not None
desktop_service = importlib.util.module_from_spec(spec)
spec.loader.exec_module(desktop_service)


def test_explicit_daemon_start_does_not_require_login_startup_task(monkeypatch):
    monkeypatch.setattr(desktop_service, "startup_task_state", lambda: "not_installed")
    statuses = iter(({"state": "stopped"}, {"state": "running", "pid": 123}))
    monkeypatch.setattr(
        desktop_service,
        "daemon_status",
        lambda _port, _secret: next(statuses),
    )
    started = []
    monkeypatch.setattr(
        desktop_service,
        "start_independent_daemon",
        lambda **kwargs: started.append(kwargs) or 123,
    )
    monkeypatch.setattr(desktop_service, "production_daemon_spec", lambda _environment: (30009, ("--enable-codex",)))

    assert desktop_service.start_daemon({}, 30009, "test-secret")["state"] == "running"
    assert started == [{"port": 30009, "runtime_args": ("--enable-codex",)}]
