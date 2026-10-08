"""Opt-in real PTY -> Go journal -> Python bridge integration.

Only a disposable shell, isolated daemon and temporary databases are used.
"""
import asyncio
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import time

import pytest
import websockets

from astrorder.config import Settings
from astrorder.core.events import EventHub
from astrorder.daemon.bridge import DaemonBridge
from astrorder.service import ControlService
from astrorder.store import Store


@pytest.mark.asyncio
async def test_real_go_pty_control_replay_after_client_disconnect(tmp_path):
    executable = os.environ.get("ASTRORDER_GO_SESSIOND")
    if not executable:
        pytest.skip("ASTRORDER_GO_SESSIOND must select an isolated test binary")
    assert Path(executable).is_file()
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    endpoint = f"ws://127.0.0.1:{port}"
    secret = secrets.token_hex(24)
    runtime = {"type": "pty", "allowed": [str(tmp_path)], "shell": "/bin/sh"}
    command = "stty -echo; printf '%s%s\\n' PYTHON_PTY_ VERIFIED\n"
    if os.name == "nt":
        runtime["shell"] = os.environ.get("COMSPEC", "cmd.exe")
        command = "@echo off\r\nset PTY_MARKER=PYTHON_PTY_\r\necho %PTY_MARKER%VERIFIED\r\n"
    config = tmp_path / "runtime.json"
    config.write_text(json.dumps({"runtimes": [runtime]}), encoding="utf-8")
    env = {**os.environ, "ASTRORDER_SESSION_DAEMON_PORT": str(port),
           "ASTRORDER_SESSION_DAEMON_SECRET": secret,
           "ASTRORDER_SESSION_DAEMON_DB": str(tmp_path / "go.sqlite3"),
           "ASTRORDER_SESSION_DAEMON_CONFIG": str(config)}
    settings = Settings(database_url=f"sqlite:///{tmp_path}/app.sqlite3", auto_connect_local_hermes=False)
    store = Store(settings)
    chunks = []
    bridge = DaemonBridge(store, ControlService(store, EventHub(), settings), endpoint, secret=secret)
    bridge.register_native_frame_handler("pty.output", lambda sid, payload: chunks.append(payload["data"]))
    bridge.register_native_frame_handler("pty.closed", lambda *args: None)
    log = (tmp_path / "sessiond.log").open("wb")
    process = subprocess.Popen([executable], env=env, stdout=log, stderr=log)

    async def rpc(client, action, **fields):
        await client.send(json.dumps({"action": action, "request_id": "pty-test", **fields}))
        response = json.loads(await asyncio.wait_for(client.recv(), 5))
        assert response.get("action") == action + ".result", response
        return response

    async def connect():
        client = await websockets.connect(endpoint)
        await rpc(client, "daemon.handshake", secret=secret)
        return client

    try:
        deadline = time.monotonic() + 10
        while True:
            assert process.poll() is None, "isolated Go daemon exited"
            try:
                first = await connect()
                break
            except OSError:
                assert time.monotonic() < deadline, "isolated daemon did not start"
                await asyncio.sleep(0.02)
        async with first:
            await rpc(first, "session.spawn", agent_type="pty", session_id="native-terminal", cwd=str(tmp_path))
        async with await connect() as controller:
            status = await rpc(controller, "daemon.status")
            assert status["sessions"]["native-terminal"]["status"] == "running"
            await rpc(controller, "session.send", session_id="native-terminal", input=command)
        # This reconnect drains actual shell output, not injected session.event fixtures.
        async with await connect() as projection:
            report = await bridge._synchronize(projection)
            while "PYTHON_PTY_VERIFIED" not in "".join(chunks):
                bridge._project_live_frame(await asyncio.wait_for(projection.recv(), 5))
            assert store.get_daemon_checkpoint(report.daemon_id, "native-terminal") > 0
        async with await connect() as controller:
            await rpc(controller, "session.resize", session_id="native-terminal", cols=100, rows=30)
            await rpc(controller, "session.close", session_id="native-terminal")
            status = await rpc(controller, "daemon.status")
            assert "native-terminal" not in status["sessions"]
            assert all(s["status"] == "idle" for s in status["sessions"].values())
    finally:
        if process.poll() is None:
            # Remove only this test's native binding before stopping its isolated daemon.
            async with await connect() as controller:
                status = await rpc(controller, "daemon.status")
                if "native-terminal" in status["sessions"]:
                    await rpc(controller, "session.close", session_id="native-terminal")
                status = await rpc(controller, "daemon.status")
                assert all(s["status"] == "idle" for s in status["sessions"].values())
            process.terminate()
            await asyncio.to_thread(process.wait, 5)
        store.close()
        log.close()
