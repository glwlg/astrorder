"""Opt-in App bridge -> real isolated Go model-config control verification.

Uses only a temporary model home and no provider credentials/native prompts.
"""
import asyncio
import hashlib
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
from astrorder.daemon.bridge import DaemonBridge, DaemonBridgeError
from astrorder.service import ControlService
from astrorder.store import Store


@pytest.mark.asyncio
async def test_real_go_model_config_bridge_apply_plan_and_rejection(tmp_path):
    executable = os.environ.get("ASTRORDER_GO_SESSIOND")
    if not executable:
        pytest.skip("ASTRORDER_GO_SESSIOND must select an isolated test binary")
    assert Path(executable).is_file()
    home = tmp_path / "model-home"
    home.mkdir()
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    endpoint = f"ws://127.0.0.1:{port}"
    secret = secrets.token_hex(24)
    config = tmp_path / "runtime.json"
    config.write_text(json.dumps({"runtimes": [], "model_config": {"home": str(home)}}), encoding="utf-8")
    env = {**os.environ, "ASTRORDER_SESSION_DAEMON_PORT": str(port),
           "ASTRORDER_SESSION_DAEMON_SECRET": secret,
           "ASTRORDER_SESSION_DAEMON_DB": str(tmp_path / "go.sqlite3"),
           "ASTRORDER_SESSION_DAEMON_CONFIG": str(config)}
    settings = Settings(database_url=f"sqlite:///{tmp_path}/app.sqlite3", auto_connect_local_hermes=False)
    store = Store(settings)
    bridge = DaemonBridge(store, ControlService(store, EventHub(), settings), endpoint, secret=secret)
    log = (tmp_path / "sessiond.log").open("wb")
    process = subprocess.Popen([executable], env=env, stdout=log, stderr=log)

    async def rpc(client, action, **fields):
        await client.send(json.dumps({"action": action, "request_id": "models", **fields}))
        response = json.loads(await asyncio.wait_for(client.recv(), 5))
        assert response["action"] == action + ".result", response
        return response

    try:
        deadline = time.monotonic() + 10
        while True:
            assert process.poll() is None, "isolated daemon exited during startup"
            try:
                planned = await bridge.request_control("model_config.plan", {"target": {"kind": "local"}})
                break
            except OSError:
                assert time.monotonic() < deadline
                await asyncio.sleep(0.02)
        assert planned["result"]["_home"] == str(home)
        assert planned["result"]["codex_config"]["exists"] is False
        text = 'model = "bridge-model-fixture"\n'
        applied = await bridge.request_control("model_config.apply", {
            "target": {"kind": "local"}, "files": {"codex_config": text}, "api_key": "",
        })
        assert applied["result"]["changed"] == ["codex_config"]
        assert (home / ".codex/config.toml").read_text(encoding="utf-8") == text
        readback = await bridge.request_control("model_config.plan", {"target": {"kind": "local"}})
        native = readback["result"]["codex_config"]
        assert native["content"] == text
        assert native["sha256"] == hashlib.sha256(text.encode()).hexdigest()
        with pytest.raises(DaemonBridgeError):
            await bridge.request_control("model_config.apply", {
                "target": {"kind": "local"}, "files": {"codex_config": "model = invalid\n"},
            })
        assert (home / ".codex/config.toml").read_text(encoding="utf-8") == text
        # No registered adapter means reload must reject, never return false success.
        with pytest.raises(DaemonBridgeError):
            await bridge.request_control("model_config.reload", {"agents": ["codex"]})
        async with websockets.connect(endpoint) as client:
            await rpc(client, "daemon.handshake", secret=secret)
            status = await rpc(client, "daemon.status")
            assert status["sessions"] == {}
            assert status["model_config"]["configured"] is True
    finally:
        if process.poll() is None:
            async with websockets.connect(endpoint) as client:
                await rpc(client, "daemon.handshake", secret=secret)
                assert (await rpc(client, "daemon.status"))["sessions"] == {}
            process.terminate()
            await asyncio.to_thread(process.wait, 5)
        store.close()
        log.close()
