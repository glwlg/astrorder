"""Opt-in native inference/tool/offline replay through the real Go executable."""
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
from astrorder.daemon.bridge.codex_projection import CodexNativeFrameRouter
from astrorder.daemon.runtimes.codex.control import DaemonCodexController
from astrorder.native.codex import CodexConnection
from astrorder.service import ControlService
from astrorder.store import Store


@pytest.mark.asyncio
@pytest.mark.parametrize("remote", [False, True], ids=["local", "ssh"])
async def test_real_codex_tool_and_answer_survive_app_projection_restart(tmp_path, remote):
    binary = os.environ.get("ASTRORDER_GO_SESSIOND")
    codex = os.environ.get("ASTRORDER_REAL_CODEX")
    if not binary or not codex or os.environ.get("ASTRORDER_REAL_INFERENCE") != "1":
        pytest.skip("requires isolated Go binary, native Codex, and explicit inference opt-in")
    root = tmp_path / "workspace"
    root.mkdir()
    marker = root / "native-proof.txt"
    proof = secrets.token_hex(12)
    remote_settings = json.loads(os.environ.get("ASTRORDER_REAL_SSH_SETTINGS", "{}")) if remote else None
    if remote and not remote_settings:
        pytest.skip("requires explicit ASTRORDER_REAL_SSH_SETTINGS")
    ssh = ["ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5"]
    if remote:
        ssh += ["-p", str(remote_settings.get("port", 22)),
                f"{remote_settings['user']}@{remote_settings['host']}"]
        cwd = subprocess.check_output(ssh + ["mktemp -d /home/luwei/astrorder-native-audit-XXXXXX"], text=True).strip()
        agent_id = "ssh-codex-audit-native"
    else:
        cwd, agent_id = str(root), "audit-codex"
    config = tmp_path / "runtime.json"
    config.write_text(json.dumps({"runtimes": [{"type": "codex", "executable": codex,
        "workspace": str(root), "allowed": [str(root)], "agent_id": "audit-codex"}]}))
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    secret = secrets.token_hex(24)
    endpoint = f"ws://127.0.0.1:{port}"
    env = {**os.environ, "ASTRORDER_SESSION_DAEMON_PORT": str(port),
        "ASTRORDER_SESSION_DAEMON_SECRET": secret,
        "ASTRORDER_SESSION_DAEMON_DB": str(tmp_path / "journal.db"),
        "ASTRORDER_SESSION_DAEMON_CONFIG": str(config)}
    settings = Settings(database_url=f"sqlite:///{tmp_path}/app.db", auto_connect_local_hermes=False)
    store = Store(settings)
    bridge = DaemonBridge(store, ControlService(store, EventHub(), settings), endpoint, secret=secret, request_timeout=35)
    async def daemon_status():
        async with websockets.connect(endpoint) as client:
            await bridge._handshake(client)
            return await bridge._request(client, "daemon.status", {})

    process = subprocess.Popen([binary], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    sid = None
    controller = router = None
    try:
        deadline = time.monotonic() + 15
        while True:
            assert process.poll() is None, "candidate exited before readiness"
            try:
                before = await daemon_status()
                break
            except Exception:
                if time.monotonic() >= deadline:
                    raise
                await asyncio.sleep(0.1)
        params = {"approvalPolicy": "never", "sandbox": "workspace-write"}
        if remote:
            params.update(connection_id="audit-native", ssh_settings={**remote_settings, "workspace": cwd})
        created = await bridge.request_control("session.create", {
            "agent_type": "codex-ssh" if remote else "codex", "cwd": cwd, "params": params})
        sid = created["session_id"]
        store.upsert_agent({"id": agent_id, "kind": "codex", "name": "Isolated audit",
            "status": "ready", "capabilities": ["chat"], "source_id": agent_id,
            "runtime_id": agent_id, "control_state": "owned"})
        store.upsert_session({"id": sid, "agent_id": agent_id, "title": "Isolated native validation",
            "workspace": cwd, "status": "running", "source_id": agent_id,
            "source_session_id": sid, "history_state": "live", "control_state": "owned",
            "updated_at": "2026-10-08T00:00:00Z"})
        # Exercise the real IPC path above the WebSocket library's 32 KiB default.
        # Markers at both ends let the native history check detect truncation.
        long_context = "BEGIN_LONG_CONTEXT\n" + ("diagnostic padding line\n" * 3000) + "END_LONG_CONTEXT\n"
        prompt = long_context + (
            f"Use your terminal tool to create native-proof.txt in the current directory containing exactly {proof} with no newline. "
            f"Read it back using the terminal tool. Do not modify any other files. Then reply with exactly VERIFIED:{proof}."
        )
        sent = await bridge.request_control("session.send", {"session_id": sid, "prompt": prompt})
        assert sent["action"] == "session.send.result"
        # Retire App projection objects, including its DB connection. The daemon
        # and native process remain alive while inference runs without a subscriber.
        store.close()
        deadline = time.monotonic() + 180
        while time.monotonic() < deadline:
            status = await daemon_status()
            row = status["sessions"][sid]
            assert status["daemon_id"] == before["daemon_id"]
            assert process.poll() is None
            if row["status"] in {"idle", "error", "waiting_approval"}:
                assert row["status"] == "idle", f"native turn did not complete: {row['status']}"
                break
            await asyncio.sleep(0.2)
        else:
            raise AssertionError("real model turn did not finish before deadline")
        if remote:
            actual = subprocess.check_output(ssh + [f"cat {cwd}/native-proof.txt"], text=True)
        else:
            actual = marker.read_text()
        assert actual == proof, "real terminal tool did not produce exact marker bytes"
        store = Store(settings)
        bridge = DaemonBridge(store, ControlService(store, EventHub(), settings), endpoint, secret=secret, request_timeout=35)
        router = CodexNativeFrameRouter(bridge)
        connection = CodexConnection(settings, store, bridge.service)
        connection.agent_id = agent_id
        controller = DaemonCodexController(bridge, router, connection)
        controller.activate()
        report = await bridge.synchronize_once()
        messages, _ = store.list_messages(agent_id, sid, None, 200)
        assert any(m["role"] == "tool" and m.get("tool") for m in messages)
        assert any(m["role"] == "assistant" and f"VERIFIED:{proof}" in m["text"] for m in messages)
        assert store.get_session(agent_id, sid)["status"] == "idle"
        assert report.replayed_frames > 0
        count = len(messages)
        again = await bridge.synchronize_once()
        assert again.replayed_frames == 0
        assert len(store.list_messages(agent_id, sid, None, 200)[0]) == count
        history = await bridge.request_control("session.history_page", {"session_id": sid})
        assert history["result"]["thread"]["id"] == sid
        def text_values(value):
            if isinstance(value, str):
                yield value
            elif isinstance(value, dict):
                for child in value.values():
                    yield from text_values(child)
            elif isinstance(value, list):
                for child in value:
                    yield from text_values(child)
        assert prompt in set(text_values(history["result"])), "native history lost or truncated the long prompt"
        await bridge.request_control("session.delete", {"session_id": sid})
        status = await daemon_status()
        assert sid not in status["sessions"]
        bridge.service.delete_session(agent_id, sid)
        assert store.get_session(agent_id, sid) is None
        sid = None
    finally:
        if controller:
            controller.close()
        if router:
            router.close()
        if sid and process.poll() is None:
            try:
                state = await daemon_status()
                if state["sessions"].get(sid, {}).get("status") == "idle":
                    await bridge.request_control("session.delete", {"session_id": sid})
            except Exception:
                pass
        store.close()
        if process.poll() is None:
            process.terminate()
        await asyncio.to_thread(process.wait, 5)
        if remote:
            subprocess.run(ssh + [f"rm -rf -- {cwd}"], check=True, timeout=15)
