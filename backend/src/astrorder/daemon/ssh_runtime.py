"""Daemon-owned adapter for an SSH Hermes bridge runtime."""
from __future__ import annotations

import asyncio
import threading
from collections.abc import Callable, Mapping
from typing import Any, Protocol

from .errors import DaemonProtocolError


class SshController(Protocol):
    @property
    def agent_id(self) -> str: ...

    def start(self) -> None: ...

    def wait_gateway(self, timeout: float = 20) -> bool: ...

    def snapshot(self) -> Mapping[str, Any]: ...

    def create_session(self, workspace: str | None = None, title: str | None = None) -> Mapping[str, Any]: ...

    def submit(self, command: dict[str, Any]) -> tuple[str, str | None]: ...

    def stop(self) -> None: ...


class SshDaemonRuntime:
    """Own one remote SSH bridge inside the daemon process.

    This adapter intentionally only forwards native SSH bridge actions. It does
    not instantiate a local PTY for a remote session.
    """

    def __init__(
        self,
        controller_factory: Callable[[], SshController],
        *,
        emit: Callable[..., Any] | None = None,
    ) -> None:
        if not callable(controller_factory):
            raise TypeError("controller_factory must be callable")
        self._factory = controller_factory
        self._controller: SshController | None = None
        self._sessions: set[str] = set()
        self._lock = threading.RLock()
        self._emit = emit
        self._loop: asyncio.AbstractEventLoop | None = None

    def set_emitter(self, emit: Callable[..., Any]) -> None:
        if not callable(emit):
            raise TypeError("emit must be callable")
        self._emit = emit

    async def spawn(self, request: Mapping[str, Any]) -> Mapping[str, Any]:
        session_id = _session_id(request)
        controller, agent_id, snapshot = await self._controller_ready()
        del controller
        with self._lock:
            self._sessions.add(session_id)
        return {"status": "idle", "agent_id": agent_id, **_identity_metadata(snapshot)}

    async def create(self, request: Mapping[str, Any]) -> Mapping[str, Any]:
        controller, agent_id, snapshot = await self._controller_ready()
        workspace = request.get("cwd")
        title = request.get("title")
        if workspace is not None and (not isinstance(workspace, str) or not workspace):
            raise DaemonProtocolError("SSH workspace is invalid")
        if title is not None and (not isinstance(title, str) or not title or len(title) > 512):
            raise DaemonProtocolError("SSH title is invalid")
        created = await asyncio.to_thread(controller.create_session, workspace, title)
        session_id = created.get("id") if isinstance(created, Mapping) else None
        if not isinstance(session_id, str) or not session_id:
            raise DaemonProtocolError("SSH runtime did not return a native session ID")
        status = created.get("status", "idle") if isinstance(created, Mapping) else "idle"
        if status not in {"idle", "running", "waiting_approval", "error"}:
            raise DaemonProtocolError("SSH native session status is invalid")
        with self._lock:
            self._sessions.add(session_id)
        return {
            "session_id": session_id,
            "status": status,
            "agent_id": agent_id,
            **_identity_metadata(snapshot),
        }

    async def query(self, request: Mapping[str, Any]) -> Mapping[str, Any]:
        method = request.get("method")
        if method not in {"session.active_list", "approval.pending", "approval.respond"}:
            raise DaemonProtocolError("SSH runtime request is unsupported")
        params = request.get("request_params", {})
        if not isinstance(params, Mapping):
            raise DaemonProtocolError("SSH runtime request params must be an object")
        controller, _agent_id, _snapshot = await self._controller_ready()
        rpc = getattr(controller, "rpc", None)
        if not callable(rpc):
            raise DaemonProtocolError("SSH child does not support runtime requests")
        result = await asyncio.to_thread(rpc, method, dict(params))
        if not isinstance(result, Mapping):
            raise DaemonProtocolError("SSH child runtime request result is invalid")
        return dict(result)

    async def command(self, action: str, request: Mapping[str, Any]) -> Mapping[str, Any]:
        session_id = _session_id(request)
        with self._lock:
            if session_id not in self._sessions:
                raise DaemonProtocolError("SSH session is not daemon-owned")
            controller = self._controller
        if controller is None:
            raise DaemonProtocolError("SSH runtime is not connected")
        from .hermes_native_control import NATIVE_SESSION_CONTROL_ACTIONS

        if action in NATIVE_SESSION_CONTROL_ACTIONS:
            from .hermes_native_control import execute_native_session_control

            return await asyncio.to_thread(
                execute_native_session_control, controller, action, request
            )
        if action == "session.rename":
            title = request.get("title")
            if not isinstance(title, str) or not title.strip() or len(title) > 512:
                raise DaemonProtocolError("SSH title is invalid")
            rpc = getattr(controller, "rpc", None)
            if not callable(rpc):
                raise DaemonProtocolError("SSH native rename is unsupported")
            from ..native_session_mutation import rename_native_session

            await asyncio.to_thread(rename_native_session, rpc, session_id, title)
            return {"status": "idle", "title": title}
        if action == "session.delete":
            rpc = getattr(controller, "rpc", None)
            if not callable(rpc):
                raise DaemonProtocolError("SSH native delete is unsupported")
            from ..native_session_mutation import delete_native_session

            await asyncio.to_thread(delete_native_session, rpc, session_id)
            with self._lock:
                self._sessions.discard(session_id)
            return {"status": "idle", "deleted": session_id}
        if action == "session.history_page":
            before = request.get("before")
            limit = request.get("limit")
            if before is not None and (not isinstance(before, str) or len(before) > 512):
                raise DaemonProtocolError("SSH history cursor is invalid")
            if not isinstance(limit, int) or isinstance(limit, bool) or not 1 <= limit <= 200:
                raise DaemonProtocolError("SSH history limit is invalid")
            reader = getattr(controller, "load_native_history_page", None)
            if not callable(reader):
                raise DaemonProtocolError("SSH native history paging is unsupported")
            page = await asyncio.to_thread(reader, session_id, before, limit)
            items = page.get("items") if isinstance(page, Mapping) else None
            next_cursor = page.get("next_cursor") if isinstance(page, Mapping) else None
            if not isinstance(items, list) or next_cursor is not None and not isinstance(next_cursor, str):
                raise DaemonProtocolError("SSH native history page is invalid")
            return {"status": "idle", "items": items, "next_cursor": next_cursor}
        if action == "session.send":
            text = request.get("text")
            attachments = request.get("attachments")
            if not isinstance(text, str) or not text and not attachments:
                raise DaemonProtocolError("SSH command text is invalid")
            command = {"action": "send", "session_id": session_id, "text": text}
            if attachments is not None:
                if not isinstance(attachments, list) or not attachments:
                    raise DaemonProtocolError("SSH inline attachments are invalid")
                command["attachments"] = attachments
            command_id = request.get("command_id")
            if command_id is not None:
                if not isinstance(command_id, str) or not command_id:
                    raise DaemonProtocolError("SSH command_id is invalid")
                command["id"] = command_id
        elif action == "session.interrupt":
            command = {"action": "stop", "session_id": session_id}
        else:
            raise DaemonProtocolError("SSH runtime action is unsupported")
        submit = controller.submit
        if command.get("attachments"):
            submit = getattr(controller, "submit_inline", None)
            if not callable(submit):
                raise DaemonProtocolError("SSH inline attachments are unsupported")
        state, detail = await asyncio.to_thread(submit, command)
        if state != "accepted":
            raise DaemonProtocolError(detail or "SSH runtime did not accept the command")
        return {"status": "running", "accepted": True}

    async def shutdown(self) -> None:
        with self._lock:
            controller, self._controller = self._controller, None
            self._sessions.clear()
        if controller is not None:
            await asyncio.to_thread(controller.stop)

    async def disconnect(self) -> None:
        """Stop only this daemon-created SSH bridge for an explicit UI disconnect."""
        await self.shutdown()

    async def _controller_ready(self) -> tuple[SshController, str, Mapping[str, Any]]:
        self._loop = asyncio.get_running_loop()
        with self._lock:
            controller = self._controller
            created = controller is None
            if controller is None:
                controller = self._factory()
                self._controller = controller
                set_completion_callback = getattr(controller, "set_command_completion_callback", None)
                if self._emit is not None and callable(set_completion_callback):
                    set_completion_callback(self._command_completed)
        snapshot = await asyncio.to_thread(controller.snapshot)
        started = created or not isinstance(snapshot, Mapping) or not snapshot.get("alive")
        if started:
            await asyncio.to_thread(controller.start)
            snapshot = await asyncio.to_thread(controller.snapshot)
            if not await asyncio.to_thread(controller.wait_gateway, 20):
                raise DaemonProtocolError("SSH Hermes gateway did not become ready")
        agent_id = snapshot.get("agent_id") if isinstance(snapshot, Mapping) else None
        if not snapshot.get("alive") or not isinstance(agent_id, str) or not agent_id:
            raise DaemonProtocolError("SSH runtime did not confirm a native bridge")
        return controller, agent_id, snapshot

    def _command_completed(self, agent_id: str, session_id: str, command_id: str) -> None:
        loop = self._loop
        emit = self._emit
        if loop is None or loop.is_closed() or emit is None:
            return
        asyncio.run_coroutine_threadsafe(
            emit(
                session_id,
                "hermes.command_complete",
                {
                    "agent_id": agent_id,
                    "session_id": session_id,
                    "command_id": command_id,
                },
                status="idle",
            ),
            loop,
        )


