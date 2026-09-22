"""App Server client for replaying durable Session Daemon WAL frames.

The daemon owns the event sequence; this bridge owns only App Server-side
projection.  A checkpoint advances *after* the existing connector event path
has durably accepted the frame, so reconnects can safely replay after an App
Server restart.
"""
from __future__ import annotations

import asyncio
import json
import logging
import threading
from collections.abc import Callable, Mapping
from dataclasses import dataclass
from typing import Any
from urllib.parse import urlparse
from uuid import uuid4

import websockets
from websockets.exceptions import ConnectionClosed, WebSocketException

from ..service import ControlService, ProtocolError
from ..store import Store

logger = logging.getLogger(__name__)

CONTROL_ACTIONS = frozenset(
    {
        "session.create",
        "session.spawn",
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
        "runtime.request",
    }
)
NativeFrameHandler = Callable[[str, Mapping[str, Any]], None]
StatusHandler = Callable[[Mapping[str, Any]], None]


class DaemonBridgeError(RuntimeError):
    """The local daemon returned a response outside the IPC contract."""


class DaemonReplayOverflow(DaemonBridgeError):
    """The daemon retained only a tail that cannot be treated as complete."""

    def __init__(self, daemon_id: str, session_id: str) -> None:
        self.daemon_id = daemon_id
        self.session_id = session_id
        super().__init__(f"daemon replay overflow for session {session_id}")


@dataclass(frozen=True)
class DaemonSyncReport:
    daemon_id: str
    replayed_frames: int
    overflowed_sessions: tuple[str, ...]


