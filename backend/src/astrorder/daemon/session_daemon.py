"""In-memory per-session WAL used by the independent Session Daemon.

This Phase 1 module deliberately owns no Hermes/Codex runtime yet. It establishes
one invariant for later runtime migration: every event for a session receives a
monotonic sequence ID and can be replayed after the App Server reconnects.
"""
from __future__ import annotations

import argparse
import asyncio
import hmac
import json
import logging
import os
import time
from collections import deque
from collections.abc import Mapping
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Protocol
from uuid import uuid4

from websockets.exceptions import ConnectionClosed

from .errors import DaemonProtocolError

logger = logging.getLogger(__name__)

SESSION_STATUSES = frozenset({"running", "waiting_approval", "idle", "error"})
RUNTIME_ACTIONS = frozenset(
    {
        "session.send",
        "session.compact",
        "session.review",
        "session.steer",
        "session.interrupt",
        "session.approve",
        "session.settings",
        "session.resize",
        "session.disconnect",
        "session.history_page",
        "session.delete",
        "session.rename",
        "session.close",
        "session.models",
        "session.commands",
        "session.model.read",
        "session.model.set",
        "session.reasoning.set",
        "session.approval.read",
        "session.approval.set",
    }
)




class SessionRuntime(Protocol):
    """A daemon-owned adapter for one registered native Agent family."""

    async def spawn(self, request: Mapping[str, Any]) -> Mapping[str, Any]: ...

    async def create(self, request: Mapping[str, Any]) -> Mapping[str, Any]: ...

    async def command(self, action: str, request: Mapping[str, Any]) -> Mapping[str, Any]: ...


@dataclass
class _SessionWAL:
    capacity: int
    frames: deque[dict[str, Any]] = field(init=False)
    next_seq_id: int = 1
    status: str = "idle"

    def __post_init__(self) -> None:
        self.frames = deque(maxlen=self.capacity)


