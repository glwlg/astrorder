import asyncio
import json

import pytest
import websockets

from astrorder.daemon.session_daemon import DaemonProtocolError, SessionDaemon


def test_session_sync_replays_only_frames_after_client_checkpoint():
    daemon = SessionDaemon(capacity=3)
    daemon.record("session-a", "token_chunk", {"delta": "A"}, timestamp=10.0, status="running")
    daemon.record("session-a", "token_chunk", {"delta": "B"}, timestamp=11.0)
    daemon.record("session-a", "turn_completed", {"turn_id": "turn-1"}, timestamp=12.0, status="idle")

    replay = daemon.sync({"session-a": 1})

    assert replay == {
        "session-a": {
            "frames": [
                {
                    "session_id": "session-a",
                    "seq_id": 2,
                    "timestamp": 11.0,
                    "event": "token_chunk",
                    "payload": {"delta": "B"},
                },
                {
                    "session_id": "session-a",
                    "seq_id": 3,
                    "timestamp": 12.0,
                    "event": "turn_completed",
                    "payload": {"turn_id": "turn-1"},
                },
            ],
            "overflow": False,
            "min_seq_id": 1,
            "max_seq_id": 3,
            "status": "idle",
        }
    }


def test_session_sync_marks_overflow_when_checkpoint_predates_retained_wal():
    daemon = SessionDaemon(capacity=2)
    daemon.record("session-a", "token_chunk", {"delta": "A"}, timestamp=10.0)
    daemon.record("session-a", "token_chunk", {"delta": "B"}, timestamp=11.0)
    daemon.record("session-a", "token_chunk", {"delta": "C"}, timestamp=12.0, status="running")

    replay = daemon.sync({"session-a": 0})["session-a"]

    assert replay["overflow"] is True
    assert replay["min_seq_id"] == 2
    assert replay["max_seq_id"] == 3
    assert [frame["seq_id"] for frame in replay["frames"]] == [2, 3]
    assert replay["status"] == "running"


def test_daemon_status_identifies_the_running_daemon_instance():
    daemon = SessionDaemon(capacity=3, daemon_id="daemon-test")
    daemon.record("session-a", "token_chunk", {"delta": "A"})

    status = daemon.handle_request('{"action":"daemon.status","request_id":"status-1"}')
    replay = daemon.handle_request(
        '{"action":"session.sync","request_id":"sync-1","sessions":{"session-a":0}}'
    )

    assert status["daemon_id"] == "daemon-test"
    assert replay["daemon_id"] == "daemon-test"


def test_daemon_status_includes_registered_runtime_status():
    class FixtureRuntime:
        async def spawn(self, request):
            return {"status": "idle"}

        async def command(self, action, request):
            return {"status": "idle"}

        def status(self):
            return {"desktop_cdp": {"available": True}}

    daemon = SessionDaemon(secret="test-secret")
    daemon.register_runtime("codex", FixtureRuntime())

    status = daemon.handle_request('{"action":"daemon.status","request_id":"status-1"}')

    assert status["runtimes"] == {"codex": {"desktop_cdp": {"available": True}}}


@pytest.mark.asyncio
async def test_daemon_shutdown_requires_confirmation_for_active_sessions():
    stopping = asyncio.Event()
    daemon = SessionDaemon(secret="test-secret", shutdown_event=stopping)
    daemon.record("session-a", "turn_started", {}, status="running")

    refused = await daemon.handle_message(
        '{"action":"daemon.shutdown","request_id":"stop-1"}'
    )
    accepted = await daemon.handle_message(
        '{"action":"daemon.shutdown","request_id":"stop-2","confirm_active":true}'
    )

    assert refused == {
        "action": "error",
        "request_id": "stop-1",
        "detail": "daemon has active sessions; explicit confirmation is required",
    }
    assert accepted["result"] == {"stopping": True}
    assert stopping.is_set()


