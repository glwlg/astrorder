"""Real Go producer for App projection/reconnect regressions; no native Agent."""
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
from astrorder.daemon.bridge import DaemonBridge, DaemonBridgeError
from astrorder.daemon.bridge.codex_projection import CodexNativeFrameRouter
from astrorder.service import ControlService
from astrorder.store import Store


@pytest.mark.asyncio
async def test_go_events_survive_app_restart_and_ack_only_after_projection(tmp_path):
    executable = os.environ.get("ASTRORDER_GO_SESSIOND")
    if not executable:
        pytest.skip("ASTRORDER_GO_SESSIOND must select an isolated test binary")
    assert Path(executable).is_file()
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    endpoint = f"ws://127.0.0.1:{port}"
    secret = secrets.token_hex(24)
    env = {k: v for k, v in os.environ.items() if not k.startswith("ASTRORDER_")}
    env.update(ASTRORDER_SESSION_DAEMON_PORT=str(port), ASTRORDER_SESSION_DAEMON_SECRET=secret,
               ASTRORDER_SESSION_DAEMON_DB=str(tmp_path / "events.db"))
    settings = Settings(database_url=f"sqlite:///{tmp_path}/app.db", auto_connect_local_hermes=False)
    store = None
    with (tmp_path / "sessiond.log").open("wb") as log:
        process = subprocess.Popen([executable], env=env, stdout=log, stderr=log)
        try:
            deadline = time.monotonic() + 10
            while True:
                assert process.poll() is None, "isolated Go daemon exited"
                try:
                    _, writer = await asyncio.open_connection("127.0.0.1", port)
                    writer.close()
                    await writer.wait_closed()
                    break
                except OSError:
                    assert time.monotonic() < deadline
                    await asyncio.sleep(0.02)
            async with websockets.connect(endpoint) as publisher:
                async def rpc(action, **fields):
                    await publisher.send(json.dumps({"action": action, "request_id": "fixture", **fields}))
                    reply = json.loads(await asyncio.wait_for(publisher.recv(), 5))
                    assert reply["action"] == action + ".result", reply
                    return reply

                await rpc("daemon.handshake", secret=secret)
                agent = {"id": "fixture-codex", "kind": "codex", "name": "Fixture", "status": "ready",
                         "capabilities": ["chat", "events"], "source_id": "fixture-codex",
                         "runtime_id": "fixture-codex", "control_state": "owned"}
                await rpc("session.event", session_id="control", event="connector.hello", payload=agent, status="idle")
                session = {"id": "thread", "agent_id": agent["id"], "title": "Replay", "workspace": None,
                           "status": "idle", "updated_at": "2026-10-08T00:00:00Z", "source_id": agent["id"],
                           "source_session_id": "thread", "history_state": "live", "control_state": "owned"}
                await rpc("session.event", session_id="thread", event="connector.event", status="idle", payload={
                    "id": "session-event", "type": "session.upsert", "agent_id": agent["id"],
                    "session_id": "thread", "data": session})
                store = Store(settings)
                bridge = DaemonBridge(store, ControlService(store, EventHub(), settings), endpoint, secret=secret)
                report = await bridge.synchronize_once()
                identity = report.daemon_id
                assert store.get_agent(agent["id"])["status"] == "ready"
                assert store.get_daemon_checkpoint(identity, "thread") == 1
                store.close()
                store = None
                # Produce while App storage/bridge is closed, then reopen the same App DB.
                frame = {"method": "turn/completed", "params": {"threadId": "thread", "turn": {"id": "turn", "status": "completed"}}}
                await rpc("session.event", session_id="thread", event="codex.notification", status="idle",
                          payload={"agent_id": agent["id"], "frame": frame})
                store = Store(settings)
                bridge = DaemonBridge(store, ControlService(store, EventHub(), settings), endpoint, secret=secret)
                with pytest.raises(DaemonBridgeError, match="native frame handler"):
                    await bridge.synchronize_once()
                assert store.get_daemon_checkpoint(identity, "thread") == 1
                router = CodexNativeFrameRouter(bridge)
                with pytest.raises(DaemonBridgeError, match="not registered"):
                    await bridge.synchronize_once()
                assert store.get_daemon_checkpoint(identity, "thread") == 1
                delivered = []
                router.register(agent["id"], delivered.append)
                report = await bridge.synchronize_once()
                assert report.replayed_frames == 1
                assert delivered == [frame]
                assert store.get_daemon_checkpoint(identity, "thread") == 2
                assert (await bridge.synchronize_once()).replayed_frames == 0
                assert delivered == [frame]
                router.close()
                status = await rpc("daemon.status")
                assert all(s["status"] == "idle" for s in status["sessions"].values())
                await rpc("daemon.shutdown", confirm_active=False)
                await asyncio.to_thread(process.wait, 5)
        finally:
            if store is not None:
                store.close()
            if process.poll() is None:
                # Only the disposable process created by this test; no native Agents exist.
                process.terminate()
                await asyncio.to_thread(process.wait, 5)
