"""Daemon-owned Codex app-server runtime adapter.

This module deliberately has no dependency on the App Server's Store or
ControlService.  It owns only Codex transports it creates and emits native
notifications into the Session Daemon WAL for a later App Server projection.
"""
from __future__ import annotations

import asyncio
import json
import logging
import os
import re
import threading
import time
from collections.abc import Awaitable, Callable, Mapping
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Protocol

from astrorder_codex_connector.app_server import CodexAppServer, CodexRpcRejected
from astrorder_codex_connector.config import CodexConnectorConfig

from ..native_controls import REASONING_EFFORTS
from .codex_desktop import (
    codex_desktop_status,
    send_codex_desktop_message,
    set_codex_desktop_settings,
)
from .errors import DaemonProtocolError

logger = logging.getLogger(__name__)


class CodexAppServerClient(Protocol):
    def start(self) -> None: ...

    def stop(self) -> None: ...

    def send(self, message: dict[str, Any]) -> None: ...

    def request(self, method: str, params: dict[str, Any], timeout: float = 30) -> dict[str, Any]: ...


FrameEmitter = Callable[..., Awaitable[dict[str, Any]]]
CodexClientFactory = Callable[..., CodexAppServerClient]
_APPROVAL_METHODS = frozenset(
    {"item/commandExecution/requestApproval", "item/fileChange/requestApproval"}
)
_TURN_OPTION_KEYS = frozenset({"approvalPolicy", "sandboxPolicy", "approvalsReviewer", "model"})
_QUERY_METHODS = frozenset({
    "account/read", "hooks/list", "model/list", "skills/list", "thread/items/list", "thread/list", "thread/loaded/list", "thread/read"
})


async def forward_codex_desktop_stops(
    config: CodexDaemonRuntimeConfig,
    emit: FrameEmitter,
    stopping: asyncio.Event,
    *,
    poll_interval: float = 0.5,
) -> None:
    configured_home = (config.environment or {}).get("CODEX_HOME")
    home = Path(configured_home or os.environ.get("CODEX_HOME") or Path.home() / ".codex")
    directory = home / "astrorder-observer" / "events"

    def files_after(cursor: tuple[int, str]) -> tuple[list[Path], tuple[int, str]]:
        files = sorted(
            (
                (path.stat().st_mtime_ns, path.name, path)
                for path in directory.glob("*.json")
                if not path.is_symlink() and path.stat().st_size <= 8192
            ),
            key=lambda item: (item[0], item[1]),
        )
        fresh = [path for modified, name, path in files if (modified, name) > cursor]
        latest = (files[-1][0], files[-1][1]) if files else cursor
        return fresh, latest

    try:
        _, cursor = await asyncio.to_thread(files_after, (0, ""))
    except OSError:
        cursor = (0, "")
    while not stopping.is_set():
        try:
            paths, cursor = await asyncio.to_thread(files_after, cursor)
            for path in paths:
                try:
                    row = json.loads(await asyncio.to_thread(path.read_text, encoding="utf-8"))
                except (OSError, ValueError):
                    continue
                event_id = row.get("id") if isinstance(row, Mapping) else None
                session_id = row.get("session_id") if isinstance(row, Mapping) else None
                observed_at = row.get("observed_at") if isinstance(row, Mapping) else None
                if (
                    row.get("event") != "Stop"
                    or not isinstance(event_id, str)
                    or path.stem != event_id
                    or re.fullmatch(r"[a-f0-9]{32}(?:[a-f0-9]{32})?", event_id) is None
                    or not isinstance(session_id, str)
                    or not session_id
                    or len(session_id) > 256
                    or not isinstance(observed_at, (int, float))
                    or abs(time.time() - observed_at) > 300
                ):
                    continue
                turn_id = row.get("turn_id")
                if not isinstance(turn_id, str) or not turn_id:
                    turn_id = event_id
                await emit(
                    session_id,
                    "codex.notification",
                    {
                        "agent_id": config.agent_id,
                        "frame": {
                            "method": "turn/completed",
                            "params": {
                                "threadId": session_id,
                                "turn": {"id": turn_id, "status": "completed"},
                            },
                        },
                    },
                    timestamp=observed_at,
                    status="idle",
                )
        except OSError:
            pass
        try:
            await asyncio.wait_for(stopping.wait(), timeout=poll_interval)
        except TimeoutError:
            pass