def test_runtime_registry_requires_a_daemon_secret():
    class FixtureRuntime:
        async def spawn(self, request):
            del request
            return {"status": "idle"}

        async def command(self, action, request):
            del action, request
            return {"status": "idle"}

    with pytest.raises(ValueError, match="secret"):
        SessionDaemon().register_runtime("fixture", FixtureRuntime())


def test_external_runtime_adapters_require_a_daemon_secret():
    from astrorder.daemon.session_daemon import create_session_daemon

    class FixtureRuntime:
        async def spawn(self, request):
            del request
            return {"status": "idle"}

        async def command(self, action, request):
            del action, request
            return {"status": "idle"}

    with pytest.raises(ValueError, match="daemon secret"):
        create_session_daemon(hermes_runtime=FixtureRuntime())
    with pytest.raises(ValueError, match="daemon secret"):
        create_session_daemon(ssh_runtime=FixtureRuntime())

    daemon = create_session_daemon(
        secret="test-only-daemon-secret",
        hermes_runtime=FixtureRuntime(),
        ssh_runtime=FixtureRuntime(),
    )
    assert set(daemon._runtime_registry) == {"hermes", "ssh"}


def test_external_runtime_adapter_receives_daemon_emitter_when_supported():
    from astrorder.daemon.session_daemon import create_session_daemon

    class FixtureRuntime:
        def __init__(self) -> None:
            self.emit = None

        def set_emitter(self, emit):
            self.emit = emit

        async def spawn(self, request):
            del request
            return {"status": "idle"}

        async def command(self, action, request):
            del action, request
            return {"status": "idle"}

    runtime = FixtureRuntime()
    daemon = create_session_daemon(
        secret="test-only-daemon-secret",
        connector_secret="connector-test-secret",
        hermes_runtime=runtime,
    )

    assert runtime.emit == daemon.publish


@pytest.mark.asyncio
async def test_daemon_shutdown_closes_only_registered_runtime_owners():
    class FixtureRuntime:
        def __init__(self) -> None:
            self.shutdown_calls = 0

        async def spawn(self, request):
            del request
            return {"status": "idle"}

        async def command(self, action, request):
            del action, request
            return {"status": "idle"}

        async def shutdown(self):
            self.shutdown_calls += 1

    daemon = SessionDaemon(secret="test-only-daemon-secret")
    runtime = FixtureRuntime()
    daemon.register_runtime("fixture", runtime)

    await daemon.shutdown()

    assert runtime.shutdown_calls == 1


@pytest.mark.asyncio
async def test_reattached_spawn_retains_daemon_confirmed_runtime_metadata():
    class FixtureRuntime:
        async def spawn(self, request):
            del request
            return {
                "status": "idle",
                "agent_id": "daemon-hermes",
                "source_id": "hermes-local-opaque-source",
            }

        async def command(self, action, request):
            del action, request
            return {"status": "idle"}

    daemon = SessionDaemon(secret="test-only-daemon-secret")
    daemon.register_runtime("hermes", FixtureRuntime())
    request = {
        "action": "session.spawn",
        "session_id": "opaque-runtime-binding",
        "agent_type": "hermes",
        "params": {},
    }

    await daemon._spawn_runtime(request)
    attached = await daemon._spawn_runtime(request)

    assert attached["result"] == {
        "status": "idle",
        "agent_id": "daemon-hermes",
        "source_id": "hermes-local-opaque-source",
        "attached": True,
    }


