from __future__ import annotations

import json
import os
import secrets
import socket
import subprocess
import threading
import time
from pathlib import Path
from uuid import uuid4

import uvicorn

from astrorder.config import Settings
from astrorder.main import create_app

ROOT = Path(__file__).resolve().parents[1]
ARTIFACT_ROOT = ROOT / ".runtime" / "native-hermes-evidence"
CHROME = Path("C:/Program Files/Google/Chrome/Application/chrome.exe")


def reserve_loopback_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])


def wait_for(predicate, *, timeout: float, failure: str):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        value = predicate()
        if value is not None:
            return value
        time.sleep(0.25)
    raise RuntimeError(failure)


def native_state(app):
    snapshot = app.state.connections.snapshot(app.state.service)["local"]
    agent_id = snapshot.get("agent_id")
    if snapshot.get("state") != "connected" or not isinstance(agent_id, str):
        return None
    sessions = app.state.service.store.list_sessions(agent_id)
    if not sessions:
        return None
    session = next((item for item in sessions if item.get("control_state") == "owned"), None)
    if session is None:
        return None
    messages, _ = app.state.service.store.list_messages(agent_id, session["id"], None, 20)
    if len(messages) < 2:
        return None
    return snapshot, agent_id, session["id"], len(messages)


def main() -> None:
    if not CHROME.is_file():
        raise RuntimeError("Chrome is required for native Hermes browser verification")
    static_dir = ROOT / "frontend" / "dist"
    if not static_dir.is_dir():
        raise RuntimeError("Build frontend/dist before native Hermes browser verification")

    run_dir = ARTIFACT_ROOT / f"run-{uuid4().hex}"
    run_dir.mkdir(parents=True, exist_ok=False)
    port = reserve_loopback_port()
    browser_secret = secrets.token_urlsafe(32)
    connector_secret = secrets.token_urlsafe(32)
    settings = Settings(
        host="127.0.0.1",
        port=port,
        database_url=f"sqlite:///{(run_dir / 'native.sqlite3').as_posix()}",
        browser_secret=browser_secret,
        connector_secret=connector_secret,
        attachments_dir=run_dir / "attachments",
        static_dir=static_dir,
        allowed_origins=(f"http://127.0.0.1:{port}",),
        auto_connect_local_hermes=True,
    )
    app = create_app(settings)
    server = uvicorn.Server(uvicorn.Config(app, host="127.0.0.1", port=port, log_level="warning"))
    thread = threading.Thread(target=server.run, name="native-hermes-test-server", daemon=True)
    thread.start()
    try:
        wait_for(lambda: True if server.started else None, timeout=20, failure="native test server did not start")
        snapshot, agent_id, session_id, _message_count = wait_for(
            lambda: native_state(app),
            timeout=90,
            failure="installed Hermes runtime did not produce a connected agent and isolated session",
        )
        environment = {
            **os.environ,
            "ASTRORDER_NATIVE_URL": f"http://127.0.0.1:{port}",
            "ASTRORDER_NATIVE_BROWSER_TOKEN": browser_secret,
            "ASTRORDER_NATIVE_AGENT_ID": agent_id,
            "ASTRORDER_NATIVE_SESSION_ID": session_id,
            "ASTRORDER_NATIVE_ARTIFACT_DIR": str(run_dir),
            "ASTRORDER_NATIVE_CHROME": str(CHROME),
            "ASTRORDER_NATIVE_COMMAND_TEXT": (
                f"Astrorder native UI command {uuid4().hex}. Reply with exactly: UI_CONNECTED. Do not use tools."
            ),
        }
        result = subprocess.run(
            ["node", str(ROOT / "frontend" / "e2e" / "native-hermes-local.mjs")],
            cwd=ROOT / "frontend",
            env=environment,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=120,
            check=False,
        )
        if result.returncode != 0:
            (run_dir / "browser-error.txt").write_text(
                f"stdout:\n{result.stdout}\nstderr:\n{result.stderr}", encoding="utf-8"
            )
            raise RuntimeError(f"native Hermes browser verification failed; inspect {run_dir}")
        commands = app.state.service.store.list_commands(agent_id, session_id)
        if len(commands) != 1 or commands[0]["state"] != "accepted":
            raise RuntimeError("native Hermes browser command was not accepted exactly once")
        messages, _ = app.state.service.store.list_messages(agent_id, session_id, None, 20)
        evidence = {
            "server_url": f"http://127.0.0.1:{port}",
            "agent_id": agent_id,
            "agent_status": snapshot["state"],
            "session_id": session_id,
            "message_count": len(messages),
            "command_state": commands[0]["state"],
            "browser": "Chromium desktop and mobile passed",
            "artifacts": [
                "native-hermes-agents-desktop.png",
                "native-hermes-session-desktop.png",
                "native-hermes-agents-mobile.png",
                "native-hermes-session-mobile.png",
            ],
        }
        (run_dir / "evidence.json").write_text(json.dumps(evidence, indent=2), encoding="utf-8")
        print(json.dumps(evidence, ensure_ascii=False))
    finally:
        server.should_exit = True
        thread.join(timeout=20)
        if thread.is_alive():
            server.force_exit = True
            thread.join(timeout=5)


if __name__ == "__main__":
    main()
