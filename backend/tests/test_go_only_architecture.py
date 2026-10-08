"""Architecture and launch boundaries after removing the Python daemon."""
import ast
import importlib
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
if str(ROOT / "scripts") not in sys.path:
    sys.path.insert(0, str(ROOT / "scripts"))


def test_python_daemon_contains_only_app_clients_and_projections():
    package = ROOT / "backend/src/astrorder/daemon"
    assert not (package / "runtimes").exists()
    for name in ("session_daemon.py", "model_config.py", "errors.py"):
        assert not (package / name).exists()
    for path in package.rglob("*.py"):
        tree = ast.parse(path.read_text(encoding="utf-8"))
        for node in ast.walk(tree):
            if isinstance(node, ast.ClassDef):
                assert node.name not in {"SessionDaemon", "SessionWAL", "CodexDaemonRuntime", "HermesDaemonRuntime"}, path
            if isinstance(node, ast.ImportFrom) and node.module:
                assert ".runtimes." not in node.module, path
    daemon = importlib.import_module("astrorder.daemon")
    for target in daemon._SUBMODULE_MAP.values():
        importlib.import_module(target)
    for name in ("session_daemon", "codex_runtime", "hermes_runtime", "model_config"):
        assert not hasattr(daemon, name)


@pytest.mark.parametrize("endpoint", ["ws://192.168.1.5:30009", "ws://127.0.0.1:0", "ws://user@localhost:30009", "ws://localhost:30009/path"])
def test_production_endpoint_rejects_invalid_targets(endpoint):
    from scripts.production_daemon import production_daemon_spec
    with pytest.raises(ValueError):
        production_daemon_spec({"ASTRORDER_SESSION_DAEMON_ENDPOINT": endpoint})


def test_production_endpoint_does_not_generate_python_runtime_flags():
    from scripts.production_daemon import production_daemon_spec
    assert production_daemon_spec({"ASTRORDER_DAEMON_CODEX_ENABLED": "1"}) == 30009


def test_start_entrypoint_passes_port_and_rejects_removed_flags(monkeypatch):
    from scripts import start_daemon
    calls = []
    monkeypatch.setattr(start_daemon, "start_daemon", lambda **kw: calls.append(kw) or 123)
    start_daemon.main(["--port", "30130"])
    assert calls == [{"port": 30130}]
    with pytest.raises(SystemExit):
        start_daemon.main(["--enable-hermes"])
    assert len(calls) == 1


@pytest.mark.parametrize("filename", ["run_production.py", "start_desktop_daemon.py"])
def test_production_entrypoint_arguments_match_go_launcher(filename):
    import inspect
    from scripts.production_daemon import ensure_production_daemon

    tree = ast.parse((ROOT / "scripts" / filename).read_text(encoding="utf-8"))
    calls = [node for node in ast.walk(tree) if isinstance(node, ast.Call)
             and isinstance(node.func, ast.Name) and node.func.id == "ensure_production_daemon"]
    assert calls, "production entrypoint must ensure the independent daemon"
    for call in calls:
        inspect.signature(ensure_production_daemon).bind(
            *[object() for _ in call.args], **{kw.arg: object() for kw in call.keywords}
        )


def test_old_python_listener_is_not_reused():
    from scripts.service_lifecycle import select_owned_daemon_pid
    assert select_owned_daemon_pid([1], lambda _: "python -m astrorder.daemon.session_daemon") is None


def test_desktop_input_preserves_mentions_and_images():
    from astrorder.daemon.clients.codex.desktop_input import desktop_message_input
    from astrorder.daemon.bridge import DaemonBridgeError
    text, images = desktop_message_input([
        {"type": "mention", "name": "notes", "path": "C:/notes.txt"},
        {"type": "text", "text": "review"},
        {"type": "image", "url": "data:image/png;base64,fixture"},
    ])
    assert text == "[notes](<C:/notes.txt>)\nreview"
    assert images == ["data:image/png;base64,fixture"]
    with pytest.raises(DaemonBridgeError):
        desktop_message_input([{"type": "image", "url": "file:///private"}])