@pytest.mark.asyncio
async def test_created_session_reattach_retains_daemon_confirmed_runtime_metadata():
    class FixtureRuntime:
        async def spawn(self, request):
            raise AssertionError(f"created session must reattach without respawn: {request}")

        async def create(self, request):
            del request
            return {
                "session_id": "native-created-session",
                "status": "idle",
                "agent_id": "daemon-hermes",
                "source_id": "hermes-local-opaque-source",
            }

        async def command(self, action, request):
            del action, request
            return {"status": "idle"}

    daemon = SessionDaemon(secret="test-only-daemon-secret")
    daemon.register_runtime("hermes", FixtureRuntime())
    await daemon._create_runtime({"action": "session.create", "agent_type": "hermes"})

    attached = await daemon._spawn_runtime(
        {
            "action": "session.spawn",
            "session_id": "native-created-session",
            "agent_type": "hermes",
            "params": {},
        }
    )

    assert attached["result"] == {
        "session_id": "native-created-session",
        "status": "idle",
        "agent_id": "daemon-hermes",
        "source_id": "hermes-local-opaque-source",
        "attached": True,
    }


@pytest.mark.asyncio
async def test_native_delete_releases_only_the_exact_created_session_binding():
    class FixtureRuntime:
        async def spawn(self, request):
            del request
            return {"status": "idle", "agent_id": "daemon-hermes"}

        async def create(self, request):
            del request
            return {
                "session_id": "native-delete-target",
                "status": "idle",
                "agent_id": "daemon-hermes",
            }

        async def command(self, action, request):
            assert action == "session.delete"
            return {"status": "idle", "deleted": request["session_id"]}

    daemon = SessionDaemon(secret="test-only-daemon-secret")
    daemon.register_runtime("hermes", FixtureRuntime())
    await daemon._create_runtime({"action": "session.create", "agent_type": "hermes"})

    result = await daemon._dispatch_runtime_action(
        "session.delete", {"session_id": "native-delete-target"}
    )

    assert result["result"]["deleted"] == "native-delete-target"
    assert "native-delete-target" not in daemon._session_runtimes
    assert "native-delete-target" not in daemon._session_runtime_metadata
    assert "native-delete-target" not in daemon.status()


@pytest.mark.asyncio
async def test_runtime_disconnect_requires_control_binding_and_releases_its_exact_adapter_sessions():
    class FixtureRuntime:
        def __init__(self) -> None:
            self.disconnect_calls = 0

        async def spawn(self, request):
            del request
            return {"status": "idle", "agent_id": "daemon-hermes"}

        async def command(self, action, request):
            del action, request
            return {"status": "idle"}

        async def disconnect(self):
            self.disconnect_calls += 1

    runtime = FixtureRuntime()
    daemon = SessionDaemon(secret="test-only-daemon-secret")
    daemon.register_runtime("hermes", runtime)
    await daemon._spawn_runtime(
        {
            "action": "session.spawn",
            "session_id": "runtime-control-binding",
            "agent_type": "hermes",
            "params": {"runtime_control": True},
        }
    )
    await daemon._spawn_runtime(
        {
            "action": "session.spawn",
            "session_id": "ordinary-native-session",
            "agent_type": "hermes",
            "params": {},
        }
    )
    with pytest.raises(DaemonProtocolError, match="runtime control binding"):
        await daemon._dispatch_runtime_action(
            "session.disconnect",
            {"action": "session.disconnect", "session_id": "ordinary-native-session"},
        )

    result = await daemon._dispatch_runtime_action(
        "session.disconnect",
        {"action": "session.disconnect", "session_id": "runtime-control-binding"},
    )

    assert result["result"] == {"status": "idle", "disconnected": True}
    assert runtime.disconnect_calls == 1
    assert "runtime-control-binding" not in daemon._session_runtimes
    assert "ordinary-native-session" not in daemon._session_runtimes
    with pytest.raises(DaemonProtocolError, match="not daemon-owned"):
        await daemon._dispatch_runtime_action(
            "session.send",
            {"action": "session.send", "session_id": "ordinary-native-session"},
        )