class SessionDaemon:
    """Per-session bounded WAL and authoritative status projection.

    The class is intentionally transport-independent. Phase 1 tests use it
    directly; the WebSocket process introduced next delegates to this exact core
    so replay semantics cannot diverge from in-process behavior.
    """

    def __init__(
        self,
        *,
        capacity: int = 2000,
        daemon_id: str | None = None,
        secret: str | None = None,
        connector_secret: str | None = None,
        shutdown_event: asyncio.Event | None = None,
    ) -> None:
        if capacity < 1:
            raise ValueError("capacity must be positive")
        if daemon_id is not None and (
            not isinstance(daemon_id, str) or not daemon_id or len(daemon_id) > 128
        ):
            raise ValueError("daemon_id must be a non-empty string up to 128 characters")
        if secret is not None and (
            not isinstance(secret, str) or not secret or len(secret) > 1024
        ):
            raise ValueError("daemon secret must be a non-empty string up to 1024 characters")
        if connector_secret is not None and (
            not isinstance(connector_secret, str) or not connector_secret or len(connector_secret) > 1024
        ):
            raise ValueError("connector secret must be a non-empty string up to 1024 characters")
        self.capacity = capacity
        self.daemon_id = daemon_id or uuid4().hex
        self._secret = secret
        self._connector_secret = connector_secret
        self._sessions: dict[str, _SessionWAL] = {}
        self._subscribers: set[Any] = set()
        self._publish_lock = asyncio.Lock()
        self._runtime_registry: dict[str, SessionRuntime] = {}
        self._session_runtimes: dict[str, SessionRuntime] = {}
        self._session_agent_types: dict[str, str] = {}
        self._session_runtime_metadata: dict[str, dict[str, Any]] = {}
        self._runtime_control_sessions: set[str] = set()
        self._agent_control_sessions: dict[str, str] = {}
        self._pending_connector_events: dict[str, deque[dict[str, Any]]] = {}
        self._connector_agents: dict[str, dict[str, Any]] = {}
        self._connector_sockets: dict[str, Any] = {}
        self._runtime_lock = asyncio.Lock()
        self._maintenance = False
        self._shutdown_event = shutdown_event

    def register_runtime(self, agent_type: str, runtime: SessionRuntime) -> None:
        """Register one explicit runtime adapter; no type-name guessing is allowed."""
        if self._secret is None:
            raise ValueError("a daemon secret is required before registering runtime adapters")
        if not isinstance(agent_type, str) or not agent_type or len(agent_type) > 64:
            raise ValueError("agent_type must be a non-empty string up to 64 characters")
        if agent_type in self._runtime_registry:
            raise ValueError("agent_type is already registered")
        if not callable(getattr(runtime, "spawn", None)) or not callable(
            getattr(runtime, "command", None)
        ):
            raise TypeError("runtime must provide async spawn and command methods")
        self._runtime_registry[agent_type] = runtime

    def record(
        self,
        session_id: str,
        event: str,
        payload: Mapping[str, Any],
        *,
        timestamp: float | None = None,
        status: str | None = None,
    ) -> dict[str, Any]:
        """Append one immutable daemon frame and return its canonical envelope."""
        if not isinstance(session_id, str) or not session_id:
            raise DaemonProtocolError("session_id must be a non-empty string")
        if not isinstance(event, str) or not event:
            raise DaemonProtocolError("event must be a non-empty string")
        if not isinstance(payload, Mapping):
            raise DaemonProtocolError("payload must be an object")
        if status is not None and status not in SESSION_STATUSES:
            raise DaemonProtocolError("status is invalid")
        if timestamp is None:
            timestamp = time.time()
        if not isinstance(timestamp, (int, float)):
            raise DaemonProtocolError("timestamp must be numeric")

        wal = self._sessions.setdefault(session_id, _SessionWAL(capacity=self.capacity))
        if status is not None:
            wal.status = status
        frame = {
            "session_id": session_id,
            "seq_id": wal.next_seq_id,
            "timestamp": float(timestamp),
            "event": event,
            "payload": dict(payload),
        }
        wal.next_seq_id += 1
        wal.frames.append(frame)
        return dict(frame)

    def sync(self, checkpoints: Mapping[str, int]) -> dict[str, dict[str, Any]]:
        """Return frames newer than each App Server checkpoint.

        A checkpoint below the oldest retained sequence is an explicit overflow:
        callers receive the retained tail and must perform a native full snapshot
        refresh before treating that tail as complete history.
        """
        if not isinstance(checkpoints, Mapping):
            raise DaemonProtocolError("sessions must be an object")
        result: dict[str, dict[str, Any]] = {}
        for session_id, checkpoint in checkpoints.items():
            if not isinstance(session_id, str) or not session_id:
                raise DaemonProtocolError("session_id must be a non-empty string")
            if not isinstance(checkpoint, int) or checkpoint < 0:
                raise DaemonProtocolError("checkpoint must be a non-negative integer")
            wal = self._sessions.get(session_id)
            if wal is None:
                continue
            frames = list(wal.frames)
            min_seq_id = frames[0]["seq_id"] if frames else wal.next_seq_id
            max_seq_id = wal.next_seq_id - 1
            result[session_id] = {
                "frames": [dict(frame) for frame in frames if frame["seq_id"] > checkpoint],
                "overflow": bool(frames and checkpoint < min_seq_id - 1),
                "min_seq_id": min_seq_id,
                "max_seq_id": max_seq_id,
                "status": wal.status,
            }
        return result

    def status(self) -> dict[str, dict[str, Any]]:
        """Return each daemon-owned session's authoritative runtime projection."""
        result: dict[str, dict[str, Any]] = {}
        for session_id, wal in self._sessions.items():
            frames = list(wal.frames)
            result[session_id] = {
                "status": wal.status,
                "min_seq_id": frames[0]["seq_id"] if frames else wal.next_seq_id,
                "max_seq_id": wal.next_seq_id - 1,
            }
        return result

    def runtime_status(self) -> dict[str, dict[str, Any]]:
        result: dict[str, dict[str, Any]] = {}
        for agent_type, runtime in self._runtime_registry.items():
            status = getattr(runtime, "status", None)
            if callable(status):
                value = status()
                if not isinstance(value, Mapping):
                    raise DaemonProtocolError("runtime status must be an object")
                result[agent_type] = dict(value)
        return result

    async def publish(
        self,
        session_id: str,
        event: str,
        payload: Mapping[str, Any],
        *,
        timestamp: float | None = None,
        status: str | None = None,
    ) -> dict[str, Any]:
        """Record one frame and fan it out to currently synced App Servers."""
        async with self._publish_lock:
            frame = self.record(session_id, event, payload, timestamp=timestamp, status=status)
            encoded = json.dumps(frame, ensure_ascii=False, separators=(",", ":"))
            stale: list[Any] = []
            for socket in tuple(self._subscribers):
                try:
                    await socket.send(encoded)
                except (ConnectionClosed, OSError):
                    stale.append(socket)
            for socket in stale:
                self._subscribers.discard(socket)
            return frame

    async def serve(self, host: str = "127.0.0.1", port: int = 30009):
        """Start the daemon's loopback IPC WebSocket listener."""
        if host not in {"127.0.0.1", "::1", "localhost"}:
            raise ValueError("Session Daemon must bind to loopback")
        if not 0 <= port <= 65535:
            raise ValueError("port must be between 0 and 65535")
        import websockets

        return await websockets.serve(self._serve_socket, host, port, max_size=16_000_000)

    async def shutdown(self) -> None:
        """Stop only runtime adapters explicitly registered by this daemon."""
        async with self._runtime_lock:
            runtimes = list(dict.fromkeys(self._runtime_registry.values()))
            self._runtime_registry.clear()
            self._session_runtimes.clear()
            self._session_agent_types.clear()
            self._session_runtime_metadata.clear()
            self._runtime_control_sessions.clear()
            self._agent_control_sessions.clear()
            self._pending_connector_events.clear()
            self._connector_agents.clear()
            self._connector_sockets.clear()
        shutdowns = [getattr(runtime, "shutdown", None) for runtime in runtimes]
        await asyncio.gather(
            *(shutdown() for shutdown in shutdowns if callable(shutdown)),
        )

    async def _serve_socket(self, socket: Any) -> None:
        path = getattr(getattr(socket, "request", None), "path", "/")
        path = path.split("?", 1)[0] if isinstance(path, str) else "/"
        if path == "/ws/v1/connector":
            await self._handle_connector_socket(socket)
            return
        if path != "/":
            await socket.close(code=1008, reason="unsupported daemon endpoint")
            return
        await self._handle_socket(socket)

    async def _handle_socket(self, socket: Any) -> None:
        authenticated = self._secret is None
        try:
            async for raw in socket:
                if not authenticated:
                    response, authenticated = self._handle_handshake(raw)
                    await socket.send(
                        json.dumps(response, ensure_ascii=False, separators=(",", ":"))
                    )
                    continue
                if self._is_sync_request(raw):
                    async with self._publish_lock:
                        response = await self.handle_message(raw)
                        await socket.send(
                            json.dumps(response, ensure_ascii=False, separators=(",", ":"))
                        )
                        if response.get("action") == "session.sync.result":
                            self._subscribers.add(socket)
                else:
                    response = await self.handle_message(raw)
                    await socket.send(
                        json.dumps(response, ensure_ascii=False, separators=(",", ":"))
                    )
        finally:
            self._subscribers.discard(socket)

    async def _handle_connector_socket(self, socket: Any) -> None:
        if not self._connector_authenticated(socket):
            await socket.close(code=4401, reason="connector authentication failed")
            return
        agent_id: str | None = None
        try:
            async for raw in socket:
                try:
                    frame = json.loads(raw)
                except (TypeError, json.JSONDecodeError):
                    raise DaemonProtocolError("connector frame is invalid") from None
                if not isinstance(frame, Mapping):
                    raise DaemonProtocolError("connector frame is invalid")
                if agent_id is None:
                    agent = frame.get("agent")
                    candidate = agent.get("id") if isinstance(agent, Mapping) else None
                    if (
                        frame.get("type") != "hello"
                        or frame.get("protocol_version") != 1
                        or not isinstance(candidate, str)
                        or not candidate
                    ):
                        raise DaemonProtocolError("connector hello is invalid")
                    agent_id = candidate
                    self._connector_agents[agent_id] = dict(agent)
                    self._connector_sockets[agent_id] = socket
                    await self._record_connector_hello(agent_id, agent)
                    continue
                if frame.get("type") == "event":
                    event = frame.get("event")
                    if not isinstance(event, Mapping):
                        raise DaemonProtocolError("connector event is invalid")
                    await self._record_connector_event(agent_id, event)
                elif frame.get("type") == "ping":
                    await socket.send(json.dumps({"type": "pong"}, separators=(",", ":")))
                else:
                    raise DaemonProtocolError("connector frame type is unsupported")
        except DaemonProtocolError:
            await socket.close(code=1008, reason="invalid connector frame")
        except ConnectionClosed:
            return
        finally:
            if agent_id is not None and self._connector_sockets.get(agent_id) is socket:
                self._connector_sockets.pop(agent_id, None)
                agent = self._connector_agents.pop(agent_id, None)
                if agent is not None:
                    await self._record_connector_hello(agent_id, {**agent, "status": "disconnected"})

    def _connector_authenticated(self, socket: Any) -> bool:
        if self._connector_secret is None:
            return False
        headers = getattr(getattr(socket, "request", None), "headers", None)
        authorization = headers.get("Authorization") if headers is not None else None
        prefix = "Bearer "
        if not isinstance(authorization, str) or not authorization.startswith(prefix):
            return False
        supplied = authorization[len(prefix) :]
        return bool(supplied) and hmac.compare_digest(supplied, self._connector_secret)

    async def _record_connector_hello(self, agent_id: str, agent: Mapping[str, Any]) -> None:
        payload = dict(agent)
        control_session_id = self._agent_control_sessions.get(agent_id)
        if control_session_id is not None:
            await self.publish(control_session_id, "connector.hello", payload)
            return
        pending = self._pending_connector_events.setdefault(agent_id, deque(maxlen=200))
        pending.append({"_daemon_event": "connector.hello", "agent": payload})

    async def _record_connector_event(self, agent_id: str, event: Mapping[str, Any]) -> None:
        if event.get("agent_id") != agent_id:
            raise DaemonProtocolError("connector event agent identity does not match hello")
        event_type = event.get("type")
        session_id = event.get("session_id")
        if not isinstance(event_type, str) or not event_type:
            raise DaemonProtocolError("connector event type is invalid")
        if session_id is not None and (not isinstance(session_id, str) or not session_id):
            raise DaemonProtocolError("connector event session identity is invalid")
        payload = dict(event)
        if event_type == "agent.upsert":
            agent = event.get("data")
            if not isinstance(agent, Mapping) or agent.get("id") != agent_id:
                raise DaemonProtocolError("connector agent update identity does not match hello")
            current = self._connector_agents.get(agent_id)
            if current is not None:
                self._connector_agents[agent_id] = {**current, **dict(agent)}
        if session_id is not None:
            await self.publish(session_id, "connector.event", payload)
            return
        control_session_id = self._agent_control_sessions.get(agent_id)
        if control_session_id is not None:
            await self.publish(control_session_id, "connector.event", payload)
            return
        pending = self._pending_connector_events.setdefault(agent_id, deque(maxlen=200))
        pending.append(payload)

    def _handle_handshake(self, raw: str) -> tuple[dict[str, Any], bool]:
        try:
            request = json.loads(raw)
        except (TypeError, json.JSONDecodeError):
            return {"action": "error", "detail": "daemon handshake is required"}, False
        if not isinstance(request, dict):
            return {"action": "error", "detail": "daemon handshake is required"}, False
        request_id = request.get("request_id")
        if request_id is not None and (not isinstance(request_id, str) or not request_id):
            return {"action": "error", "detail": "request_id must be a non-empty string"}, False
        if request.get("action") != "daemon.handshake":
            return {
                "action": "error",
                "request_id": request_id,
                "detail": "daemon handshake is required",
            }, False
        supplied = request.get("secret")
        if not isinstance(supplied, str) or self._secret is None or not hmac.compare_digest(
            supplied, self._secret
        ):
            return {
                "action": "error",
                "request_id": request_id,
                "detail": "daemon authentication failed",
            }, False
        return {
            "action": "daemon.handshake.result",
            "request_id": request_id,
            "daemon_id": self.daemon_id,
        }, True

    @staticmethod
    def _is_sync_request(raw: str) -> bool:
        try:
            request = json.loads(raw)
        except (TypeError, json.JSONDecodeError):
            return False
        return isinstance(request, dict) and request.get("action") == "session.sync"

    async def handle_message(self, raw: str) -> dict[str, Any]:
        """Handle one transport request, including registered runtime actions."""
        try:
            request = json.loads(raw)
        except (TypeError, json.JSONDecodeError):
            return self.handle_request(raw)
        if not isinstance(request, dict):
            return self.handle_request(raw)
        action = request.get("action")
        if action not in {
            "daemon.shutdown", "daemon.status", "session.create", "session.spawn",
            "runtime.request", *RUNTIME_ACTIONS,
        }:
            return self.handle_request(raw)
        request_id = request.get("request_id")
        if request_id is not None and (not isinstance(request_id, str) or not request_id):
            return {"action": "error", "detail": "request_id must be a non-empty string"}
        try:
            if action == "daemon.status":
                return {
                    "action": "daemon.status.result",
                    "request_id": request_id,
                    "daemon_id": self.daemon_id,
                    "sessions": self.status(),
                    "connectors": [dict(agent) for agent in self._connector_agents.values()],
                    "runtimes": await asyncio.to_thread(self.runtime_status),
                }
            if action == "daemon.shutdown":
                return await self._shutdown_request(request)
            if action == "session.create":
                return await self._create_runtime(request)
            if action == "session.spawn":
                return await self._spawn_runtime(request)
            if action == "runtime.request":
                return await self._request_runtime(request)
            return await self._dispatch_runtime_action(action, request)
        except DaemonProtocolError as exc:
            return {"action": "error", "request_id": request_id, "detail": str(exc)}
        except Exception:
            logger.exception("daemon runtime action failed: %s", action)
            return {"action": "error", "request_id": request_id, "detail": "runtime action failed"}

    async def _request_runtime(self, request: Mapping[str, Any]) -> dict[str, Any]:
        agent_type = request.get("agent_type")
        runtime = self._runtime_registry.get(agent_type) if isinstance(agent_type, str) else None
        query = getattr(runtime, "query", None)
        if not callable(query):
            raise DaemonProtocolError("agent type does not support runtime requests")
        async with self._runtime_lock:
            if self._maintenance:
                raise DaemonProtocolError("daemon is stopping")
            result = await query(dict(request))
        if not isinstance(result, Mapping):
            raise DaemonProtocolError("runtime request result must be an object")
        return {
            "action": "runtime.request.result",
            "request_id": request.get("request_id"),
            "daemon_id": self.daemon_id,
            "result": dict(result),
        }

    async def _shutdown_request(self, request: Mapping[str, Any]) -> dict[str, Any]:
        if self._secret is None:
            raise DaemonProtocolError("daemon shutdown requires authenticated IPC")
        confirm_active = request.get("confirm_active", False)
        if not isinstance(confirm_active, bool):
            raise DaemonProtocolError("confirm_active must be a boolean")
        async with self._runtime_lock:
            active = {
                session_id: wal.status
                for session_id, wal in self._sessions.items()
                if wal.status in {"running", "waiting_approval"}
            }
            if active and not confirm_active:
                raise DaemonProtocolError(
                    "daemon has active sessions; explicit confirmation is required"
                )
            self._maintenance = True
        await self.shutdown()
        if self._shutdown_event is not None:
            self._shutdown_event.set()
        return {
            "action": "daemon.shutdown.result",
            "request_id": request.get("request_id"),
            "daemon_id": self.daemon_id,
            "result": {"stopping": True},
        }

    async def _create_runtime(self, request: Mapping[str, Any]) -> dict[str, Any]:
        agent_type = request.get("agent_type")
        if not isinstance(agent_type, str) or not agent_type:
            raise DaemonProtocolError("agent_type must be a non-empty string")
        runtime = self._runtime_registry.get(agent_type)
        create = getattr(runtime, "create", None)
        if runtime is None or not callable(create):
            raise DaemonProtocolError("agent type does not support daemon session creation")
        async with self._runtime_lock:
            if self._maintenance:
                raise DaemonProtocolError("daemon is stopping")
            result = await create(dict(request))
            if not isinstance(result, Mapping):
                raise DaemonProtocolError("runtime create result must be an object")
            result_data = dict(result)
            session_id = result_data.get("session_id")
            self._validate_session_id(session_id)
            if session_id in self._session_runtimes:
                raise DaemonProtocolError("runtime created a duplicate session identity")
            status = result_data.get("status", "idle")
            if status not in SESSION_STATUSES:
                raise DaemonProtocolError("runtime create status is invalid")
            try:
                json.dumps(result_data)
            except (TypeError, ValueError) as exc:
                raise DaemonProtocolError("runtime create result is not JSON-safe") from exc
            wal = self._sessions.setdefault(session_id, _SessionWAL(capacity=self.capacity))
            wal.status = status
            self._session_runtimes[session_id] = runtime
            self._session_agent_types[session_id] = agent_type
            self._session_runtime_metadata[session_id] = result_data
            return {
                "action": "session.create.result",
                "request_id": request.get("request_id"),
                "daemon_id": self.daemon_id,
                "session_id": session_id,
                "result": result_data,
            }

    async def _spawn_runtime(self, request: Mapping[str, Any]) -> dict[str, Any]:
        session_id = request.get("session_id")
        self._validate_session_id(session_id)
        agent_type = request.get("agent_type")
        if not isinstance(agent_type, str) or not agent_type:
            raise DaemonProtocolError("agent_type must be a non-empty string")
        params = request.get("params", {})
        if not isinstance(params, Mapping):
            raise DaemonProtocolError("params must be an object")
        runtime_control = params.get("runtime_control", False)
        if not isinstance(runtime_control, bool):
            raise DaemonProtocolError("runtime_control must be a boolean")
        runtime = self._runtime_registry.get(agent_type)
        if runtime is None:
            raise DaemonProtocolError("agent type is not registered")
        async with self._runtime_lock:
            if self._maintenance:
                raise DaemonProtocolError("daemon is stopping")
            if session_id in self._session_runtimes:
                if self._session_agent_types.get(session_id) != agent_type:
                    raise DaemonProtocolError("session is owned by a different agent type")
                wal = self._sessions[session_id]
                result_data = dict(self._session_runtime_metadata.get(session_id, {}))
                result_data.update({"status": wal.status, "attached": True})
                if runtime_control:
                    await self._bind_agent_control_session(session_id, result_data)
                return {
                    "action": "session.spawn.result",
                    "request_id": request.get("request_id"),
                    "daemon_id": self.daemon_id,
                    "session_id": session_id,
                    "result": result_data,
                }
            result = await runtime.spawn(dict(request))
            if not isinstance(result, Mapping):
                raise DaemonProtocolError("runtime spawn result must be an object")
            result_data = dict(result)
            status = result_data.get("status", "idle")
            if status not in SESSION_STATUSES:
                raise DaemonProtocolError("runtime spawn status is invalid")
            try:
                json.dumps(result_data)
            except (TypeError, ValueError) as exc:
                raise DaemonProtocolError("runtime spawn result is not JSON-safe") from exc
            wal = self._sessions.setdefault(session_id, _SessionWAL(capacity=self.capacity))
            wal.status = status
            self._session_runtimes[session_id] = runtime
            self._session_agent_types[session_id] = agent_type
            self._session_runtime_metadata[session_id] = result_data
            if runtime_control:
                self._runtime_control_sessions.add(session_id)
                await self._bind_agent_control_session(session_id, result_data)
            return {
                "action": "session.spawn.result",
                "request_id": request.get("request_id"),
                "daemon_id": self.daemon_id,
                "session_id": session_id,
                "result": result_data,
            }

    async def _dispatch_runtime_action(
        self, action: str, request: Mapping[str, Any]
    ) -> dict[str, Any]:
        session_id = request.get("session_id")
        self._validate_session_id(session_id)
        if action == "session.disconnect":
            return await self._disconnect_runtime(session_id, request)
        async with self._runtime_lock:
            if self._maintenance:
                raise DaemonProtocolError("daemon is stopping")
            runtime = self._session_runtimes.get(session_id)
            if runtime is None:
                raise DaemonProtocolError("session is not daemon-owned")
            result = await runtime.command(action, dict(request))
            if not isinstance(result, Mapping):
                raise DaemonProtocolError("runtime command result must be an object")
            result_data = dict(result)
            status = result_data.get("status")
            if status is not None:
                if status not in SESSION_STATUSES:
                    raise DaemonProtocolError("runtime command status is invalid")
                self._sessions[session_id].status = status
            try:
                json.dumps(result_data)
            except (TypeError, ValueError) as exc:
                raise DaemonProtocolError("runtime command result is not JSON-safe") from exc
            if (
                action == "session.delete" and result_data.get("deleted") == session_id
                or action == "session.close" and result_data.get("closed") is True
            ):
                self._session_runtimes.pop(session_id, None)
                self._session_agent_types.pop(session_id, None)
                self._session_runtime_metadata.pop(session_id, None)
                self._runtime_control_sessions.discard(session_id)
                self._sessions.pop(session_id, None)
        return {
            "action": f"{action}.result",
            "request_id": request.get("request_id"),
            "daemon_id": self.daemon_id,
            "session_id": session_id,
            "result": result_data,
        }

    async def _disconnect_runtime(
        self, session_id: str, request: Mapping[str, Any]
    ) -> dict[str, Any]:
        async with self._runtime_lock:
            if session_id not in self._runtime_control_sessions:
                raise DaemonProtocolError("session is not a runtime control binding")
            runtime = self._session_runtimes.get(session_id)
            if runtime is None:
                raise DaemonProtocolError("session is not daemon-owned")
            disconnect_session = getattr(runtime, "disconnect_session", None)
            if callable(disconnect_session):
                reported_ids = await disconnect_session(session_id)
                if (
                    not isinstance(reported_ids, (list, tuple, set, frozenset))
                    or session_id not in reported_ids
                    or not all(isinstance(released_id, str) and released_id for released_id in reported_ids)
                ):
                    raise DaemonProtocolError("runtime returned invalid scoped disconnect sessions")
                released_ids = list(reported_ids)
                if any(self._session_runtimes.get(released_id) is not runtime for released_id in released_ids):
                    raise DaemonProtocolError("runtime returned a session outside its ownership")
            else:
                disconnect = getattr(runtime, "disconnect", None)
                if not callable(disconnect):
                    raise DaemonProtocolError("runtime does not support scoped disconnect")
                await disconnect()
                released_ids = [
                    owned_session_id
                    for owned_session_id, owned_runtime in self._session_runtimes.items()
                    if owned_runtime is runtime
                ]
            for released_id in released_ids:
                self._session_runtimes.pop(released_id, None)
                self._session_agent_types.pop(released_id, None)
                self._session_runtime_metadata.pop(released_id, None)
                self._runtime_control_sessions.discard(released_id)
                if released_id in self._sessions:
                    self._sessions[released_id].status = "idle"
            for agent_id, control_session_id in tuple(self._agent_control_sessions.items()):
                if control_session_id in released_ids:
                    self._agent_control_sessions.pop(agent_id, None)
        return {
            "action": "session.disconnect.result",
            "request_id": request.get("request_id"),
            "daemon_id": self.daemon_id,
            "session_id": session_id,
            "result": {"status": "idle", "disconnected": True},
        }

    async def _bind_agent_control_session(
        self, control_session_id: str, metadata: Mapping[str, Any]
    ) -> None:
        agent_id = metadata.get("agent_id")
        if not isinstance(agent_id, str) or not agent_id:
            return
        self._agent_control_sessions[agent_id] = control_session_id
        pending = tuple(self._pending_connector_events.pop(agent_id, ()))
        for event in pending:
            if event.get("_daemon_event") == "connector.hello":
                await self.publish(control_session_id, "connector.hello", event["agent"])
            else:
                await self.publish(control_session_id, "connector.event", event)

    @staticmethod
    def _validate_session_id(session_id: Any) -> None:
        if not isinstance(session_id, str) or not session_id:
            raise DaemonProtocolError("session_id must be a non-empty string")

    def handle_request(self, raw: str) -> dict[str, Any]:
        """Handle one local IPC control message without executing a runtime action."""
        try:
            request = json.loads(raw)
        except (TypeError, json.JSONDecodeError):
            return {"action": "error", "detail": "invalid JSON"}
        if not isinstance(request, dict):
            return {"action": "error", "detail": "request must be an object"}
        action = request.get("action")
        request_id = request.get("request_id")
        if request_id is not None and (not isinstance(request_id, str) or not request_id):
            return {"action": "error", "detail": "request_id must be a non-empty string"}
        try:
            if action == "session.sync":
                return {
                    "action": "session.sync.result",
                    "request_id": request_id,
                    "daemon_id": self.daemon_id,
                    "sessions": self.sync(request.get("sessions", {})),
                }
            if action == "daemon.status":
                return {
                    "action": "daemon.status.result",
                    "request_id": request_id,
                    "daemon_id": self.daemon_id,
                    "sessions": self.status(),
                    "connectors": [dict(agent) for agent in self._connector_agents.values()],
                    "runtimes": self.runtime_status(),
                }
        except DaemonProtocolError as exc:
            return {"action": "error", "request_id": request_id, "detail": str(exc)}
        return {"action": "error", "request_id": request_id, "detail": "unsupported action"}


