from __future__ import annotations

import asyncio
import json

import pytest
import websockets
from connectors.codex.astrorder_codex_connector.transport import CodexTransport
from connectors.hermes.astrorder_hermes_plugin.transport import HermesTransport


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "transport_type", [HermesTransport, CodexTransport], ids=["hermes", "codex"]
)
async def test_connector_transport_keeps_outbound_events_after_an_inbound_server_event(
    transport_type,
):
    inbound_sent = asyncio.Event()
    received: asyncio.Queue[dict[str, object]] = asyncio.Queue()

    async def handler(socket):
        await socket.recv()  # connector hello
        await socket.send(json.dumps({"type": "event", "event": {"type": "agent.upsert"}}))
        inbound_sent.set()
        try:
            frame = json.loads(await asyncio.wait_for(socket.recv(), timeout=1))
        except TimeoutError:
            return
        await received.put(frame)

    server = await websockets.serve(handler, "127.0.0.1", 0)
    port = server.sockets[0].getsockname()[1]
    transport = transport_type(
        f"ws://127.0.0.1:{port}", "test-only", lambda _command: None
    )
    try:
        transport.start({"id": "hermes-transport-test"})
        await asyncio.wait_for(inbound_sent.wait(), timeout=1)
        await asyncio.sleep(0.1)
        transport.send_event({"id": "event-after-inbound"})

        assert await asyncio.wait_for(received.get(), timeout=1) == {
            "type": "event",
            "event": {"id": "event-after-inbound"},
        }
    finally:
        transport.stop()
        # Cancelled asyncio.to_thread queue reads in a failed implementation need wakeups.
        for _ in range(3):
            transport._outgoing.put(None)
        server.close()
        await server.wait_closed()