@pytest.mark.asyncio
async def test_runtime_disconnect_uses_registry_reported_session_scope_when_available():
    class MultiplexedRuntime:
        async def spawn(self, request):
            del request
            return {"status": "idle"}

        async def command(self, action, request):
            del action, request
            return {"status": "idle"}

        async def disconnect_session(self, session_id):
            assert session_id == "connection-a-control"
            return ("connection-a-control", "connection-a-native")

    runtime = MultiplexedRuntime()
    daemon = SessionDaemon(secret="test-only-daemon-secret")
    daemon.register_runtime("ssh", runtime)
    for session_id, runtime_control in (
        ("connection-a-control", True),
        ("connection-a-native", False),
        ("connection-b-native", False),
    ):
        await daemon._spawn_runtime(
            {
                "action": "session.spawn",
                "session_id": session_id,
                "agent_type": "ssh",
                "params": {"runtime_control": runtime_control},
            }
        )

    await daemon._dispatch_runtime_action(
        "session.disconnect",
        {"action": "session.disconnect", "session_id": "connection-a-control"},
    )

    assert "connection-a-control" not in daemon._session_runtimes
    assert "connection-a-native" not in daemon._session_runtimes
    assert daemon._session_runtimes["connection-b-native"] is runtime


@pytest.mark.asyncio
async def test_authenticated_daemon_shutdown_stops_runtime_and_signals_runner():
    class FixtureRuntime:
        async def spawn(self, request):
            del request
            return {"status": "idle"}

        async def command(self, action, request):
            del action, request
            return {"status": "idle"}

        async def shutdown(self):
            shutdowns.append(True)

    stopping = asyncio.Event()
    shutdowns: list[bool] = []
    daemon = SessionDaemon(
        daemon_id="daemon-shutdown",
        secret="test-only-daemon-secret",
        shutdown_event=stopping,
    )
    daemon.register_runtime("fixture", FixtureRuntime())

    response = await daemon.handle_message('{"action":"daemon.shutdown","request_id":"shutdown-1"}')

    assert response == {
        "action": "daemon.shutdown.result",
        "request_id": "shutdown-1",
        "daemon_id": "daemon-shutdown",
        "result": {"stopping": True},
    }
    assert stopping.is_set()
    assert shutdowns == [True]


@pytest.mark.asyncio
async def test_websocket_daemon_replays_sync_reports_status_and_streams_live_frame():
    daemon = SessionDaemon(capacity=3)
    daemon.record("session-a", "token_chunk", {"delta": "A"}, timestamp=10.0, status="running")
    daemon.record("session-a", "token_chunk", {"delta": "B"}, timestamp=11.0)
    server = await daemon.serve("127.0.0.1", 0)
    port = server.sockets[0].getsockname()[1]

    try:
        async with websockets.connect(f"ws://127.0.0.1:{port}") as socket:
            await socket.send(
                json.dumps(
                    {
                        "action": "session.sync",
                        "request_id": "sync-1",
                        "sessions": {"session-a": 1},
                    }
                )
            )
            replay = json.loads(await socket.recv())
            assert replay["action"] == "session.sync.result"
            assert replay["request_id"] == "sync-1"
            assert [frame["seq_id"] for frame in replay["sessions"]["session-a"]["frames"]] == [2]

            await socket.send(json.dumps({"action": "daemon.status", "request_id": "status-1"}))
            status = json.loads(await socket.recv())
            assert status == {
                "action": "daemon.status.result",
                "request_id": "status-1",
                "daemon_id": daemon.daemon_id,
                "sessions": {
                    "session-a": {"status": "running", "min_seq_id": 1, "max_seq_id": 2}
                },
                "connectors": [],
                "runtimes": {},
            }

            live = await daemon.publish(
                "session-a",
                "approval_requested",
                {"request_id": "approval-1"},
                timestamp=12.0,
                status="waiting_approval",
            )
            assert json.loads(await socket.recv()) == live
    finally:
        server.close()
        await server.wait_closed()