@dataclass(frozen=True)
class CodexDaemonRuntimeConfig:
    """Trusted daemon-local launch configuration for an owned Codex child."""

    executable: str
    workspace: Path
    allowed_workspaces: tuple[Path, ...]
    agent_id: str
    agent_name: str
    environment: Mapping[str, str] | None = None

    def __post_init__(self) -> None:
        if not isinstance(self.executable, str) or not self.executable or "\x00" in self.executable:
            raise ValueError("Codex executable must be a non-empty path")
        if not isinstance(self.agent_id, str) or not self.agent_id:
            raise ValueError("Codex agent_id must be a non-empty string")
        if not isinstance(self.agent_name, str) or not self.agent_name:
            raise ValueError("Codex agent_name must be a non-empty string")
        workspace = Path(self.workspace).expanduser().resolve()
        roots = tuple(Path(root).expanduser().resolve() for root in self.allowed_workspaces)
        if not roots or not workspace.is_dir() or not any(_inside(workspace, root) for root in roots):
            raise ValueError("Codex workspace must be inside an allowed workspace")
        environment = dict(self.environment) if self.environment is not None else None
        if environment is not None and any(
            not isinstance(key, str) or not key or "=" in key or "\x00" in key
            or not isinstance(value, str) or "\x00" in value
            for key, value in environment.items()
        ):
            raise ValueError("Codex environment is invalid")
        object.__setattr__(self, "workspace", workspace)
        object.__setattr__(self, "allowed_workspaces", roots)
        object.__setattr__(self, "environment", environment)


@dataclass
class _OwnedCodexSession:
    client: CodexAppServerClient
    loop: asyncio.AbstractEventLoop
    ephemeral: bool = False
    status: str = "idle"
    active_turn_id: str | None = None
    completed_turn_statuses: dict[str, str] = field(default_factory=dict)
    pending_approvals: dict[str, str | int] = field(default_factory=dict)


