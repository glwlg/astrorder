import asyncio
import json
import os
import subprocess
import time
import pytest
import websockets

pytestmark = pytest.mark.skipif(
    not os.environ.get("ASTRORDER_GO_SESSIOND"),
    reason="ASTRORDER_GO_SESSIOND must point to Go session daemon binary",
)

@pytest.mark.asyncio
async def test_go_daemon_runtime_disconnect_rejects_active_and_releases_idle(tmp_path):
    exe = os.environ["ASTRORDER_GO_SESSIOND"]
    secret = "test-secret"
    port = 30198
    db_path = tmp_path / "events.db"
    log = (tmp_path / "sessiond.log").open("wb")

    # 配置一个支持 PTY 的环境
    config = tmp_path / "runtime.json"
    shell = os.environ.get("COMSPEC", "cmd.exe") if os.name == "nt" else "/bin/sh"
    config.write_text(
        json.dumps({
            "runtimes": [
                {
                    "type": "pty",
                    "allowed": [str(tmp_path)],
                    "shell": shell,
                }
            ]
        }),
        encoding="utf-8",
    )

    env = {
        **os.environ,
        "ASTRORDER_SESSION_DAEMON_SECRET": secret,
        "ASTRORDER_SESSION_DAEMON_PORT": str(port),
        "ASTRORDER_SESSION_DAEMON_DB": str(db_path),
        "ASTRORDER_SESSION_DAEMON_CONFIG": str(config),
    }

    process = subprocess.Popen([exe], env=env, stdout=log, stderr=log)
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        assert process.poll() is None, "isolated Go daemon exited during startup"
        try:
            reader, writer = await asyncio.open_connection("127.0.0.1", port)
            writer.close()
            await writer.wait_closed()
            break
        except OSError:
            await asyncio.sleep(0.05)
    else:
        raise RuntimeError("daemon did not start in time")

    ws = None
    try:
        ws = await websockets.connect(f"ws://127.0.0.1:{port}/ws/v1")
        # Handshake
        await ws.send(json.dumps({"action": "daemon.handshake", "request_id": "h-1", "secret": secret}))
        resp = json.loads(await ws.recv())
        assert resp["action"] == "daemon.handshake.result"

        # Disconnect unsupported runtime
        await ws.send(json.dumps({"action": "runtime.disconnect", "request_id": "d-1", "agent_type": "unknown"}))
        err_resp = json.loads(await ws.recv())
        assert err_resp["action"] == "error"
        assert "unsupported runtime type" in err_resp["detail"]

        # Disconnect codex-ssh when connection_id has no sessions
        await ws.send(json.dumps({"action": "runtime.disconnect", "request_id": "d-2", "agent_type": "codex-ssh", "connection_id": "conn-nonexistent"}))
        ok_resp = json.loads(await ws.recv())
        assert ok_resp["action"] == "runtime.disconnect.result"
        assert ok_resp["result"]["disconnected"] is True
        assert ok_resp["result"]["released_sessions"] == []

    finally:
        if ws is not None:
            await ws.close()
        if process.poll() is None:
            process.terminate()
            process.wait()
        log.close()
