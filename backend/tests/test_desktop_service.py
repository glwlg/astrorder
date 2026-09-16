from __future__ import annotations

import importlib.util
import sys
from pathlib import Path

import pytest

SCRIPTS = Path(__file__).resolve().parents[2] / "scripts"
if str(SCRIPTS) not in sys.path:
    sys.path.insert(0, str(SCRIPTS))
spec = importlib.util.spec_from_file_location("desktop_service", SCRIPTS / "desktop_service.py")
assert spec is not None and spec.loader is not None
desktop_service = importlib.util.module_from_spec(spec)
spec.loader.exec_module(desktop_service)


def test_desktop_requires_login_startup_task(monkeypatch):
    monkeypatch.setattr(desktop_service, "startup_task_state", lambda: "not_installed")
    monkeypatch.setattr(
        desktop_service,
        "daemon_status",
        lambda _port, _secret: {"state": "stopped"},
    )

    with pytest.raises(RuntimeError, match="登录启动"):
        desktop_service.start_daemon({}, 30009, "test-secret")
