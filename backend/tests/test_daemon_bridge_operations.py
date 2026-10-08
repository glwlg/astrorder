"""Operation negotiation and lost-reply recovery over real loopback WebSockets."""
import json

import pytest
import websockets

from astrorder.daemon.bridge import DaemonBridge, DaemonBridgeError


@pytest.mark.asyncio
@pytest.mark.parametrize("capable", [True, False])
async def test_bridge_recovers_lost_create_reply_without_dispatching_twice(capable):
    calls = []
    receipt = None

    async def handler(socket):
        nonlocal receipt
        async for raw in socket:
            request = json.loads(raw)
            calls.append(request)
            action = request["action"]
            reply = {"action": action + ".result", "request_id": request["request_id"], "daemon_id": "test"}
            if action == "daemon.handshake":
                if capable:
                    reply["operations"] = {"version": 1, "lookup": True}
            elif action == "session.create":
                receipt = {**reply, "session_id": "native-one", "result": {"session_id": "native-one"}}
                await socket.close()
                return
            elif action == "operation.read":
                assert request["operation_id"] == next(c["operation_id"] for c in calls if c["action"] == "session.create")
                assert request["operation_action"] == "session.create"
                reply.update(operation_state="completed", response=receipt)
            await socket.send(json.dumps(reply))

    async with websockets.serve(handler, "127.0.0.1", 0) as server:
        port = server.sockets[0].getsockname()[1]
        bridge = DaemonBridge(None, None, f"ws://127.0.0.1:{port}", secret="test", request_timeout=0.2)
        if capable:
            result = await bridge.request_control("session.create", {"agent_type": "codex"})
            assert result["session_id"] == "native-one"
        else:
            with pytest.raises(websockets.ConnectionClosed):
                await bridge.request_control("session.create", {"agent_type": "codex"})
    creates = [c for c in calls if c["action"] == "session.create"]
    assert len(creates) == 1
    assert ("operation_id" in creates[0]) is capable


@pytest.mark.asyncio
async def test_bridge_keeps_operation_identity_when_native_result_is_uncertain():
    calls = []

    async def handler(socket):
        async for raw in socket:
            request = json.loads(raw)
            calls.append(request)
            action = request["action"]
            reply = {"action": action + ".result", "request_id": request["request_id"], "daemon_id": "test"}
            if action == "daemon.handshake":
                reply["operations"] = {"version": 1, "lookup": True}
            else:
                reply.update(action="error", detail="native outcome uncertain", operation_id=request["command_id"], operation_state="uncertain")
            await socket.send(json.dumps(reply))

    async with websockets.serve(handler, "127.0.0.1", 0) as server:
        port = server.sockets[0].getsockname()[1]
        bridge = DaemonBridge(None, None, f"ws://127.0.0.1:{port}", secret="test", request_timeout=0.2)
        with pytest.raises(DaemonBridgeError) as captured:
            await bridge.request_control("session.send", {"session_id": "s", "command_id": "stable-command"})
        assert getattr(captured.value, "operation_id", None) == "stable-command"
        assert getattr(captured.value, "operation_state", None) == "uncertain"
    assert len([c for c in calls if c["action"] == "session.send"]) == 1
