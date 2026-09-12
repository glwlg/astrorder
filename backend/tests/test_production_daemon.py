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


def test_production_credentials_require_daemon_secret_only_when_enabled():
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


def test_production_daemon_builds_only_public_runtime_arguments(tmp_path):
    module = _module()
    environment = {
        "ASTRORDER_SESSION_DAEMON_ENDPOINT": "ws://127.0.0.1:30009",
        "ASTRORDER_DAEMON_CODEX_ENABLED": "1",
        "ASTRORDER_CODEX_EXECUTABLE": "C:/tools/codex.exe",
        "ASTRORDER_DAEMON_CODEX_WORKSPACE": str(tmp_path),
        "ASTRORDER_ALLOWED_WORKSPACES": "P:/workspace,C:/Users/operator",
        "ASTRORDER_DAEMON_PTY_ENABLED": "1",
        "ASTRORDER_DAEMON_HERMES_ENABLED": "1",
        "ASTRORDER_DAEMON_SSH_ENABLED": "1",
    }

    port, args = module.production_daemon_spec(environment, root=tmp_path)

    assert port == 30009
    assert args == [
        "--enable-codex",
        "--codex-executable",
        "C:/tools/codex.exe",
        "--codex-workspace",
        str(tmp_path),
        "--codex-agent-id",
        "local-codex",
        "--codex-agent-name",
        "本机 Codex",
        "--codex-allowed-workspace",
        "P:/workspace",
        "--codex-allowed-workspace",
        "C:/Users/operator",
        "--enable-pty",
        "--pty-allowed-workspace",
        "P:/workspace",
        "--pty-allowed-workspace",
        "C:/Users/operator",
        "--enable-hermes",
        "--enable-ssh",
    ]
    assert not any("secret" in item.casefold() for item in args)


def test_production_daemon_rejects_non_loopback_or_missing_codex_configuration(tmp_path):
    module = _module()
    with pytest.raises(ValueError, match="loopback"):
        module.production_daemon_spec(
            {"ASTRORDER_SESSION_DAEMON_ENDPOINT": "ws://192.168.1.5:30009"},
            root=tmp_path,
        )
    with pytest.raises(ValueError, match="Codex"):
        module.production_daemon_spec(
            {
                "ASTRORDER_SESSION_DAEMON_ENDPOINT": "ws://127.0.0.1:30009",
                "ASTRORDER_DAEMON_CODEX_ENABLED": "1",
            },
            root=tmp_path,
        )


def test_ensure_production_daemon_requires_environment_secret_and_passes_no_secret(
    monkeypatch, tmp_path
):
    module = _module()
    captured: dict[str, object] = {}

    def start_daemon(*, port, runtime_args):
        captured.update(port=port, runtime_args=list(runtime_args))
        return 321

    monkeypatch.delenv("ASTRORDER_SESSION_DAEMON_SECRET", raising=False)
    with pytest.raises(RuntimeError, match="SESSION_DAEMON_SECRET"):
        module.ensure_production_daemon({}, root=tmp_path, starter=start_daemon)

    monkeypatch.setenv("ASTRORDER_SESSION_DAEMON_SECRET", "test-only-secret")
    assert module.ensure_production_daemon({}, root=tmp_path, starter=start_daemon) == 321
    assert captured == {"port": 30009, "runtime_args": []}