@pytest.mark.asyncio
async def test_model_config_reload_waits_for_active_agent_sessions():
    class Runtime:
        def __init__(self):
            self.reload_calls = 0

        async def spawn(self, _request):
            return {"status": "running"}

        async def command(self, _action, _request):
            return {"status": "idle"}

        async def reload_config(self):
            self.reload_calls += 1

    daemon = SessionDaemon(secret="test-only-daemon-secret")
    runtime = Runtime()
    daemon.register_runtime("codex", runtime)
    await daemon._spawn_runtime({"session_id": "active", "agent_type": "codex", "params": {}})

    response = await daemon.handle_message(json.dumps({
        "action": "model_config.reload", "request_id": "reload-1", "agents": ["codex"],
    }))
    assert response["result"] == {"reloaded": [], "pending": ["codex"]}
    assert runtime.reload_calls == 0

    daemon._sessions["active"].status = "idle"
    assert await daemon._drain_config_reloads() == ["codex"]
    assert runtime.reload_calls == 1


@pytest.mark.asyncio
async def test_websocket_daemon_waits_for_sync_before_streaming_live_frames():
    daemon = SessionDaemon(capacity=3)
    server = await daemon.serve("127.0.0.1", 0)
    port = server.sockets[0].getsockname()[1]

    try:
        async with websockets.connect(f"ws://127.0.0.1:{port}") as socket:
            await socket.send(json.dumps({"action": "daemon.status", "request_id": "status-1"}))
            assert json.loads(await socket.recv())["action"] == "daemon.status.result"

            await daemon.publish("session-a", "token_chunk", {"delta": "before sync"})
            with pytest.raises(TimeoutError):
                await asyncio.wait_for(socket.recv(), timeout=0.05)

            await socket.send(
                json.dumps(
                    {
                        "action": "session.sync",
                        "request_id": "sync-1",
                        "sessions": {"session-a": 0},
                    }
                )
            )
            assert json.loads(await socket.recv())["action"] == "session.sync.result"

            live = await daemon.publish("session-a", "token_chunk", {"delta": "after sync"})
            assert json.loads(await socket.recv()) == live
    finally:
        server.close()
        await server.wait_closed()


@pytest.mark.asyncio
async def test_connector_agent_update_refreshes_daemon_status_snapshot():
    daemon = SessionDaemon()
    daemon._connector_agents["hermes-1"] = {
        "id": "hermes-1",
        "kind": "hermes",
        "status": "connecting",
    }

    await daemon._record_connector_event(
        "hermes-1",
        {
            "id": "agent-ready-1",
            "type": "agent.upsert",
            "agent_id": "hermes-1",
            "session_id": None,
            "data": {"id": "hermes-1", "kind": "hermes", "status": "ready"},
        },
    )

    assert daemon._connector_agents["hermes-1"]["status"] == "ready"