def create_session_daemon(
    *,
    capacity: int = 2000,
    daemon_id: str | None = None,
    secret: str | None = None,
    connector_secret: str | None = None,
    codex_config: Any | None = None,
    pty_config: Any | None = None,
    hermes_runtime: Any | None = None,
    ssh_runtime: Any | None = None,
    remote_codex_runtime: Any | None = None,
    grok_runtime: Any | None = None,
    remote_grok_runtime: Any | None = None,
    shutdown_event: asyncio.Event | None = None,
) -> SessionDaemon:
    """Create a daemon and register only explicitly enabled runtime adapters."""
    if (
        codex_config is not None
        or pty_config is not None
        or hermes_runtime is not None
        or ssh_runtime is not None
        or remote_codex_runtime is not None
        or grok_runtime is not None
        or remote_grok_runtime is not None
    ) and secret is None:
        raise ValueError("a daemon secret is required when registering runtime adapters")
    daemon = SessionDaemon(
        capacity=capacity,
        daemon_id=daemon_id,
        secret=secret,
        connector_secret=connector_secret,
        shutdown_event=shutdown_event,
    )
    if codex_config is not None:
        from .codex_runtime import CodexDaemonRuntime

        daemon.register_runtime("codex", CodexDaemonRuntime(codex_config, emit=daemon.publish))
    if pty_config is not None:
        from .pty_runtime import PtyDaemonRuntime

        daemon.register_runtime("pty", PtyDaemonRuntime(pty_config, emit=daemon.publish))
    for agent_type, runtime in (
        ("hermes", hermes_runtime),
        ("ssh", ssh_runtime),
        ("codex-ssh", remote_codex_runtime),
        ("grok", grok_runtime),
        ("grok-ssh", remote_grok_runtime),
    ):
        if runtime is None:
            continue
        set_emitter = getattr(runtime, "set_emitter", None)
        if callable(set_emitter):
            set_emitter(daemon.publish)
        daemon.register_runtime(agent_type, runtime)
    return daemon


