"""Opt-in cross-language replay test against a real isolated Go daemon.

Set ASTRORDER_GO_SESSIOND to the freshly built executable. No native Agent is
started and all recorded test sessions remain idle. Only this test's process
and databases are created/removed.
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
async def test_go_replay_batches_pages_live_and_restart(tmp_path):
    executable = os.environ.get("ASTRORDER_GO_SESSIOND")
    if not executable:
        pytest.skip("ASTRORDER_GO_SESSIOND must select an isolated test binary")
    assert Path(executable).is_file()
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    endpoint = f"ws://127.0.0.1:{port}"
    secret = secrets.token_hex(24)
    env = {**os.environ, "ASTRORDER_SESSION_DAEMON_PORT": str(port),
           "ASTRORDER_SESSION_DAEMON_SECRET": secret,
           "ASTRORDER_SESSION_DAEMON_DB": str(tmp_path / "go.sqlite3")}
    process = None
    log = (tmp_path / "sessiond.log").open("wb")
    settings = Settings(database_url=f"sqlite:///{tmp_path}/app.sqlite3", auto_connect_local_hermes=False)
    store = Store(settings)

    async def start():
        nonlocal process
        process = subprocess.Popen([executable], env=env, stdout=log, stderr=log)
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            assert process.poll() is None, "isolated Go daemon exited during startup"
            try:
                reader, writer = await asyncio.open_connection("127.0.0.1", port)
                writer.close()
                await writer.wait_closed()
                return
            except OSError:
                await asyncio.sleep(0.02)
        raise AssertionError("isolated Go daemon did not become ready")

    async def rpc(client, action, **fields):
        await client.send(json.dumps({"action": action, "request_id": "test-rpc", **fields}))
        response = json.loads(await asyncio.wait_for(client.recv(), 5))
        assert response.get("action") == action + ".result", response
        assert response.get("request_id") == "test-rpc"
        return response

    async def authenticate(client):
        await rpc(client, "daemon.handshake", secret=secret)

    async def publish(client, sid):
        return await rpc(client, "session.event", session_id=sid, event="native.test",
                         payload={"text": "real-bridge"}, timestamp=1234.5, status="idle")

    async def stop_owned_daemon():
        # Read authenticated authority before stopping our isolated process.
        async with websockets.connect(endpoint) as client:
            await authenticate(client)
            status = await rpc(client, "daemon.status")
            assert all(s["status"] == "idle" for s in status["sessions"].values())
        process.terminate()
        await asyncio.to_thread(process.wait, 5)

    try:
        await start()
        async with websockets.connect(endpoint, max_size=16_000_000) as publisher:
            await authenticate(publisher)
            for index in range(130):
                await publish(publisher, f"s-{index}")
            for _ in range(1005):
                await publish(publisher, "long")

            projected = []
            class Bridge(DaemonBridge):
                injected = False
                async def _request(self, client, action, fields, **kwargs):
                    response = await super()._request(client, action, fields, **kwargs)
                    if action == "daemon.status":
                        await publish(publisher, "gap-new")
                        await publish(publisher, "gap-new")
                    if action == "session.sync" and not self.injected:
                        self.injected = True
                        await publish(publisher, "s-0")
                    return response

            bridge = Bridge(store, ControlService(store, EventHub(), settings), endpoint, secret=secret)
            bridge.register_native_frame_handler("native.test", lambda sid, payload: projected.append(sid))
            async with websockets.connect(endpoint, max_size=16_000_000) as client:
                await bridge._handshake(client)
                report = await bridge._synchronize(client)
                assert report.replayed_frames == 1135
                assert not report.overflowed_sessions
                assert len(projected) == 1135
                assert store.get_daemon_checkpoint(report.daemon_id, "long") == 1005
                for _ in range(3):
                    live = await asyncio.wait_for(client.recv(), 5)
                    assert json.loads(live)["timestamp"] == 1234.5
                    bridge._project_live_frame(live)
                assert projected[-3:] == ["gap-new", "gap-new", "s-0"]
                assert store.get_daemon_checkpoint(report.daemon_id, "gap-new") == 2
                assert store.get_daemon_checkpoint(report.daemon_id, "s-0") == 2
                identity = report.daemon_id

        await stop_owned_daemon()
        await start()
        resumed = DaemonBridge(store, ControlService(store, EventHub(), settings), endpoint, secret=secret)
        resumed.register_native_frame_handler("native.test", lambda *args: None)
        report = await resumed.synchronize_once()
        assert report.daemon_id == identity
        assert report.replayed_frames == 0
        assert not report.overflowed_sessions
        await stop_owned_daemon()
    finally:
        if process is not None and process.poll() is None:
            # Failed tests still clean up only their own disposable daemon.
            process.terminate()
            await asyncio.to_thread(process.wait, 5)
        store.close()
        log.close()
