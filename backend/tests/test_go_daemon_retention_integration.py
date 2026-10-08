"""Real isolated Go daemon: retention, overflow, and stable sequence restart."""
import asyncio
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess

import pytest
import websockets


@pytest.mark.asyncio
async def test_go_retention_overflow_and_restart(tmp_path):
    executable = os.environ.get("ASTRORDER_GO_SESSIOND")
    if not executable:
        pytest.skip("ASTRORDER_GO_SESSIOND must select an isolated test binary")
    assert Path(executable).is_file()
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    secret = secrets.token_hex(24)
    config = tmp_path / "config.json"
    config.write_text(json.dumps({"journal": {"total_bytes": 4096, "session_bytes": 1200, "max_age_seconds": 3600}}))
    env = {**os.environ, "ASTRORDER_SESSION_DAEMON_PORT": str(port),
           "ASTRORDER_SESSION_DAEMON_SECRET": secret,
           "ASTRORDER_SESSION_DAEMON_DB": str(tmp_path / "events.db"),
           "ASTRORDER_SESSION_DAEMON_CONFIG": str(config)}
    endpoint = f"ws://127.0.0.1:{port}"
    process = None
    async def rpc(client, action, **fields):
        await client.send(json.dumps({"action": action, "request_id": "retention", **fields}))
        response = json.loads(await asyncio.wait_for(client.recv(), 5))
        assert response["action"] == action + ".result", response
        return response
    async def start():
        nonlocal process
        process = subprocess.Popen([executable], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for _ in range(200):
            assert process.poll() is None
            try:
                reader, writer = await asyncio.open_connection("127.0.0.1", port)
                writer.close()
                await writer.wait_closed()
                return
            except OSError:
                await asyncio.sleep(0.025)
        pytest.fail("isolated daemon startup timed out")
    async def stop():
        async with websockets.connect(endpoint) as client:
            await rpc(client, "daemon.handshake", secret=secret)
            status = await rpc(client, "daemon.status")
            assert status["pending_operations"] == 0
            assert all(s["status"] == "idle" for s in status["sessions"].values())
        process.terminate()
        await asyncio.to_thread(process.wait, 5)
    try:
        await start()
        async with websockets.connect(endpoint) as client:
            await rpc(client, "daemon.handshake", secret=secret)
            for _ in range(8):
                last = await rpc(client, "session.event", session_id="s", event="native.test", payload={"text": "x" * 300}, status="idle")
            assert last["seq_id"] == 8
            status = await rpc(client, "daemon.status")
            minimum = status["sessions"]["s"]["min_seq_id"]
            assert minimum > 1
            page = await rpc(client, "session.sync", sessions={"s": 0}, defer_live=True)
            assert page["sessions"]["s"]["overflow"] is True
            assert page["sessions"]["s"]["max_seq_id"] == 8
            page = await rpc(client, "session.sync", sessions={"s": minimum - 1}, defer_live=True)
            assert [f["seq_id"] for f in page["sessions"]["s"]["frames"]] == list(range(minimum, 9))
        await stop()
        await start()
        async with websockets.connect(endpoint) as client:
            await rpc(client, "daemon.handshake", secret=secret)
            response = await rpc(client, "session.event", session_id="s", event="native.test", payload={}, status="idle")
            assert response["seq_id"] == 9
        await stop()
    finally:
        if process is not None and process.poll() is None:
            process.terminate()
            await asyncio.to_thread(process.wait, 5)