def _session_id(request: Mapping[str, Any]) -> str:
    session_id = request.get("session_id")
    if not isinstance(session_id, str) or not session_id or len(session_id) > 256:
        raise DaemonProtocolError("SSH session_id is invalid")
    return session_id


def _identity_metadata(snapshot: Mapping[str, Any]) -> dict[str, str]:
    return {
        key: value
        for key in ("source_id", "runtime_id", "connection_id")
        if isinstance(value := snapshot.get(key), str) and value
    }


class SshDaemonRuntimeRegistry:
    """Multiplex explicit SSH connection identities into daemon-owned children.

    One daemon registry may own several SSH bridges, but every native session
    remains bound to the exact connection config that spawned it.
    """

    def __init__(self, factory: Callable[[str, Mapping[str, Any]], Any]) -> None:
        if not callable(factory):
            raise TypeError("factory must be callable")
        self._factory = factory
        self._children: dict[str, tuple[str, Any]] = {}
        self._sessions: dict[str, str] = {}
        self._lock = asyncio.Lock()
        self._emit: Callable[..., Any] | None = None

    def set_emitter(self, emit: Callable[..., Any]) -> None:
        if not callable(emit):
            raise TypeError("emit must be callable")
        self._emit = emit
        for _config, runtime in self._children.values():
            set_emitter = getattr(runtime, "set_emitter", None)
            if callable(set_emitter):
                set_emitter(emit)

    async def spawn(self, request: Mapping[str, Any]) -> Mapping[str, Any]:
        session_id = _session_id(request)
        connection_id, runtime = await self._runtime_for_request(request)
        result = await runtime.spawn(request)
        if not isinstance(result, Mapping):
            raise DaemonProtocolError("SSH child spawn result is invalid")
        async with self._lock:
            self._sessions[session_id] = connection_id
        return dict(result)

    async def query(self, request: Mapping[str, Any]) -> Mapping[str, Any]:
        _connection_id, runtime = await self._runtime_for_request(request)
        query = getattr(runtime, "query", None)
        if not callable(query):
            raise DaemonProtocolError("SSH child does not support runtime requests")
        result = await query(
            {"method": request.get("method"), "request_params": request.get("request_params", {})}
        )
        if not isinstance(result, Mapping):
            raise DaemonProtocolError("SSH child runtime request result is invalid")
        return dict(result)

    async def create(self, request: Mapping[str, Any]) -> Mapping[str, Any]:
        connection_id, runtime = await self._runtime_for_request(request)
        result = await runtime.create(request)
        if not isinstance(result, Mapping):
            raise DaemonProtocolError("SSH child create result is invalid")
        session_id = result.get("session_id")
        if not isinstance(session_id, str) or not session_id:
            raise DaemonProtocolError("SSH child did not return a native session ID")
        async with self._lock:
            self._sessions[session_id] = connection_id
        return dict(result)

    async def command(self, action: str, request: Mapping[str, Any]) -> Mapping[str, Any]:
        session_id = _session_id(request)
        async with self._lock:
            connection_id = self._sessions.get(session_id)
            child = self._children.get(connection_id) if connection_id else None
        if child is None:
            raise DaemonProtocolError("SSH session is not daemon-owned")
        _config, runtime = child
        return await runtime.command(action, request)

    async def disconnect_session(self, session_id: str) -> tuple[str, ...]:
        async with self._lock:
            connection_id = self._sessions.get(session_id)
            child = self._children.get(connection_id) if connection_id else None
            if child is None:
                raise DaemonProtocolError("SSH session is not daemon-owned")
            released = tuple(
                owned_session_id
                for owned_session_id, owned_connection_id in self._sessions.items()
                if owned_connection_id == connection_id
            )
            for released_session_id in released:
                self._sessions.pop(released_session_id, None)
            self._children.pop(connection_id, None)
        _config, runtime = child
        await runtime.disconnect()
        return released

    async def shutdown(self) -> None:
        async with self._lock:
            children = [runtime for _config, runtime in self._children.values()]
            self._children.clear()
            self._sessions.clear()
        await asyncio.gather(*(runtime.shutdown() for runtime in children))

    async def _runtime_for_request(self, request: Mapping[str, Any]) -> tuple[str, Any]:
        connection_id, settings, canonical = _ssh_connection_config(request)
        async with self._lock:
            child = self._children.get(connection_id)
            if child is not None:
                known_config, runtime = child
                if known_config != canonical:
                    raise DaemonProtocolError("SSH connection settings changed while daemon-owned")
                return connection_id, runtime
            runtime = self._factory(connection_id, settings)
            set_emitter = getattr(runtime, "set_emitter", None)
            if self._emit is not None and callable(set_emitter):
                set_emitter(self._emit)
            if not callable(getattr(runtime, "spawn", None)) or not callable(
                getattr(runtime, "command", None)
            ):
                raise DaemonProtocolError("SSH factory returned an invalid runtime")
            self._children[connection_id] = (canonical, runtime)
            return connection_id, runtime


def _ssh_connection_config(request: Mapping[str, Any]) -> tuple[str, dict[str, Any], str]:
    params = request.get("params")
    if not isinstance(params, Mapping):
        raise DaemonProtocolError("SSH runtime params are invalid")
    connection_id = params.get("connection_id")
    raw_settings = params.get("ssh_settings")
    if not isinstance(connection_id, str) or not connection_id or len(connection_id) > 128:
        raise DaemonProtocolError("SSH connection_id is invalid")
    if not isinstance(raw_settings, Mapping) or not raw_settings:
        raise DaemonProtocolError("SSH settings are invalid")
    settings = dict(raw_settings)
    try:
        import json

        canonical = json.dumps(settings, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
    except (TypeError, ValueError) as exc:
        raise DaemonProtocolError("SSH settings are not JSON-safe") from exc
    return connection_id, settings, canonical
