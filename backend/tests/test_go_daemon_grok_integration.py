"""Opt-in real Go IPC/owned subprocess -> App Grok projection contract test.

The ACP peer is deliberately a deterministic protocol fixture, NOT real Grok
or LLM inference. All daemon ports, homes, processes and databases are isolated.
"""
import asyncio
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import sys
import time

import pytest
import websockets

from astrorder.config import Settings
from astrorder.core.events import EventHub
from astrorder.daemon.bridge import DaemonBridge
from astrorder.service import ControlService
from astrorder.store import Store
from connectors.grok.connection import GrokProjection

ACP_FIXTURE = '''import json, sys
for line in sys.stdin:
    request = json.loads(line)
    method = request.get("method")
    result = {}
    if method == "initialize":
        result = {"protocolVersion": 1}
    elif method == "session/new":
        result = {"sessionId": "grok-contract-fixture"}
    elif method == "session/load":
        result = {"sessionId": request["params"]["sessionId"]}
    elif method == "session/prompt":
        for text in ("协议", "已验证"):
            frame = {"jsonrpc": "2.0", "method": "session/update",
                "params": {"sessionId": request["params"]["sessionId"],
                    "update": {"sessionUpdate": "agent_message_chunk", "content": {"text": text}}},
                "_meta": {"promptId": "fixture-turn"}}
            print(json.dumps(frame), flush=True)
        result = {"stopReason": "end_turn"}
    if "id" in request:
        print(json.dumps({"jsonrpc": "2.0", "id": request["id"], "result": result}), flush=True)
'''


@pytest.mark.asyncio
async def test_go_grok_owned_output_projects_and_replays_without_duplicates(tmp_path):
    executable = os.environ.get("ASTRORDER_GO_SESSIOND")
    if not executable:
        pytest.skip("ASTRORDER_GO_SESSIOND must select an isolated candidate")
    assert Path(executable).is_file()
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    endpoint, secret = f"ws://127.0.0.1:{port}", secrets.token_hex(24)
    peer = tmp_path / "acp_fixture.py"
    peer.write_text(ACP_FIXTURE, encoding="utf-8")
    config = tmp_path / "runtimes.json"
    config.write_text(json.dumps({"runtimes": [{"type": "grok", "executable": sys.executable,
        "arguments": [str(peer)], "workspace": str(tmp_path), "allowed": [str(tmp_path)],
        "agent_id": "fixture-grok"}]}), encoding="utf-8")
    env = {**os.environ, "ASTRORDER_SESSION_DAEMON_PORT": str(port),
        "ASTRORDER_SESSION_DAEMON_SECRET": secret, "ASTRORDER_SESSION_DAEMON_DB": str(tmp_path / "go.db"),
        "ASTRORDER_SESSION_DAEMON_CONFIG": str(config), "PYTHONIOENCODING": "utf-8"}
    settings = Settings(database_url=f"sqlite:///{tmp_path}/app.db", auto_connect_local_hermes=False)
    store = Store(settings)
    bridge = DaemonBridge(store, ControlService(store, EventHub(), settings), endpoint, secret=secret)
    projection = GrokProjection(bridge, store, bridge.service)
    log = (tmp_path / "candidate.log").open("wb")
    process = subprocess.Popen([executable], env=env, stdout=log, stderr=log)
    session_id = None

    async def rpc(client, action, **fields):
        await client.send(json.dumps({"action": action, "request_id": "contract", **fields}))
        result = json.loads(await asyncio.wait_for(client.recv(), 5))
        assert result.get("action") == action + ".result", result
        return result

    async def connect():
        client = await websockets.connect(endpoint)
        await rpc(client, "daemon.handshake", secret=secret)
        return client

    try:
        deadline = time.monotonic() + 10
        while True:
            assert process.poll() is None
            try:
                client = await connect()
                break
            except OSError:
                assert time.monotonic() < deadline
                await asyncio.sleep(0.02)
        async with client:
            native = await rpc(client, "runtime.request", agent_type="grok", method="initialize", params={})
            assert native["result"]["protocolVersion"] == 1
            assert (await rpc(client, "daemon.status"))["sessions"] == {}
            created = await rpc(client, "session.create", agent_type="grok", cwd=str(tmp_path))
            session_id = created["session_id"]
        store.upsert_agent({"id": "fixture-grok", "kind": "grok", "name": "Grok fixture",
            "status": "ready", "capabilities": ["chat"]})
        store.upsert_session({"id": session_id, "agent_id": "fixture-grok", "title": "fixture",
            "status": "idle", "updated_at": "2026-01-01T00:00:00Z"})
        await bridge.request_control("session.send", {"session_id": session_id,
            "command_id": "fixture-command", "prompt": "fixture, no inference"})
        # With no subscribed App, the Go-owned peer still completes. Wait for authority.
        async with await connect() as controller:
            deadline = time.monotonic() + 5
            while (await rpc(controller, "daemon.status"))["sessions"][session_id]["status"] != "idle":
                assert time.monotonic() < deadline
                await asyncio.sleep(0.01)
        async with await connect() as client:
            report = await bridge._synchronize(client)
        messages, _ = store.list_messages("fixture-grok", session_id, None, 10)
        assert [message["text"] for message in messages] == ["协议已验证"]
        checkpoint = store.get_daemon_checkpoint(report.daemon_id, session_id)
        async with await connect() as client:
            replay = await rpc(client, "session.sync", sessions={session_id: 0}, defer_live=True)
        frames = replay["sessions"][session_id]["frames"]
        assert frames[0]["event"] == "runtime.owner"
        native_frames = [frame for frame in frames if frame["event"] != "runtime.owner"]
        assert len(native_frames) == 3  # two chunks and native prompt completion
        assert checkpoint == frames[-1]["seq_id"]
        async with await connect() as client:
            await bridge._synchronize(client)
        readback, _ = store.list_messages("fixture-grok", session_id, None, 10)
        assert readback == messages
        assert store.get_daemon_checkpoint(report.daemon_id, session_id) == checkpoint
    finally:
        if process.poll() is None:
            async with await connect() as controller:
                if session_id:
                    await rpc(controller, "session.close", session_id=session_id)
                assert (await rpc(controller, "daemon.status"))["sessions"] == {}
            process.terminate()
            await asyncio.to_thread(process.wait, 5)
        projection.close()
        store.close()
        log.close()
