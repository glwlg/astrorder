from __future__ import annotations

import asyncio
import json

import pytest
import websockets

from astrorder.daemon.session_daemon import SessionDaemon


@pytest.mark.asyncio
async def test_daemon_connector_proxy_buffers_agent_event_then_wals_exact_native_session_events():
    class FixtureRuntime:
        async def spawn(self, request):
            del request
            return {"status": "idle", "agent_id": "daemon-hermes-agent"}

        async def command(self, action, request):
            del action, request
            return {"status": "idle"}

    daemon = SessionDaemon(
        secret="test-only-daemon-secret",
        connector_secret="connector-test-secret",
    )
    daemon.register_runtime("hermes", FixtureRuntime())
    server = await daemon.serve("127.0.0.1", 0)
    port = server.sockets[0].getsockname()[1]
    agent = {
        "id": "daemon-hermes-agent",
        "kind": "hermes",
        "name": "Daemon Hermes",
        "status": "ready",
        "capabilities": ["chat", "events"],
        "source_id": "hermes-local-opaque-source",
    }

    try:
        async with websockets.connect(
            f"ws://127.0.0.1:{port}/ws/v1/connector",
            additional_headers={"Authorization": "Bearer connector-test-secret"},
        ) as socket:
            await socket.send(json.dumps({"type": "hello", "protocol_version": 1, "agent": agent}))
            await socket.send(
                json.dumps(
                    {
                        "type": "event",
                        "event": {
                            "id": "agent-event-before-binding",
                            "type": "agent.upsert",
                            "agent_id": "daemon-hermes-agent",
                            "session_id": None,
                            "data": agent,
                        },
                    }
                )
            )
            await asyncio.sleep(0.05)

            await daemon._spawn_runtime(
                {
                    "action": "session.spawn",
                    "session_id": "daemon-hermes-control",
                    "agent_type": "hermes",
                    "params": {"runtime_control": True},
                }
            )
            agent_frames = daemon.sync({"daemon-hermes-control": 0})["daemon-hermes-control"]["frames"]
            assert agent_frames == [
                {
                    "session_id": "daemon-hermes-control",
                    "seq_id": 1,
                    "timestamp": pytest.approx(agent_frames[0]["timestamp"]),
                    "event": "connector.event",
                    "payload": {
                        "id": "agent-event-before-binding",
                        "type": "agent.upsert",
                        "agent_id": "daemon-hermes-agent",
                        "session_id": None,
                        "data": agent,
                    },
                }
            ]

            await socket.send(
                json.dumps(
                    {
                        "type": "event",
                        "event": {
                            "id": "session-event-after-binding",
                            "type": "session.upsert",
                            "agent_id": "daemon-hermes-agent",
                            "session_id": "native-hermes-session",
                            "data": {
                                "id": "native-hermes-session",
                                "agent_id": "daemon-hermes-agent",
                                "title": "native",
                                "status": "running",
                            },
                        },
                    }
                )
            )
            deadline = asyncio.get_running_loop().time() + 1
            while "native-hermes-session" not in daemon.sync({"native-hermes-session": 0}):
                assert asyncio.get_running_loop().time() < deadline
                await asyncio.sleep(0.01)

            session_frames = daemon.sync({"native-hermes-session": 0})["native-hermes-session"]["frames"]
            assert session_frames[0]["event"] == "connector.event"
            assert session_frames[0]["payload"]["id"] == "session-event-after-binding"
    finally:
        server.close()
        await server.wait_closed()
        await daemon.shutdown()