class CodexDaemonRuntime:
    """Own Codex app-server children per explicitly spawned native thread."""

    def __init__(
        self,
        config: CodexDaemonRuntimeConfig,
        *,
        emit: FrameEmitter,
        client_factory: CodexClientFactory = CodexAppServer,
        desktop_submit=send_codex_desktop_message,
    ) -> None:
        self.config = config
        self.emit = emit
        self.client_factory = client_factory
        self.desktop_submit = desktop_submit
        self._sessions: dict[str, _OwnedCodexSession] = {}
        self._lock = threading.RLock()
        self._catalog_client: CodexAppServerClient | None = None
        self._catalog_initialized: dict[str, Any] | None = None

    def set_emitter(self, emit: FrameEmitter) -> None:
        if not callable(emit):
            raise TypeError("emit must be callable")
        self.emit = emit

    def status(self) -> Mapping[str, Any]:
        return {"desktop_cdp": codex_desktop_status()}

    async def query(self, request: Mapping[str, Any]) -> Mapping[str, Any]:
        method = request.get("method")
        params = request.get("request_params", {})
        if method == "desktop/submit":
            if not isinstance(params, Mapping):
                raise DaemonProtocolError("Codex Desktop request params must be an object")
            return await self.desktop_submit(
                params.get("threadId"), params.get("input", params.get("text"))
            )
        if method == "desktop/settings":
            if not isinstance(params, Mapping):
                raise DaemonProtocolError("Codex Desktop request params must be an object")
            updates = params.get("updates")
            if not isinstance(updates, Mapping):
                raise DaemonProtocolError("Codex Desktop settings must be an object")
            return await set_codex_desktop_settings(params.get("threadId"), dict(updates))
        if method != "initialize" and method not in _QUERY_METHODS:
            raise DaemonProtocolError(f"Codex runtime request is not read-only: {method}")
        if not isinstance(params, Mapping):
            raise DaemonProtocolError("Codex runtime request params must be an object")
        return await asyncio.to_thread(self._query, method, dict(params))

    def _query(
        self, method: str, params: dict[str, Any], *, retry_closed: bool = True
    ) -> Mapping[str, Any]:
        with self._lock:
            client = self._catalog_client
            if client is None:
                client = self.client_factory(
                    CodexConnectorConfig(
                        endpoint="",
                        secret="",
                        agent_id=self.config.agent_id,
                        agent_name=self.config.agent_name,
                        executable=self.config.executable,
                        workspace=self.config.workspace,
                        allowed_workspaces=self.config.allowed_workspaces,
                        thread_id=None,
                    ),
                    lambda _frame: None,
                    environment=self.config.environment,
                )
                client.start()
                initialized = client.request(
                    "initialize",
                    {
                        "clientInfo": {
                            "name": "astrorder-daemon",
                            "title": "Astrorder Session Daemon",
                            "version": "0.1.0",
                        },
                        "capabilities": {"experimentalApi": True},
                    },
                )
                client.send({"method": "initialized", "params": {}})
                self._catalog_client = client
                self._catalog_initialized = dict(initialized)
            if method == "initialize":
                return dict(self._catalog_initialized or {})
            try:
                return client.request(method, params)
            except CodexRpcRejected as exc:
                raise DaemonProtocolError(str(exc)) from None
            except RuntimeError as exc:
                if not retry_closed or not str(exc).startswith(
                    "Codex transport closed before response"
                ):
                    raise
                client.stop()
                self._catalog_client = None
                self._catalog_initialized = None
                return self._query(method, params, retry_closed=False)

    async def spawn(self, request: Mapping[str, Any]) -> Mapping[str, Any]:
        session_id = _session_id(request)
        workspace = self._workspace(request.get("cwd"))
        loop = asyncio.get_running_loop()
        with self._lock:
            if session_id in self._sessions:
                raise DaemonProtocolError("Codex session is already daemon-owned")

        def on_notification(frame: dict[str, Any]) -> None:
            self._on_notification(session_id, loop, frame)

        def on_close() -> None:
            self._schedule_emit(
                loop,
                session_id,
                "codex.transport_closed",
                {
                    "agent_id": self.config.agent_id,
                    "reason": "native transport closed",
                },
                "error",
            )

        client = self.client_factory(
            CodexConnectorConfig(
                endpoint="",
                secret="",
                agent_id=self.config.agent_id,
                agent_name=self.config.agent_name,
                executable=self.config.executable,
                workspace=workspace,
                allowed_workspaces=self.config.allowed_workspaces,
                thread_id=session_id,
            ),
            on_notification,
            on_close=on_close,
            environment=self.config.environment,
        )
        owned = _OwnedCodexSession(client=client, loop=loop)
        with self._lock:
            self._sessions[session_id] = owned
        try:
            await asyncio.to_thread(client.start)
            await asyncio.to_thread(
                client.request,
                "initialize",
                {
                    "clientInfo": {
                        "name": "astrorder-daemon",
                        "title": "Astrorder Session Daemon",
                        "version": "0.1.0",
                    },
                    "capabilities": {"experimentalApi": True},
                },
            )
            await asyncio.to_thread(client.send, {"method": "initialized", "params": {}})
            resumed = await self._resume(client, session_id)
            thread = resumed.get("thread") if isinstance(resumed, Mapping) else None
            if not isinstance(thread, Mapping) or thread.get("id") != session_id:
                raise DaemonProtocolError("Codex did not confirm the requested native thread")
            model = resumed.get("model")
            provider = resumed.get("modelProvider")
            result: dict[str, Any] = {"status": "idle"}
            if isinstance(model, str) and model:
                result["model"] = model
            if isinstance(provider, str) and provider:
                result["provider"] = provider
            return result
        except Exception as exc:
            with self._lock:
                self._sessions.pop(session_id, None)
            try:
                await asyncio.to_thread(client.stop)
            except Exception:
                logger.exception("failed to stop Codex client after spawn rejection")
            if "active writer" in str(exc).casefold():
                raise DaemonProtocolError("Codex thread already has an active writer") from None
            raise

    async def create(self, request: Mapping[str, Any]) -> Mapping[str, Any]:
        """Create a thread through a daemon-owned transport and retain it.

        Codex assigns the thread ID.  The App Server therefore never invents a
        session identity or starts a short-lived creator process on its behalf.
        """
        workspace = self._workspace(request.get("cwd"))
        ephemeral = request.get("ephemeral", False)
        if not isinstance(ephemeral, bool):
            raise DaemonProtocolError("Codex ephemeral must be boolean")
        parent_session_id = request.get("parent_session_id")
        if parent_session_id is not None and (
            not isinstance(parent_session_id, str) or not parent_session_id
        ):
            raise DaemonProtocolError("Codex parent_session_id is invalid")
        loop = asyncio.get_running_loop()
        pending_notifications: list[dict[str, Any]] = []
        session_ref: dict[str, str] = {}

        def on_notification(frame: dict[str, Any]) -> None:
            session_id = session_ref.get("session_id")
            if session_id is None:
                if isinstance(frame, Mapping):
                    pending_notifications.append(dict(frame))
                return
            self._on_notification(session_id, loop, frame)

        def on_close() -> None:
            session_id = session_ref.get("session_id")
            if session_id is not None:
                self._schedule_emit(
                    loop,
                    session_id,
                    "codex.transport_closed",
                    {
                        "agent_id": self.config.agent_id,
                        "reason": "native transport closed",
                    },
                    "error",
                )

        client = self.client_factory(
            CodexConnectorConfig(
                endpoint="",
                secret="",
                agent_id=self.config.agent_id,
                agent_name=self.config.agent_name,
                executable=self.config.executable,
                workspace=workspace,
                allowed_workspaces=self.config.allowed_workspaces,
                thread_id=None,
            ),
            on_notification,
            on_close=on_close,
            environment=self.config.environment,
        )
        owned = _OwnedCodexSession(client=client, loop=loop, ephemeral=ephemeral)
        session_id: str | None = None
        try:
            await asyncio.to_thread(client.start)
            await asyncio.to_thread(
                client.request,
                "initialize",
                {
                    "clientInfo": {
                        "name": "astrorder-daemon",
                        "title": "Astrorder Session Daemon",
                        "version": "0.1.0",
                    },
                    "capabilities": {"experimentalApi": True},
                },
            )
            await asyncio.to_thread(client.send, {"method": "initialized", "params": {}})
            if parent_session_id is None:
                created = await asyncio.to_thread(
                    client.request,
                    "thread/start",
                    {
                        "cwd": str(workspace),
                        "ephemeral": ephemeral,
                        "persistExtendedHistory": not ephemeral,
                    },
                )
            else:
                created = await asyncio.to_thread(
                    client.request,
                    "thread/fork",
                    {
                        "threadId": parent_session_id,
                        "cwd": str(workspace),
                        "ephemeral": ephemeral,
                        "excludeTurns": True,
                        **({"deferGoalContinuation": True} if not ephemeral else {}),
                    },
                )
            thread = created.get("thread") if isinstance(created, Mapping) else None
            candidate = thread.get("id") if isinstance(thread, Mapping) else None
            if not isinstance(candidate, str) or not candidate:
                raise DaemonProtocolError("Codex did not return a native thread ID")
            session_id = candidate
            session_ref["session_id"] = session_id
            with self._lock:
                if session_id in self._sessions:
                    raise DaemonProtocolError("Codex returned an already-owned native thread")
                self._sessions[session_id] = owned
            title = request.get("title")
            if title is not None and not ephemeral:
                if (
                    not isinstance(title, str)
                    or not title.strip()
                    or len(title) > 512
                    or any(char in title for char in "\x00\r\n")
                ):
                    raise DaemonProtocolError("Codex title is invalid")
                await asyncio.to_thread(
                    client.request,
                    "thread/name/set",
                    {"threadId": session_id, "name": title},
                )
                verified = await asyncio.to_thread(
                    client.request,
                    "thread/read",
                    {"threadId": session_id, "includeTurns": False},
                )
                native_thread = verified.get("thread") if isinstance(verified, Mapping) else None
                if (
                    not isinstance(native_thread, Mapping)
                    or native_thread.get("id") != session_id
                    or native_thread.get("name") != title
                ):
                    raise DaemonProtocolError("Codex title was not confirmed by native state")
            for frame in pending_notifications:
                self._on_notification(session_id, loop, frame)
            result: dict[str, Any] = {"session_id": session_id, "status": "idle"}
            model = created.get("model") if isinstance(created, Mapping) else None
            provider = created.get("modelProvider") if isinstance(created, Mapping) else None
            if isinstance(model, str) and model:
                result["model"] = model
            if isinstance(provider, str) and provider:
                result["provider"] = provider
            return result
        except Exception:
            if session_id is not None:
                with self._lock:
                    self._sessions.pop(session_id, None)
            await asyncio.to_thread(client.stop)
            raise

    async def command(self, action: str, request: Mapping[str, Any]) -> Mapping[str, Any]:
        session_id = _session_id(request)
        owned = self._owned(session_id)
        if action == "session.send":
            return await self._send(session_id, owned, request)
        if action == "session.compact":
            await asyncio.to_thread(
                owned.client.request, "thread/compact/start", {"threadId": session_id}
            )
            return {"status": "idle", "completed": True}
        if action == "session.review":
            instructions = request.get("instructions")
            if instructions is not None and not isinstance(instructions, str):
                raise DaemonProtocolError("Codex review instructions are invalid")
            target = (
                {"type": "custom", "instructions": instructions.strip()}
                if isinstance(instructions, str) and instructions.strip()
                else {"type": "uncommittedChanges"}
            )
            response = await asyncio.to_thread(
                owned.client.request,
                "review/start",
                {"threadId": session_id, "target": target, "delivery": "inline"},
            )
            turn = response.get("turn") if isinstance(response, Mapping) else None
            turn_id = turn.get("id") if isinstance(turn, Mapping) else None
            if not isinstance(turn_id, str) or not turn_id:
                raise DaemonProtocolError("Codex did not confirm a review turn ID")
            with self._lock:
                owned.active_turn_id = turn_id
                owned.status = "running"
            return {"status": "running", "turn_id": turn_id}
        if action == "session.steer":
            return await self._steer(session_id, owned, request)
        if action == "session.interrupt":
            return await self._interrupt(session_id, owned, request)
        if action == "session.approve":
            return await self._approve(session_id, owned, request)
        if action == "session.settings":
            return await self._settings(session_id, owned, request)
        if action == "session.delete":
            return await self._delete(session_id, owned)
        if action == "session.rename":
            return await self._rename(session_id, owned, request)
        raise DaemonProtocolError("Codex runtime action is unsupported")

    async def shutdown(self) -> None:
        """Stop only the app-server clients created by this runtime adapter."""
        with self._lock:
            clients = [owned.client for owned in self._sessions.values()]
            self._sessions.clear()
            if self._catalog_client is not None:
                clients.append(self._catalog_client)
                self._catalog_client = None
                self._catalog_initialized = None
        await asyncio.gather(*(asyncio.to_thread(client.stop) for client in clients))

    async def _send(
        self,
        session_id: str,
        owned: _OwnedCodexSession,
        request: Mapping[str, Any],
    ) -> dict[str, Any]:
        inputs = request.get("input")
        if inputs is None:
            # Kept solely for an older trusted bridge client.  New App-side
            # controllers stage attachments before IPC and always send input.
            prompt = request.get("prompt")
            if not isinstance(prompt, str) or not prompt:
                raise DaemonProtocolError("Codex prompt must be a non-empty string")
            inputs = [{"type": "text", "text": prompt}]
        _validate_input(inputs)
        options = request.get("params", {})
        if not isinstance(options, Mapping):
            raise DaemonProtocolError("Codex turn params must be an object")
        turn_params: dict[str, Any] = {
            "threadId": session_id,
            "input": [dict(item) for item in inputs],
        }
        for key in _TURN_OPTION_KEYS:
            if key in options:
                turn_params[key] = options[key]
        _require_json_safe(turn_params, "Codex turn params")
        response = await asyncio.to_thread(owned.client.request, "turn/start", turn_params)
        turn = response.get("turn") if isinstance(response, Mapping) else None
        turn_id = turn.get("id") if isinstance(turn, Mapping) else None
        if not isinstance(turn_id, str) or not turn_id:
            raise DaemonProtocolError("Codex did not confirm a native turn ID")
        with self._lock:
            terminal = owned.completed_turn_statuses.get(turn_id)
            if terminal is None:
                owned.active_turn_id = turn_id
                owned.status = "running"
            else:
                owned.active_turn_id = None
                owned.status = terminal
            status = owned.status
        return {"status": status, "turn_id": turn_id, "accepted": True}

    async def _steer(
        self,
        session_id: str,
        owned: _OwnedCodexSession,
        request: Mapping[str, Any],
    ) -> dict[str, Any]:
        turn_id = request.get("turn_id")
        inputs = request.get("input")
        if not isinstance(turn_id, str) or not turn_id:
            raise DaemonProtocolError("Codex steer requires a native turn_id")
        _validate_input(inputs)
        with self._lock:
            if owned.active_turn_id != turn_id:
                raise DaemonProtocolError("Codex turn is not the active daemon-owned turn")
        await asyncio.to_thread(
            owned.client.request,
            "turn/steer",
            {"threadId": session_id, "expectedTurnId": turn_id, "input": [dict(item) for item in inputs]},
        )
        return {"status": "running", "turn_id": turn_id, "accepted": True}

    async def _interrupt(
        self,
        session_id: str,
        owned: _OwnedCodexSession,
        request: Mapping[str, Any],
    ) -> dict[str, Any]:
        turn_id = request.get("turn_id")
        if not isinstance(turn_id, str) or not turn_id:
            raise DaemonProtocolError("Codex interrupt requires a native turn_id")
        with self._lock:
            if owned.active_turn_id != turn_id:
                raise DaemonProtocolError("Codex turn is not the active daemon-owned turn")
        await asyncio.to_thread(
            owned.client.request,
            "turn/interrupt",
            {"threadId": session_id, "turnId": turn_id},
        )
        return {"status": "running", "turn_id": turn_id, "accepted": True}

    async def _approve(
        self,
        session_id: str,
        owned: _OwnedCodexSession,
        request: Mapping[str, Any],
    ) -> dict[str, Any]:
        approval_id = request.get("approval_id")
        decision = request.get("decision")
        if not isinstance(approval_id, str) or not approval_id:
            raise DaemonProtocolError("Codex approval requires an approval_id")
        if decision not in {"accept", "decline"}:
            raise DaemonProtocolError("Codex approval decision is invalid")
        with self._lock:
            native_request_id = owned.pending_approvals.get(approval_id)
        if native_request_id is None:
            raise DaemonProtocolError("Codex approval is not pending for this daemon session")
        await asyncio.to_thread(
            owned.client.send,
            {"id": native_request_id, "result": {"decision": decision}},
        )
        return {"status": "waiting_approval", "accepted": True}

    async def _settings(
        self,
        session_id: str,
        owned: _OwnedCodexSession,
        request: Mapping[str, Any],
    ) -> dict[str, Any]:
        update: dict[str, str] = {"threadId": session_id}
        model = request.get("model")
        if model is not None:
            if not isinstance(model, str) or not model or len(model) > 160 or any(
                char.isspace() for char in model
            ):
                raise DaemonProtocolError("Codex model is invalid")
            update["model"] = model
        effort = request.get("effort")
        if effort is not None:
            if effort not in REASONING_EFFORTS:
                raise DaemonProtocolError("Codex effort is invalid")
            update["effort"] = effort
        if len(update) == 1:
            raise DaemonProtocolError("Codex settings request is empty")
        await asyncio.to_thread(owned.client.request, "thread/settings/update", update)
        with self._lock:
            status = owned.status
        return {"status": status, "accepted": True, **{key: value for key, value in update.items() if key != "threadId"}}

    async def _delete(self, session_id: str, owned: _OwnedCodexSession) -> dict[str, Any]:
        with self._lock:
            if owned.active_turn_id or owned.status in {"running", "waiting_approval"}:
                raise DaemonProtocolError("Codex session is active")
        if not owned.ephemeral:
            await asyncio.to_thread(
                owned.client.request, "thread/delete", {"threadId": session_id}
            )
            for archived in (False, True):
                rows = await asyncio.to_thread(
                    owned.client.request,
                    "thread/list",
                    {"limit": 100, "archived": archived, "modelProviders": [], "useStateDbOnly": True},
                )
                if any(row.get("id") == session_id for row in rows.get("data", [])):
                    raise DaemonProtocolError("Codex session delete was not confirmed")
        with self._lock:
            self._sessions.pop(session_id, None)
        await asyncio.to_thread(owned.client.stop)
        return {"status": "idle", "deleted": session_id}

    async def _rename(
        self,
        session_id: str,
        owned: _OwnedCodexSession,
        request: Mapping[str, Any],
    ) -> dict[str, Any]:
        title = request.get("title")
        if not isinstance(title, str) or not title.strip() or len(title) > 512 or any(
            char in title for char in "\x00\r\n"
        ):
            raise DaemonProtocolError("Codex title is invalid")
        await asyncio.to_thread(
            owned.client.request, "thread/name/set", {"threadId": session_id, "name": title}
        )
        return {"status": owned.status, "title": title}

    def _owned(self, session_id: str) -> _OwnedCodexSession:
        with self._lock:
            owned = self._sessions.get(session_id)
        if owned is None:
            raise DaemonProtocolError("Codex session is not daemon-owned")
        return owned

    def _workspace(self, raw_workspace: Any) -> Path:
        if raw_workspace is None:
            return self.config.workspace
        if not isinstance(raw_workspace, str) or not raw_workspace or "\x00" in raw_workspace:
            raise DaemonProtocolError("Codex workspace is invalid")
        candidate = Path(raw_workspace).expanduser().resolve()
        if not candidate.is_dir() or not any(
            _inside(candidate, root) for root in self.config.allowed_workspaces
        ):
            raise DaemonProtocolError("Codex workspace is outside daemon allowlist")
        return candidate

    async def _resume(
        self, client: CodexAppServerClient, session_id: str
    ) -> Mapping[str, Any]:
        return await asyncio.to_thread(
            client.request,
            "thread/resume",
            {"threadId": session_id, "excludeTurns": True},
        )

    def _on_notification(
        self,
        session_id: str,
        loop: asyncio.AbstractEventLoop,
        frame: Any,
    ) -> None:
        if not isinstance(frame, Mapping):
            return
        copied = dict(frame)
        with self._lock:
            owned = self._sessions.get(session_id)
            if owned is None:
                return
            status = self._apply_notification_status(owned, copied)
            payload: dict[str, Any] = {
                "agent_id": self.config.agent_id,
                "frame": copied,
            }
            approval_id = self._track_approval(owned, copied)
            if approval_id is not None:
                payload["approval_id"] = approval_id
        self._schedule_emit(loop, session_id, "codex.notification", payload, status)

    def _apply_notification_status(
        self, owned: _OwnedCodexSession, frame: Mapping[str, Any]
    ) -> str:
        method = frame.get("method")
        params = frame.get("params")
        if not isinstance(params, Mapping):
            return owned.status
        turn = params.get("turn")
        turn_id = turn.get("id") if isinstance(turn, Mapping) else None
        if method == "turn/started" and isinstance(turn_id, str) and turn_id:
            owned.active_turn_id = turn_id
            owned.status = "running"
        elif method == "turn/completed" and isinstance(turn_id, str) and turn_id:
            terminal = "error" if turn.get("status") == "failed" else "idle"
            owned.completed_turn_statuses[turn_id] = terminal
            if len(owned.completed_turn_statuses) > 32:
                owned.completed_turn_statuses.pop(next(iter(owned.completed_turn_statuses)))
            if owned.active_turn_id == turn_id:
                owned.active_turn_id = None
            owned.status = terminal
        elif method in _APPROVAL_METHODS:
            owned.status = "waiting_approval"
        elif method == "serverRequest/resolved":
            owned.status = "running" if owned.active_turn_id else "idle"
        return owned.status

    @staticmethod
    def _track_approval(owned: _OwnedCodexSession, frame: Mapping[str, Any]) -> str | None:
        if frame.get("method") not in _APPROVAL_METHODS:
            return None
        native_request_id = frame.get("id")
        if not isinstance(native_request_id, (str, int)) or isinstance(native_request_id, bool):
            return None
        approval_id = f"codex:{native_request_id}"
        owned.pending_approvals[approval_id] = native_request_id
        return approval_id

    def _schedule_emit(
        self,
        loop: asyncio.AbstractEventLoop,
        session_id: str,
        event: str,
        payload: Mapping[str, Any],
        status: str,
    ) -> None:
        try:
            future = asyncio.run_coroutine_threadsafe(
                self.emit(session_id, event, payload, status=status), loop
            )
        except RuntimeError:
            return
        future.add_done_callback(self._report_emit_failure)

    @staticmethod
    def _report_emit_failure(future) -> None:
        try:
            future.result()
        except Exception:  # noqa: BLE001 - event persistence failure must not kill reader threads
            logger.warning("Daemon Codex notification could not be persisted")