class DaemonBridge:
    """Reconnect-safe App Server projection client for one local daemon."""

    def __init__(
        self,
        store: Store,
        service: ControlService,
        endpoint: str,
        *,
        secret: str | None = None,
        request_timeout: float = 3.0,
        reconnect_delay: float = 1.0,
    ) -> None:
        self.store = store
        self.service = service
        self.endpoint = self._validate_endpoint(endpoint)
        if request_timeout <= 0:
            raise ValueError("request_timeout must be positive")
        if reconnect_delay <= 0:
            raise ValueError("reconnect_delay must be positive")
        if secret is not None and (
            not isinstance(secret, str) or not secret or len(secret) > 1024
        ):
            raise ValueError("daemon secret must be a non-empty string up to 1024 characters")
        self.request_timeout = request_timeout
        self.reconnect_delay = reconnect_delay
        self._secret = secret
        self._daemon_id: str | None = None
        self._overflowed_sessions: set[tuple[str, str]] = set()
        self._native_frame_handlers: dict[str, NativeFrameHandler] = {}
        self._native_frame_handlers_lock = threading.RLock()
        self._status_handlers: list[StatusHandler] = []
        self._runtime_status: dict[str, Any] = {}

    @property
    def runtime_status(self) -> Mapping[str, Any]:
        return dict(self._runtime_status)

    @staticmethod
    def _validate_endpoint(endpoint: str) -> str:
        if not isinstance(endpoint, str) or not endpoint:
            raise ValueError("daemon endpoint must be a non-empty WebSocket URL")
        parsed = urlparse(endpoint)
        if (
            parsed.scheme != "ws"
            or parsed.hostname not in {"127.0.0.1", "::1", "localhost"}
            or parsed.username is not None
            or parsed.password is not None
            or parsed.port is None
        ):
            raise ValueError("daemon endpoint must be an unauthenticated loopback ws URL")
        return endpoint

    async def synchronize_once(self) -> DaemonSyncReport:
        """Connect once, replay the retained WAL, then close the IPC socket."""
        async with websockets.connect(
            self.endpoint,
            open_timeout=self.request_timeout,
            close_timeout=self.request_timeout,
            max_size=16_000_000,
        ) as socket:
            await self._handshake(socket)
            return await self._synchronize(socket)

    async def request_control(self, action: str, fields: Mapping[str, Any]) -> dict[str, Any]:
        """Forward one explicit runtime control request on an isolated IPC socket."""
        if action not in CONTROL_ACTIONS:
            raise ValueError("daemon control action is unsupported")
        if not isinstance(fields, Mapping):
            raise TypeError("daemon control fields must be an object")
        async with websockets.connect(
            self.endpoint,
            open_timeout=self.request_timeout,
            close_timeout=self.request_timeout,
            max_size=16_000_000,
        ) as socket:
            await self._handshake(socket)
            response = await self._request(socket, action, fields)
        self._daemon_id_from(response)
        return response

    async def refresh_status(self) -> None:
        """Refresh App-side session state from the daemon authority."""
        async with websockets.connect(
            self.endpoint,
            open_timeout=self.request_timeout,
            close_timeout=self.request_timeout,
            max_size=16_000_000,
        ) as socket:
            await self._handshake(socket)
            status = await self._request(socket, "daemon.status", {})
        daemon_id, _ = self._apply_status(status)
        self._daemon_id = daemon_id

    def register_status_handler(self, handler: StatusHandler) -> Callable[[], None]:
        if not callable(handler):
            raise TypeError("daemon status handler must be callable")
        self._status_handlers.append(handler)

        def unregister() -> None:
            if handler in self._status_handlers:
                self._status_handlers.remove(handler)

        return unregister

    def register_native_frame_handler(
        self, event: str, handler: NativeFrameHandler
    ) -> Callable[[], None]:
        """Register one App-side projector for a daemon-native frame family.

        Native frames are intentionally not acknowledged until this handler has
        accepted them.  That makes an App Server restart safe even while its
        native runtime adapter has not completed registration.
        """
        if (
            not isinstance(event, str)
            or not event
            or event == "connector.event"
            or len(event) > 128
        ):
            raise ValueError("native frame event is invalid")
        if not callable(handler):
            raise TypeError("native frame handler must be callable")
        with self._native_frame_handlers_lock:
            if event in self._native_frame_handlers:
                raise ValueError("native frame handler is already registered")
            self._native_frame_handlers[event] = handler

        def unregister() -> None:
            with self._native_frame_handlers_lock:
                if self._native_frame_handlers.get(event) is handler:
                    self._native_frame_handlers.pop(event, None)

        return unregister

    async def run(self, stopping: asyncio.Event) -> None:
        """Keep an App Server projection connected until its lifespan ends."""
        while not stopping.is_set():
            try:
                async with websockets.connect(
                    self.endpoint,
                    open_timeout=self.request_timeout,
                    close_timeout=self.request_timeout,
                    max_size=16_000_000,
                ) as socket:
                    await self._handshake(socket)
                    await self._synchronize(socket)
                    await self._receive_live_frames(socket, stopping)
            except (ConnectionClosed, DaemonBridgeError, OSError, TimeoutError, WebSocketException) as exc:
                if not stopping.is_set():
                    logger.debug("Session daemon bridge disconnected: %s", exc)
            if not stopping.is_set():
                try:
                    await asyncio.wait_for(stopping.wait(), timeout=self.reconnect_delay)
                except TimeoutError:
                    continue

    async def _synchronize(self, socket: Any) -> DaemonSyncReport:
        status = await self._request(socket, "daemon.status", {})
        daemon_id, status_sessions = self._apply_status(status)
        identity_changed = self._daemon_id is not None and self._daemon_id != daemon_id
        if identity_changed:
            self._overflowed_sessions.clear()
        connectors = status.get("connectors")
        if not isinstance(connectors, list):
            raise DaemonBridgeError("daemon status connectors must be an array")
        for agent in connectors:
            self._project_connector_hello(agent)

        checkpoints = self.store.list_daemon_checkpoints(daemon_id)
        requested = dict(checkpoints)
        for session_id in status_sessions:
            self._validate_session_id(session_id)
            requested.setdefault(session_id, 0)

        replay = await self._request(socket, "session.sync", {"sessions": requested})
        if self._daemon_id_from(replay) != daemon_id:
            raise DaemonBridgeError("daemon identity changed during sync")
        responses = replay.get("sessions")
        if not isinstance(responses, Mapping):
            raise DaemonBridgeError("daemon sync sessions must be an object")

        replayed_frames = 0
        overflowed: list[str] = []
        for session_id, response in responses.items():
            self._validate_session_id(session_id)
            if not isinstance(response, Mapping):
                raise DaemonBridgeError("daemon sync session result must be an object")
            if response.get("overflow") is True:
                self._overflowed_sessions.add((daemon_id, session_id))
                overflowed.append(session_id)
                continue
            self._overflowed_sessions.discard((daemon_id, session_id))
            replayed_frames += self._project_frames(
                daemon_id,
                session_id,
                checkpoints.get(session_id, 0),
                response.get("frames"),
            )

        self._daemon_id = daemon_id
        return DaemonSyncReport(
            daemon_id=daemon_id,
            replayed_frames=replayed_frames,
            overflowed_sessions=tuple(overflowed),
        )

    def _apply_status(self, response: Mapping[str, Any]) -> tuple[str, Mapping[str, Any]]:
        daemon_id = self._daemon_id_from(response)
        runtimes = response.get("runtimes", {})
        if not isinstance(runtimes, Mapping) or any(
            not isinstance(key, str) or not isinstance(value, Mapping)
            for key, value in runtimes.items()
        ):
            raise DaemonBridgeError("daemon runtime status is invalid")
        self._runtime_status = {key: dict(value) for key, value in runtimes.items()}
        sessions = response.get("sessions")
        if not isinstance(sessions, Mapping):
            raise DaemonBridgeError("daemon status sessions must be an object")
        for session_id, state in sessions.items():
            self._validate_session_id(session_id)
            if not isinstance(state, Mapping) or state.get("status") not in {
                "idle", "running", "waiting_approval", "error"
            }:
                raise DaemonBridgeError("daemon session status is invalid")
        stored_sessions = self.store.list_sessions()
        self._retire_missing_sessions(set(sessions), stored_sessions)
        for session in stored_sessions:
            state = sessions.get(session["id"])
            if (
                session.get("control_state") == "owned"
                and isinstance(state, Mapping)
                and session["status"] != state["status"]
            ):
                updated = self.store.update_session(
                    session["agent_id"], session["id"], {"status": state["status"]}
                )
                if updated is not None:
                    self.service._server_event(
                        "session.upsert",
                        agent_id=session["agent_id"],
                        session_id=session["id"],
                        data=updated,
                    )
        for handler in tuple(self._status_handlers):
            handler(sessions)
        return daemon_id, sessions

    def _retire_missing_sessions(
        self, current_session_ids: set[str], stored_sessions: list[dict[str, Any]]
    ) -> None:
        reason = "小内核已重启，上一条指令的执行结果无法确认；不会自动重发。"
        missing = {
            (session["agent_id"], session["id"])
            for session in stored_sessions
            if session.get("control_state") == "owned"
            and session["id"] not in current_session_ids
        }
        for command in self.store.active_commands():
            if (command["agent_id"], command["session_id"]) in missing:
                updated = self.store.set_command_state(
                    command["agent_id"], command["session_id"], command["id"], "unknown", reason
                )
                self.service._server_event(
                    "command.upsert",
                    agent_id=command["agent_id"],
                    session_id=command["session_id"],
                    data=updated,
                )
        for task in self.store.active_tasks():
            if (task["agent_id"], task["session_id"]) in missing:
                updated_task = self.store.upsert_task({**task, "status": "unknown"})
                self.service._server_event(
                    "task.upsert",
                    agent_id=task["agent_id"],
                    session_id=task["session_id"],
                    data=updated_task,
                )
        for session in stored_sessions:
            agent_id, session_id = session["agent_id"], session["id"]
            if (agent_id, session_id) not in missing:
                continue
            if session["status"] in {"running", "waiting_approval"}:
                updated_session = self.store.update_session(
                    agent_id, session_id, {"status": "idle"}
                )
                if updated_session is not None:
                    self.service._server_event(
                        "session.upsert",
                        agent_id=agent_id,
                        session_id=session_id,
                        data=updated_session,
                    )

    async def _receive_live_frames(self, socket: Any, stopping: asyncio.Event) -> None:
        while not stopping.is_set():
            receive_task = asyncio.create_task(socket.recv())
            stop_task = asyncio.create_task(stopping.wait())
            done, pending = await asyncio.wait(
                {receive_task, stop_task}, return_when=asyncio.FIRST_COMPLETED
            )
            for task in pending:
                task.cancel()
            await asyncio.gather(*pending, return_exceptions=True)
            if stop_task in done:
                return
            raw = receive_task.result()
            self._project_live_frame(raw)

    async def _request(self, socket: Any, action: str, fields: Mapping[str, Any]) -> dict[str, Any]:
        if "action" in fields or "request_id" in fields:
            raise ValueError("daemon request fields must not override action or request_id")
        request_id = f"daemon-bridge-{uuid4().hex}"
        await socket.send(
            json.dumps(
                {"action": action, "request_id": request_id, **fields},
                ensure_ascii=False,
                separators=(",", ":"),
            )
        )
        raw = await asyncio.wait_for(socket.recv(), timeout=self.request_timeout)
        if not isinstance(raw, str):
            raise DaemonBridgeError("daemon response must be UTF-8 JSON text")
        try:
            response = json.loads(raw)
        except json.JSONDecodeError as exc:
            raise DaemonBridgeError("daemon response is invalid JSON") from exc
        if not isinstance(response, dict):
            raise DaemonBridgeError("daemon response must be an object")
        if response.get("request_id") != request_id:
            raise DaemonBridgeError("daemon response request ID does not match")
        if response.get("action") == "error":
            detail = response.get("detail")
            raise DaemonBridgeError(detail if isinstance(detail, str) else "daemon rejected request")
        if response.get("action") != f"{action}.result":
            raise DaemonBridgeError("daemon response action does not match request")
        return response

    async def _handshake(self, socket: Any) -> None:
        if self._secret is None:
            return
        response = await self._request(socket, "daemon.handshake", {"secret": self._secret})
        self._daemon_id_from(response)

    def _project_frames(
        self,
        daemon_id: str,
        session_id: str,
        checkpoint: int,
        frames: Any,
    ) -> int:
        if not isinstance(frames, list):
            raise DaemonBridgeError("daemon replay frames must be an array")
        expected_seq_id = checkpoint + 1
        for frame in frames:
            self._project_frame(daemon_id, session_id, expected_seq_id, frame)
            expected_seq_id += 1
        return len(frames)

    def _project_live_frame(self, raw: Any) -> None:
        if self._daemon_id is None:
            raise DaemonBridgeError("daemon live frame arrived before sync")
        if not isinstance(raw, str):
            raise DaemonBridgeError("daemon live frame must be UTF-8 JSON text")
        try:
            frame = json.loads(raw)
        except json.JSONDecodeError as exc:
            raise DaemonBridgeError("daemon live frame is invalid JSON") from exc
        if not isinstance(frame, Mapping):
            raise DaemonBridgeError("daemon live frame must be an object")
        session_id = frame.get("session_id")
        self._validate_session_id(session_id)
        if (self._daemon_id, session_id) in self._overflowed_sessions:
            return
        checkpoint = self.store.get_daemon_checkpoint(self._daemon_id, session_id)
        self._project_frame(self._daemon_id, session_id, checkpoint + 1, frame)

    def _project_frame(
        self,
        daemon_id: str,
        session_id: str,
        expected_seq_id: int,
        frame: Any,
    ) -> None:
        if not isinstance(frame, Mapping):
            raise DaemonBridgeError("daemon frame must be an object")
        if frame.get("session_id") != session_id:
            raise DaemonBridgeError("daemon frame session identity does not match replay stream")
        seq_id = frame.get("seq_id")
        if not isinstance(seq_id, int) or isinstance(seq_id, bool) or seq_id != expected_seq_id:
            raise DaemonBridgeError("daemon frame sequence is not contiguous")
        event = frame.get("event")
        payload = frame.get("payload")
        if not isinstance(payload, Mapping):
            raise DaemonBridgeError("daemon frame payload must be an object")
        if event == "connector.hello":
            self._project_connector_hello(payload)
        elif event == "connector.event":
            agent_id = payload.get("agent_id")
            if not isinstance(agent_id, str) or not agent_id:
                raise DaemonBridgeError("daemon connector event agent ID is invalid")
            if payload.get("session_id") not in {None, session_id}:
                raise DaemonBridgeError("daemon connector event session identity does not match")
            try:
                self.service.accept_connector_event(agent_id, dict(payload))
            except ProtocolError as exc:
                raise DaemonBridgeError(f"daemon connector event was rejected: {exc}") from exc
        else:
            self._project_native_frame(event, session_id, payload)
        self.store.set_daemon_checkpoint(daemon_id, session_id, seq_id)

    def _project_connector_hello(self, payload: Any) -> None:
        if not isinstance(payload, Mapping):
            raise DaemonBridgeError("daemon connector hello must be an object")
        agent_id = payload.get("id")
        if not isinstance(agent_id, str) or not agent_id:
            raise DaemonBridgeError("daemon connector hello agent ID is invalid")
        try:
            agent = self.store.upsert_agent(dict(payload))
            self.service.apply_connector_hello_event(agent)
        except (TypeError, ValueError, ProtocolError) as exc:
            raise DaemonBridgeError(f"daemon connector hello was rejected: {exc}") from exc

    def _project_native_frame(
        self, event: Any, session_id: str, payload: Mapping[str, Any]
    ) -> None:
        if not isinstance(event, str) or not event:
            raise DaemonBridgeError("daemon native frame event is invalid")
        with self._native_frame_handlers_lock:
            handler = self._native_frame_handlers.get(event)
        if handler is None:
            raise DaemonBridgeError("daemon native frame handler is not registered")
        try:
            handler(session_id, dict(payload))
        except DaemonBridgeError:
            raise
        except Exception as exc:
            raise DaemonBridgeError("daemon native frame handler rejected the frame") from exc

    @staticmethod
    def _daemon_id_from(response: Mapping[str, Any]) -> str:
        daemon_id = response.get("daemon_id")
        if not isinstance(daemon_id, str) or not daemon_id or len(daemon_id) > 128:
            raise DaemonBridgeError("daemon identity is invalid")
        return daemon_id

    @staticmethod
    def _validate_session_id(session_id: Any) -> None:
        if not isinstance(session_id, str) or not session_id or len(session_id) > 256:
            raise DaemonBridgeError("daemon session identity is invalid")