async def _run_forever(
    host: str,
    port: int,
    capacity: int,
    *,
    secret: str | None = None,
    connector_secret: str | None = None,
    codex_config: Any | None = None,
    pty_config: Any | None = None,
    hermes_runtime: Any | None = None,
    ssh_runtime: Any | None = None,
    remote_codex_runtime: Any | None = None,
    grok_runtime: Any | None = None,
    remote_grok_runtime: Any | None = None,
) -> None:
    stopping = asyncio.Event()
    daemon = create_session_daemon(
        capacity=capacity,
        secret=secret,
        connector_secret=connector_secret,
        codex_config=codex_config,
        pty_config=pty_config,
        hermes_runtime=hermes_runtime,
        ssh_runtime=ssh_runtime,
        remote_codex_runtime=remote_codex_runtime,
        grok_runtime=grok_runtime,
        remote_grok_runtime=remote_grok_runtime,
        shutdown_event=stopping,
    )
    codex_observer_task = None
    if codex_config is not None:
        from .codex_runtime import forward_codex_desktop_stops

        codex_observer_task = asyncio.create_task(
            forward_codex_desktop_stops(codex_config, daemon.publish, stopping)
        )
    server = await daemon.serve(host, port)
    try:
        await stopping.wait()
    finally:
        if codex_observer_task is not None:
            codex_observer_task.cancel()
            await asyncio.gather(codex_observer_task, return_exceptions=True)
        server.close()
        await server.wait_closed()
        await daemon.shutdown()


