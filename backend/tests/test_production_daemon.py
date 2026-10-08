from __future__ import annotations

import importlib.util
import sys
from pathlib import Path

import pytest

SCRIPTS = Path(__file__).resolve().parents[2] / "scripts"
if str(SCRIPTS) not in sys.path:
    sys.path.insert(0, str(SCRIPTS))
SCRIPT = SCRIPTS / "production_daemon.py"


def _module():
    spec = importlib.util.spec_from_file_location("production_daemon", SCRIPT)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def _production_module():
    script = SCRIPTS / "run_production.py"
    spec = importlib.util.spec_from_file_location("run_production_test", script)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_production_credentials_require_daemon_secret_only_when_enabled(monkeypatch):
    production = _production_module()

    assert production.required_credential_keys({}) == (
        "ASTRORDER_BROWSER_SECRET",
        "ASTRORDER_CONNECTOR_SECRET",
    )
    assert production.required_credential_keys(
        {"ASTRORDER_SESSION_DAEMON_ENABLED": "1"}
    ) == (
        "ASTRORDER_BROWSER_SECRET",
        "ASTRORDER_CONNECTOR_SECRET",
        "ASTRORDER_SESSION_DAEMON_SECRET",
    )
    monkeypatch.setenv("ASTRORDER_DESKTOP_PID", "123")
    assert production.required_credential_keys(
        {"ASTRORDER_SESSION_DAEMON_ENABLED": "1"}
    )[-1] == "ASTRORDER_SESSION_DAEMON_SECRET"


def test_desktop_parent_pid_must_be_valid():
    production = _production_module()

    with pytest.raises(ValueError, match="integer"):
        production.stop_when_desktop_exits(object(), "not-a-pid")
    with pytest.raises(ValueError, match="positive"):
        production.stop_when_desktop_exits(object(), "0")






def test_ensure_production_daemon_requires_environment_secret_and_passes_no_secret(
    monkeypatch, tmp_path
):
    module = _module()
    captured: dict[str, object] = {}

    def start_daemon(*, port):
        captured.update(port=port)
        return 321

    monkeypatch.delenv("ASTRORDER_SESSION_DAEMON_SECRET", raising=False)
    with pytest.raises(RuntimeError, match="SESSION_DAEMON_SECRET"):
        module.ensure_production_daemon({}, starter=start_daemon)

    monkeypatch.setenv("ASTRORDER_SESSION_DAEMON_SECRET", "test-only-secret")
    assert module.ensure_production_daemon({}, starter=start_daemon) == 321
    assert captured == {"port": 30009}