@pytest.mark.asyncio
async def test_websocket_daemon_routes_runtime_actions_by_registered_session_identity():
    class FixtureRuntime:
        def __init__(self) -> None:
            self.spawned: list[dict[str, object]] = []
            self.commands: list[tuple[str, dict[str, object]]] = []

        async def spawn(self, request):
            self.spawned.append(dict(request))
            return {"status": "running", "runtime_handle": "opaque-runtime-handle"}

        async def command(self, action, request):
            self.commands.append((action, dict(request)))
            return {"status": "idle", "accepted": True}

    daemon = SessionDaemon(
        capacity=3,
        daemon_id="daemon-runtime",
        secret="test-only-daemon-secret",
    )
    runtime = FixtureRuntime()
    daemon.register_runtime("fixture", runtime)
    server = await daemon.serve("127.0.0.1", 0)
    port = server.sockets[0].getsockname()[1]

    try:
        async with websockets.connect(f"ws://127.0.0.1:{port}") as socket:
            await socket.send(
                json.dumps(
                    {
                        "action": "daemon.handshake",
                        "request_id": "handshake-1",
                        "secret": "test-only-daemon-secret",
                    }
                )
            )
            assert json.loads(await socket.recv())["action"] == "daemon.handshake.result"
            await socket.send(
                json.dumps(
                    {
                        "action": "session.spawn",
                        "request_id": "spawn-1",
                        "session_id": "session-opaque",
                        "agent_type": "fixture",
                        "connection_id": "connection-opaque",
                        "cwd": "C:/workspace/opaque",
                        "params": {"model": "fixture-model"},
                    }
                )
            )
            spawned = json.loads(await socket.recv())
            assert spawned == {
                "action": "session.spawn.result",
                "request_id": "spawn-1",
                "daemon_id": "daemon-runtime",
                "session_id": "session-opaque",
                "result": {"status": "running", "runtime_handle": "opaque-runtime-handle"},
            }

            await socket.send(
                json.dumps(
                    {
                        "action": "session.spawn",
                        "request_id": "spawn-2",
                        "session_id": "session-opaque",
                        "agent_type": "fixture",
                        "params": {},
                    }
                )
            )
            assert json.loads(await socket.recv()) == {
                "action": "session.spawn.result",
                "request_id": "spawn-2",
                "daemon_id": "daemon-runtime",
                "session_id": "session-opaque",
                "result": {
                    "status": "running",
                    "runtime_handle": "opaque-runtime-handle",
                    "attached": True,
                },
            }

            await socket.send(
                json.dumps(
                    {
                        "action": "session.send",
                        "request_id": "send-1",
                        "session_id": "session-opaque",
                        "turn_id": "turn-opaque",
                        "prompt": "same text is not identity",
                        "attachments": [],
                    }
                )
            )
            sent = json.loads(await socket.recv())
            assert sent["action"] == "session.send.result"
            assert sent["result"] == {"status": "idle", "accepted": True}

            await socket.send(
                json.dumps(
                    {
                        "action": "session.interrupt",
                        "request_id": "interrupt-1",
                        "session_id": "other-session",
                        "turn_id": "turn-opaque",
                    }
                )
            )
            rejected = json.loads(await socket.recv())
            assert rejected == {
                "action": "error",
                "request_id": "interrupt-1",
                "detail": "session is not daemon-owned",
            }

        assert runtime.spawned == [
            {
                "action": "session.spawn",
                "request_id": "spawn-1",
                "session_id": "session-opaque",
                "agent_type": "fixture",
                "connection_id": "connection-opaque",
                "cwd": "C:/workspace/opaque",
                "params": {"model": "fixture-model"},
            }
        ]
        assert runtime.commands == [
            (
                "session.send",
                {
                    "action": "session.send",
                    "request_id": "send-1",
                    "session_id": "session-opaque",
                    "turn_id": "turn-opaque",
                    "prompt": "same text is not identity",
                    "attachments": [],
                },
            )
        ]
        assert daemon.status()["session-opaque"]["status"] == "idle"
    finally:
        server.close()
        await server.wait_closed()


@pytest.mark.asyncio
async def test_websocket_daemon_requires_handshake_before_secret_protected_ipc():
    daemon = SessionDaemon(
        capacity=3,
        daemon_id="daemon-auth",
        secret="test-only-daemon-secret",
    )
    server = await daemon.serve("127.0.0.1", 0)
    port = server.sockets[0].getsockname()[1]

    try:
        async with websockets.connect(f"ws://127.0.0.1:{port}") as socket:
            await socket.send(json.dumps({"action": "daemon.status", "request_id": "status-1"}))
            assert json.loads(await socket.recv()) == {
                "action": "error",
                "request_id": "status-1",
                "detail": "daemon handshake is required",
            }

            await socket.send(
                json.dumps(
                    {
                        "action": "daemon.handshake",
                        "request_id": "handshake-1",
                        "secret": "test-only-daemon-secret",
                    }
                )
            )
            assert json.loads(await socket.recv()) == {
                "action": "daemon.handshake.result",
                "request_id": "handshake-1",
                "daemon_id": "daemon-auth",
            }

            await socket.send(json.dumps({"action": "daemon.status", "request_id": "status-2"}))
            assert json.loads(await socket.recv())["action"] == "daemon.status.result"
    finally:
        server.close()
        await server.wait_closed()