def main(argv: list[str] | None = None) -> None:
    parser = argparse.ArgumentParser(description="Astrorder Session Daemon")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=30009)
    parser.add_argument("--capacity", type=int, default=2000)
    parser.add_argument("--enable-codex", action="store_true")
    parser.add_argument("--codex-executable")
    parser.add_argument("--codex-workspace")
    parser.add_argument("--codex-allowed-workspace", action="append", default=[])
    parser.add_argument("--codex-agent-id", default="daemon-codex")
    parser.add_argument("--codex-agent-name", default="Daemon Codex")
    parser.add_argument("--enable-pty", action="store_true")
    parser.add_argument("--pty-allowed-workspace", action="append", default=[])
    parser.add_argument("--enable-hermes", action="store_true")
    parser.add_argument("--enable-ssh", action="store_true")
    parser.add_argument("--enable-grok", action="store_true")
    parser.add_argument("--grok-executable")
    parser.add_argument("--grok-workspace")
    parser.add_argument("--grok-allowed-workspace", action="append", default=[])
    args = parser.parse_args(argv)
    secret = os.environ.get("ASTRORDER_SESSION_DAEMON_SECRET") or None
    connector_secret = os.environ.get("ASTRORDER_CONNECTOR_SECRET") or None
    codex_config = None
    if args.enable_codex:
        if not secret:
            parser.error("ASTRORDER_SESSION_DAEMON_SECRET is required with --enable-codex")
        if not args.codex_executable or not args.codex_workspace:
            parser.error("--codex-executable and --codex-workspace are required with --enable-codex")
        from .codex_runtime import CodexDaemonRuntimeConfig

        allowed_workspaces = args.codex_allowed_workspace or [args.codex_workspace]
        codex_config = CodexDaemonRuntimeConfig(
            executable=args.codex_executable,
            workspace=args.codex_workspace,
            allowed_workspaces=tuple(allowed_workspaces),
            agent_id=args.codex_agent_id,
            agent_name=args.codex_agent_name,
        )
    pty_config = None
    if args.enable_pty:
        if not secret:
            parser.error("ASTRORDER_SESSION_DAEMON_SECRET is required with --enable-pty")
        if not args.pty_allowed_workspace:
            parser.error("--pty-allowed-workspace is required with --enable-pty")
        from .pty_runtime import PtyDaemonRuntimeConfig

        pty_config = PtyDaemonRuntimeConfig(
            allowed_workspaces=tuple(args.pty_allowed_workspace),
        )
    grok_runtime = None
    if args.enable_grok:
        if not secret:
            parser.error("ASTRORDER_SESSION_DAEMON_SECRET is required with --enable-grok")
        if not args.grok_executable or not args.grok_workspace:
            parser.error("--grok-executable and --grok-workspace are required with --enable-grok")
        from .grok_runtime import GrokDaemonRuntime, GrokDaemonRuntimeConfig

        grok_runtime = GrokDaemonRuntime(
            GrokDaemonRuntimeConfig(
                executable=args.grok_executable,
                workspace=Path(args.grok_workspace),
                allowed_workspaces=tuple(
                    Path(item) for item in (args.grok_allowed_workspace or [args.grok_workspace])
                ),
            ),
            emit=lambda *_args, **_kwargs: None,
        )
    hermes_runtime = None
    if args.enable_hermes:
        if not secret:
            parser.error("ASTRORDER_SESSION_DAEMON_SECRET is required with --enable-hermes")
        if not connector_secret:
            parser.error("ASTRORDER_CONNECTOR_SECRET is required with --enable-hermes")
        from ..config import Settings
        from ..connections import LocalHermesController
        from .hermes_runtime import HermesDaemonRuntime

        hermes_settings = Settings(
            host="127.0.0.1",
            port=args.port,
            connector_secret=connector_secret,
            hermes_executable=os.environ.get("ASTRORDER_HERMES_EXECUTABLE") or None,
            auto_connect_local_hermes=False,
        )
        hermes_runtime = HermesDaemonRuntime(
            lambda: LocalHermesController(hermes_settings)
        )
    ssh_runtime = None
    remote_codex_runtime = None
    remote_grok_runtime = None
    if args.enable_ssh:
        if not secret:
            parser.error("ASTRORDER_SESSION_DAEMON_SECRET is required with --enable-ssh")
        if not connector_secret:
            parser.error("ASTRORDER_CONNECTOR_SECRET is required with --enable-ssh")
        from ..config import Settings
        from ..connections import validate_ssh_settings
        from ..ssh_transport import SshNativeRuntime
        from .ssh_runtime import SshDaemonRuntime, SshDaemonRuntimeRegistry

        project_root = Path(__file__).resolve().parents[4]
        ssh_attachment_settings = Settings(
            host="127.0.0.1",
            port=args.port,
            connector_secret=connector_secret,
            auto_connect_local_hermes=False,
        )

        def ssh_factory(connection_id: str, raw_settings: Mapping[str, Any]) -> SshDaemonRuntime:
            settings = validate_ssh_settings(dict(raw_settings))

            def controller_factory() -> SshNativeRuntime:
                controller = SshNativeRuntime(
                    settings,
                    connection_id,
                    args.port,
                    project_root,
                    project_root / ".hermes" / "plugins" / "astrorder-hermes",
                    connector_secret=connector_secret,
                )
                controller.app_settings = ssh_attachment_settings
                return controller

            return SshDaemonRuntime(
                controller_factory
            )

        ssh_runtime = SshDaemonRuntimeRegistry(ssh_factory)
        if args.enable_codex:
            from .remote_codex_runtime import RemoteCodexDaemonRuntime

            def remote_codex_factory(
                connection_id: str, raw_settings: Mapping[str, Any]
            ) -> RemoteCodexDaemonRuntime:
                return RemoteCodexDaemonRuntime(
                    connection_id,
                    raw_settings,
                    emit=lambda *_args, **_kwargs: None,
                )

            remote_codex_runtime = SshDaemonRuntimeRegistry(remote_codex_factory)
        if args.enable_grok:
            from .remote_grok_runtime import RemoteGrokDaemonRuntime

            def remote_grok_factory(
                connection_id: str, raw_settings: Mapping[str, Any]
            ) -> RemoteGrokDaemonRuntime:
                return RemoteGrokDaemonRuntime(
                    connection_id,
                    raw_settings,
                    emit=lambda *_args, **_kwargs: None,
                )

            remote_grok_runtime = SshDaemonRuntimeRegistry(remote_grok_factory)
    asyncio.run(
        _run_forever(
            args.host,
            args.port,
            args.capacity,
            secret=secret,
            connector_secret=connector_secret,
            codex_config=codex_config,
            pty_config=pty_config,
            hermes_runtime=hermes_runtime,
            ssh_runtime=ssh_runtime,
            remote_codex_runtime=remote_codex_runtime,
            grok_runtime=grok_runtime,
            remote_grok_runtime=remote_grok_runtime,
        )
    )


if __name__ == "__main__":
    main()