def _session_id(request: Mapping[str, Any]) -> str:
    session_id = request.get("session_id")
    if not isinstance(session_id, str) or not session_id or len(session_id) > 256:
        raise DaemonProtocolError("Codex session_id is invalid")
    return session_id


def _inside(path: Path, root: Path) -> bool:
    try:
        path.relative_to(root)
        return True
    except ValueError:
        return False


def _require_json_safe(value: Any, label: str) -> None:
    try:
        json.dumps(value)
    except (TypeError, ValueError) as exc:
        raise DaemonProtocolError(f"{label} must be JSON-safe") from exc


def _validate_input(inputs: Any) -> None:
    if not isinstance(inputs, list) or not inputs:
        raise DaemonProtocolError("Codex input must be a non-empty array")
    for item in inputs:
        if not isinstance(item, Mapping):
            raise DaemonProtocolError("Codex input item must be an object")
        kind = item.get("type")
        if kind == "text":
            if not isinstance(item.get("text"), str) or not item["text"]:
                raise DaemonProtocolError("Codex text input is invalid")
        elif kind in {"image", "audio"}:
            value = item.get("url")
            if not isinstance(value, str) or not value.startswith("data:"):
                raise DaemonProtocolError("Codex media input must use an inline data URL")
        else:
            raise DaemonProtocolError("Codex input type is unsupported")
    _require_json_safe(inputs, "Codex input")
