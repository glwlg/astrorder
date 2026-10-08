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

from scripts.switch_to_go_daemon import check_daemon_authoritative_idle, probe_daemon_authoritative_idle

@pytest.mark.asyncio
async def test_switch_script_check_authoritative_idle_handles_various_shapes(tmp_path):
    # Test that probe_daemon_authoritative_idle correctly parses active vs idle states
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]

    secret = "secret-switch-test"
    mock_sessions = {
        "idle-1": {"status": "idle"},
        "idle-2": {"status": "idle"},
    }

    async def mock_handler(websocket):
        async for message in websocket:
            msg = json.loads(message)
            action = msg.get("action")
            if action == "daemon.handshake":
                if msg.get("secret") == secret:
                    await websocket.send(json.dumps({"action": "daemon.handshake.result", "status": "ok"}))
                else:
                    await websocket.send(json.dumps({"action": "daemon.handshake.result", "status": "error"}))
            elif action == "daemon.status":
                await websocket.send(json.dumps({
                    "action": "daemon.status.result",
                    "request_id": msg.get("request_id"),
                    "daemon_id": "test-epoch-1",
                    "sessions": mock_sessions,
                }))

    server = await websockets.serve(mock_handler, "127.0.0.1", port)
    try:
        # 1. Idle state
        st = await probe_daemon_authoritative_idle(port, secret)
        assert st["active_sessions"] == 0
        assert st["waiting_approvals"] == 0
        assert st["active_session_ids"] == []

        # 2. Running state
        mock_sessions["active-1"] = {"status": "running"}
        st = await probe_daemon_authoritative_idle(port, secret)
        assert st["active_sessions"] == 1
        assert "active-1" in st["active_session_ids"]

        # 3. Waiting approval state
        mock_sessions["approval-1"] = {"status": "waiting_approval"}
        st = await probe_daemon_authoritative_idle(port, secret)
        assert st["active_sessions"] == 2
        assert st["waiting_approvals"] == 1
        assert "approval-1" in st["active_session_ids"]

        # 4. Unknown/transitional state -> treated as safe active
        mock_sessions["trans-1"] = {"status": "starting"}
        st = await probe_daemon_authoritative_idle(port, secret)
        assert st["active_sessions"] == 3
        assert "trans-1" in st["active_session_ids"]

        # 6. Session status dictionary missing status key entirely
        mock_sessions["malformed-1"] = {"foo": "bar"}
        st = await probe_daemon_authoritative_idle(port, secret)
        assert st["active_sessions"] == 4
        assert "malformed-1" in st["active_session_ids"]

        # A malformed session cannot be counted as safely idle.
        mock_sessions["invalid-value"] = None
        with pytest.raises(RuntimeError, match="状态格式无效"):
            await probe_daemon_authoritative_idle(port, secret)
        del mock_sessions["invalid-value"]

        # 7. Non-dict sessions payload raises
        saved = mock_sessions
        mock_sessions = "not-a-dict"
        with pytest.raises(RuntimeError, match="缺少合法的 'sessions' 字典"):
            await probe_daemon_authoritative_idle(port, secret)
        mock_sessions = saved
    finally:
        server.close()
        await server.wait_closed()


@pytest.mark.parametrize("missing", ["secret", "db", "config"])
def test_switch_rejects_invalid_replacement_before_querying_or_stopping_daemon(tmp_path, monkeypatch, missing):
    from scripts import switch_to_go_daemon as switch
    binary = tmp_path / "astrorder-sessiond.exe"
    binary.write_bytes(b"fixture")
    config = tmp_path / "runtime.json"
    config.write_text('{"runtimes": []}')
    values = {"secret": "fixture", "db": str(tmp_path / "events.db"), "config": str(config)}
    values[missing] = ""
    monkeypatch.setattr("sys.argv", ["switch", "--go-bin", str(binary), "--secret", values["secret"],
                                   "--db-path", values["db"], "--config-path", values["config"]])
    monkeypatch.setattr(switch, "current_listening_pids", lambda *_: pytest.fail("invalid replacement touched live daemon"))
    with pytest.raises(SystemExit) as caught:
        switch.main()
    assert caught.value.code == 2
