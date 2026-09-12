from __future__ import annotations

import asyncio
import json
import os
import shutil
from pathlib import Path

import pytest
import websockets

from astrorder.daemon.codex_runtime import CodexDaemonRuntime, CodexDaemonRuntimeConfig
from astrorder.daemon.session_daemon import SessionDaemon


@pytest.mark.asyncio
@pytest.mark.skipif(
    os.getenv("ASTRORDER_RUN_NATIVE_CODEX_E2E") != "1",
    reason="set ASTRORDER_RUN_NATIVE_CODEX_E2E=1 for the isolated native lifecycle test",
)
async def test_native_codex_thread_survives_app_control_disconnect_and_reattaches(tmp_path: Path):
    executable = shutil.which("codex")
    if not executable:
        pytest.skip("codex executable is unavailable")

    daemon = SessionDaemon(capacity=32, daemon_id="native-e2e-daemon", secret="native-e2e-secret")
    runtime = CodexDaemonRuntime(
        CodexDaemonRuntimeConfig(
            executable=executable,
            workspace=tmp_path,
            allowed_workspaces=(tmp_path,),
            agent_id="native-e2e-codex",
            agent_name="Native E2E Codex",
        ),
        emit=daemon.publish,
    )
    daemon.register_runtime("codex", runtime)
    server = await daemon.serve("127.0.0.1", 0)
    endpoint = f"ws://127.0.0.1:{server.sockets[0].getsockname()[1]}"
    session_id: str | None = None
    try:
        async with websockets.connect(endpoint) as first_app:
            await first_app.send(json.dumps({"action": "daemon.handshake", "secret": "native-e2e-secret"}))
            assert json.loads(await first_app.recv())["action"] == "daemon.handshake.result"
            await first_app.send(
                json.dumps(
                    {
                        "action": "session.create",
                        "request_id": "native-create",
                        "agent_type": "codex",
                        "cwd": str(tmp_path),
                        "ephemeral": True,
                    }
                )
            )
            created = json.loads(await first_app.recv())
            assert created["action"] == "session.create.result"
            session_id = created["result"]["session_id"]
            assert created["result"]["status"] == "idle"
            await first_app.send(
                json.dumps(
                    {
                        "action": "session.send",
                        "request_id": "native-turn",
                        "session_id": session_id,
                        "input": [
                            {
                                "type": "text",
                                "text": (
                                    "Create a file named daemon-survived.txt containing exactly "
                                    "daemon-survived and do not modify anything else."
                                ),
                            }
                        ],
                        "params": {
                            "approvalPolicy": "on-request",
                            "sandboxPolicy": {"type": "workspaceWrite"},
                            "approvalsReviewer": "auto_review",
                        },
                    }
                )
            )
            sent = json.loads(await first_app.recv())
            assert sent["action"] == "session.send.result"
            assert sent["result"]["accepted"] is True

        deadline = asyncio.get_running_loop().time() + 180
        while daemon.status()[session_id]["status"] not in {"idle", "error"}:
            if asyncio.get_running_loop().time() >= deadline:
                raise AssertionError("native Codex turn did not reach a terminal daemon state")
            await asyncio.sleep(0.1)
        assert daemon.status()[session_id]["status"] == "idle"
        assert (tmp_path / "daemon-survived.txt").read_text(encoding="utf-8") == "daemon-survived"

        async with websockets.connect(endpoint) as restarted_app:
            await restarted_app.send(
                json.dumps({"action": "daemon.handshake", "secret": "native-e2e-secret"})
            )
            await restarted_app.recv()
            await restarted_app.send(
                json.dumps(
                    {
                        "action": "session.spawn",
                        "request_id": "native-reattach",
                        "session_id": session_id,
                        "agent_type": "codex",
                    }
                )
            )
            reattached = json.loads(await restarted_app.recv())
            assert reattached["action"] == "session.spawn.result"
            assert reattached["result"]["status"] == "idle"
            assert reattached["result"]["attached"] is True
            await restarted_app.send(
                json.dumps(
                    {
                        "action": "session.sync",
                        "request_id": "native-replay",
                        "sessions": {session_id: 0},
                    }
                )
            )
            replay = json.loads(await restarted_app.recv())
            frames = replay["sessions"][session_id]["frames"]
            assert any(
                frame["event"] == "codex.notification"
                and frame["payload"].get("frame", {}).get("method") == "turn/completed"
                for frame in frames
            )
        assert session_id in daemon.status()
    finally:
        await runtime.shutdown()
        server.close()
        await server.wait_closed()
