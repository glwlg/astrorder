"""Daemon-owned adapter for a local Hermes TUI gateway controller."""
from __future__ import annotations

import asyncio
import threading
from collections.abc import Callable, Mapping
from typing import Any, Protocol

from .errors import DaemonProtocolError


class HermesController(Protocol):
    def connect(self) -> Mapping[str, Any]: ...

    def snapshot(self) -> Mapping[str, Any]: ...

    def create_session(
        self,
        workspace: str | None = None,
        title: str | None = None,
        *,
        provider: str | None = None,
        model: str | None = None,
        effort: str | None = None,
    ) -> Mapping[str, Any]: ...

    def branch_session(self, parent_session_id: str, title: str | None = None) -> Mapping[str, Any]: ...

    def submit_tui_command(self, command: dict[str, Any]) -> tuple[str, str | None]: ...

    def shutdown(self) -> None: ...


class HermesDaemonRuntime:
    """Own exactly one controller created inside the Session Daemon process.

    The controller's TUI-gateway child is therefore parented by the daemon.
    App Server projection remains separate and must be registered explicitly.
    """

    def __init__(
        self,
        controller_factory: Callable[[], HermesController],
        *,
        emit: Callable[..., Any] | None = None,
    ) -> None:
        if not callable(controller_factory):
            raise TypeError("controller_factory must be callable")
        self._factory = controller_factory
        self._controller: HermesController | None = None
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
            raise DaemonProtocolError("Hermes workspace is invalid")
        if title is not None and (not isinstance(title, str) or not title or len(title) > 512):
            raise DaemonProtocolError("Hermes title is invalid")
        provider, model, effort = request.get("provider"), request.get("model"), request.get("effort")
        if bool(provider) != bool(model) or any(
            value is not None and not isinstance(value, str) for value in (provider, model, effort)
        ):
            raise DaemonProtocolError("Hermes model selection is invalid")
        parent_session_id = request.get("parent_session_id")
        if parent_session_id is not None and (
            not isinstance(parent_session_id, str) or not parent_session_id
        ):
            raise DaemonProtocolError("Hermes parent session ID is invalid")
        create = controller.create_session if parent_session_id is None else controller.branch_session
        args = (workspace, title) if parent_session_id is None else (parent_session_id, title)
        kwargs = (
            {
                key: value
                for key, value in {
                    "provider": provider,
                    "model": model,
                    "effort": effort,
                }.items()
                if value is not None
            }
            if parent_session_id is None
            else {}
        )
        created = await asyncio.to_thread(create, *args, **kwargs)
        session_id = created.get("id") if isinstance(created, Mapping) else None
        if not isinstance(session_id, str) or not session_id:
            raise DaemonProtocolError("Hermes did not return a native session ID")
        status = created.get("status", "idle") if isinstance(created, Mapping) else "idle"
        if status not in {"idle", "running", "waiting_approval", "error"}:
            raise DaemonProtocolError("Hermes native session status is invalid")
        with self._lock:
            self._sessions.add(session_id)
        return {
            "session_id": session_id,
            "status": status,
            "agent_id": agent_id,
            **_identity_metadata(snapshot),
        }

    async def command(self, action: str, request: Mapping[str, Any]) -> Mapping[str, Any]:
        session_id = _session_id(request)
        with self._lock:
            if session_id not in self._sessions:
                raise DaemonProtocolError("Hermes session is not daemon-owned")
            controller = self._controller
        if controller is None:
            raise DaemonProtocolError("Hermes controller is not connected")
        from .hermes_native_control import NATIVE_SESSION_CONTROL_ACTIONS

        if action in NATIVE_SESSION_CONTROL_ACTIONS:
            from .hermes_native_control import execute_native_session_control

            return await asyncio.to_thread(
                execute_native_session_control, controller, action, request
            )
        if action == "session.rename":
            title = request.get("title")
            if not isinstance(title, str) or not title.strip() or len(title) > 512:
                raise DaemonProtocolError("Hermes title is invalid")
            rpc = getattr(controller, "_rpc", None)
            if not callable(rpc):
                raise DaemonProtocolError("Hermes native rename is unsupported")
            from ..native_session_mutation import rename_native_session

            await asyncio.to_thread(rename_native_session, rpc, session_id, title)
            return {"status": "idle", "title": title}
        if action == "session.delete":
            rpc = getattr(controller, "_rpc", None)
            if not callable(rpc):
                raise DaemonProtocolError("Hermes native delete is unsupported")
            from ..native_session_mutation import delete_native_session

            await asyncio.to_thread(delete_native_session, rpc, session_id)
            with self._lock:
                self._sessions.discard(session_id)
            return {"status": "idle", "deleted": session_id}
        if action == "session.history_page":
            before = request.get("before")
            limit = request.get("limit")
            if before is not None and (not isinstance(before, str) or len(before) > 512):
                raise DaemonProtocolError("Hermes history cursor is invalid")
            if not isinstance(limit, int) or isinstance(limit, bool) or not 1 <= limit <= 200:
                raise DaemonProtocolError("Hermes history limit is invalid")
            reader = getattr(controller, "load_native_history_page", None)
            if not callable(reader):
                raise DaemonProtocolError("Hermes native history paging is unsupported")
            page = await asyncio.to_thread(reader, session_id, before, limit)
            items = page.get("items") if isinstance(page, Mapping) else None
            next_cursor = page.get("next_cursor") if isinstance(page, Mapping) else None
            if not isinstance(items, list) or next_cursor is not None and not isinstance(next_cursor, str):
                raise DaemonProtocolError("Hermes native history page is invalid")
            return {"status": "idle", "items": items, "next_cursor": next_cursor}
        if action == "session.send":
            text = request.get("text")
            attachments = request.get("attachments")
            if not isinstance(text, str) or not text and not attachments:
                raise DaemonProtocolError("Hermes command text is invalid")
            if text.lstrip().startswith("/") and not attachments:
                result = await asyncio.to_thread(self._slash, controller, session_id, text.strip())
                if result.get("completed"):
                    return {"status": "idle", "accepted": True, **result}
                text = result.get("message")
                if not isinstance(text, str) or not text:
                    raise DaemonProtocolError("Hermes slash command did not produce an executable message")
            command = {"action": "send", "session_id": session_id, "text": text}
            if attachments is not None:
                if not isinstance(attachments, list) or not attachments:
                    raise DaemonProtocolError("Hermes inline attachments are invalid")
                command["attachments"] = attachments
            command_id = request.get("command_id")
            if command_id is not None:
                if not isinstance(command_id, str) or not command_id:
                    raise DaemonProtocolError("Hermes command_id is invalid")
                command["id"] = command_id
        elif action == "session.interrupt":
            command = {"action": "stop", "session_id": session_id}
        else:
            raise DaemonProtocolError("Hermes runtime action is unsupported")
        submit = controller.submit_tui_command
        if command.get("attachments"):
            submit = getattr(controller, "submit_tui_command_inline", None)
            if not callable(submit):
                raise DaemonProtocolError("Hermes inline attachments are unsupported")
        state, detail = await asyncio.to_thread(submit, command)
        if state != "accepted":
            raise DaemonProtocolError(detail or "Hermes did not accept the command")
        return {"status": "running", "accepted": True}

    @staticmethod
    def _slash(controller: HermesController, session_id: str, command: str) -> dict[str, Any]:
        rpc = getattr(controller, "_rpc", None)
        if not callable(rpc):
            rpc = getattr(controller, "rpc", None)
        if not callable(rpc):
            raise DaemonProtocolError("Hermes slash transport is unavailable")
        from ..native_controls import result

        resumed = result(rpc, "session.resume", {"session_id": session_id, "lazy": False})
        handle = resumed.get("session_id")
        if not isinstance(handle, str) or not handle:
            raise DaemonProtocolError("Hermes did not return a live session handle")
        parts = command.lstrip("/").split(maxsplit=1)
        dispatched = rpc(
            "command.dispatch",
            {
                "session_id": handle,
                "name": parts[0],
                "arg": parts[1] if len(parts) > 1 else "",
            },
        )
        executed = (
            dispatched["result"]
            if isinstance(dispatched, Mapping)
            and not dispatched.get("error")
            and isinstance(dispatched.get("result"), Mapping)
            else result(rpc, "slash.exec", {"session_id": handle, "command": command})
        )
        if executed.get("type") == "alias" and isinstance(executed.get("target"), str):
            executed = result(
                rpc,
                "command.dispatch",
                {"session_id": handle, "name": executed["target"].lstrip("/"), "arg": ""},
            )
        directive = executed.get("type")
        if directive in {"send", "skill"}:
            return {"message": executed.get("message")}
        if directive == "prefill":
            raise DaemonProtocolError(executed.get("notice") or "该命令需要补充参数")
        output = executed.get("output") or executed.get("display") or executed.get("notice")
        return {"completed": True, "output": str(output or "命令已执行")}

    async def shutdown(self) -> None:
        with self._lock:
            controller, self._controller = self._controller, None
            self._sessions.clear()
        if controller is not None:
            await asyncio.to_thread(controller.shutdown)

    async def disconnect(self) -> None:
        """Stop only this daemon-created Hermes controller for an explicit UI disconnect."""
        await self.shutdown()

    async def _controller_ready(self) -> tuple[HermesController, str, Mapping[str, Any]]:
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
                if self._emit is not None:
                    set_compaction = getattr(controller, 'set_compaction_callback', None)
                    if callable(set_compaction):
                        set_compaction(self._compaction_updated)
        snapshot = await asyncio.to_thread(controller.snapshot)
        state = snapshot.get("state") if isinstance(snapshot, Mapping) else None
        if created or state not in {"connecting", "connected"}:
            await asyncio.to_thread(controller.connect)
            snapshot = await asyncio.to_thread(controller.snapshot)
        state = snapshot.get("state") if isinstance(snapshot, Mapping) else None
        agent_id = snapshot.get("agent_id") if isinstance(snapshot, Mapping) else None
        if state not in {"connecting", "connected"} or not isinstance(agent_id, str) or not agent_id:
            raise DaemonProtocolError("Hermes controller did not confirm a native connection")
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

    def _compaction_updated(self, agent_id: str, session_id: str, update: dict) -> None:
        if self._loop is not None and not self._loop.is_closed() and self._emit is not None:
            asyncio.run_coroutine_threadsafe(
                self._emit(session_id, 'hermes.compaction', {
                    'agent_id': agent_id, 'session_id': session_id, **update,
                }), self._loop,
            )


def _session_id(request: Mapping[str, Any]) -> str:
    session_id = request.get("session_id")
    if not isinstance(session_id, str) or not session_id or len(session_id) > 256:
        raise DaemonProtocolError("Hermes session_id is invalid")
    return session_id


def _identity_metadata(snapshot: Mapping[str, Any]) -> dict[str, str]:
    return {
        key: value
        for key in ("source_id", "profile_name", "runtime_id", "connection_id")
        if isinstance(value := snapshot.get(key), str) and value
    }
