from __future__ import annotations

import asyncio
import logging
import re
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any
from uuid import uuid4

from fastapi import WebSocket

from .config import Settings
from .events import EventHub
from .handoff import HANDOFF_USER_MARKER
from .schemas import AgentModel, MessageModel, SessionModel, TaskModel
from .store import DuplicateCommand, ScopeNotFound, Store, UnknownCommand

logger = logging.getLogger(__name__)


KNOWN_EVENT_TYPES = {
    "agent.upsert",
    "session.upsert",
    "session.delete",
    "message.upsert",
    "task.upsert",
    "command.upsert",
    "approval.upsert",
}
COMMAND_STATES = {
    "received",
    "queued",
    "accepted",
    "running",
    "completed",
    "failed",
    "unknown",
    "cancelled",
}


def _is_placeholder_title(title: str | None) -> bool:
    if not title:
        return True
    t = title.strip().lower()
    placeholders = {
        "新会话", "未命名", "未命名会话", "untitled", "untitled session",
        "new session", "astrorder 远程会话", "none", "null"
    }
    if t in placeholders:
        return True
    if re.match(r"^[0-9a-f]{8,}$", t, re.I):
        return True
    if re.match(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$", t, re.I):
        return True
    if re.match(r"^d{8}_d{6}_[0-9a-f]+$", t, re.I):
        return True
    return False


class ProtocolError(ValueError):
    pass


class CommandRejected(RuntimeError):
    def __init__(self, detail: str, status_code: int = 409):
        self.detail = detail
        self.status_code = status_code
        super().__init__(detail)


NativeCommandHandler = Callable[[dict[str, Any]], Awaitable[tuple[str, str | None]]]
NativeHistoryHandler = Callable[[str], Awaitable[list[dict[str, Any]]]]
ModelBindingRestorer = Callable[[str, str, str, str, str | None], Awaitable[None]]


def _queued_runtime(handler: NativeCommandHandler | None, operation: str):
    runtime = getattr(handler, "__self__", None)
    method = getattr(runtime, operation, None)
    if not callable(method):
        raise CommandRejected("当前 Agent 不支持管理服务端排队消息。", 409)
    return method


@dataclass
class ConnectorConnection:
    websocket: WebSocket
    agent: dict[str, Any]
    sent: set[tuple[str, str]] = field(default_factory=set)
    send_lock: asyncio.Lock = field(default_factory=asyncio.Lock)

    async def send(self, payload: dict[str, Any]) -> None:
        async with self.send_lock:
            await self.websocket.send_json(payload)


class ControlService:
    def __init__(self, store: Store, hub: EventHub, settings: Settings):
        self.store = store
        self.hub = hub
        self.settings = settings
        self.connections: dict[str, ConnectorConnection] = {}
        self._native_command_handlers: dict[str, NativeCommandHandler] = {}
        self._native_command_capabilities: dict[str, tuple[set[str], str | None]] = {}
        self._native_history_handlers: dict[str, NativeHistoryHandler] = {}
        self._model_binding_restorers: dict[str, ModelBindingRestorer] = {}
        self._approval_handlers: list[Any] = []
        try:
            self._loop = asyncio.get_running_loop()
        except RuntimeError:
            self._loop = None

    def register_native_command_handler(
        self,
        agent_id: str,
        handler: NativeCommandHandler,
        *,
        capabilities: set[str] | None = None,
        limitation: str | None = None,
    ) -> None:
        self._native_command_handlers[agent_id] = handler
        if capabilities is not None or limitation is not None:
            self._native_command_capabilities[agent_id] = (capabilities or set(), limitation)
        self._publish_capabilities(agent_id)

    def clear_native_command_handler(self, agent_id: str) -> None:
        self._native_command_handlers.pop(agent_id, None)
        self._native_command_capabilities.pop(agent_id, None)

    def update_queued_command(self, agent_id: str, session_id: str, command_id: str, text: str) -> dict[str, Any]:
        return _queued_runtime(self._native_command_handlers.get(agent_id), "update_queued")(session_id, command_id, text)

    def cancel_queued_command(self, agent_id: str, session_id: str, command_id: str) -> dict[str, Any]:
        return _queued_runtime(self._native_command_handlers.get(agent_id), "remove_queued")(session_id, command_id)

    async def send_queued_command(self, agent_id: str, session_id: str, command_id: str) -> dict[str, Any]:
        return await asyncio.to_thread(
            _queued_runtime(self._native_command_handlers.get(agent_id), "send_queued"),
            session_id,
            command_id,
        )

    def register_model_binding_restorer(self, kind: str, restorer: ModelBindingRestorer) -> None:
        self._model_binding_restorers[kind] = restorer

    def preserves_model_binding(self, kind: str) -> bool:
        return kind in self._model_binding_restorers

    def register_approval_handler(self, handler: Any) -> None:
        self._approval_handlers.append(handler)

    def approval_snapshot(self) -> list[dict[str, Any]]:
        return [item for handler in self._approval_handlers for item in handler.snapshot()]

    def register_native_history_handler(self, agent_id: str, handler: NativeHistoryHandler) -> None:
        self._native_history_handlers[agent_id] = handler
        self._publish_capabilities(agent_id)

    def effective_agent(self, agent):
        result = dict(agent)
        capabilities = set(agent.get('capabilities', []))
        history = agent['id'] in self._native_history_handlers
        if history:
            capabilities.add('history')
        native_capabilities = self._native_command_capabilities.get(agent['id'])
        if native_capabilities:
            capabilities.update(native_capabilities[0])
            if native_capabilities[1]:
                result['limitation'] = native_capabilities[1]
        if any(agent['id'] in handler.supported for handler in self._approval_handlers):
            capabilities.add('approvals')
        kind = agent.get('kind')
        if kind == 'hermes':
            capabilities.update({'history', 'approvals', 'launch', 'delete', 'queue'})
        elif kind == 'grok':
            capabilities.update({'history', 'launch', 'delete', 'queue', 'approvals', 'task_events', 'attachments'})
        elif kind == 'codex':
            capabilities.update({'launch', 'task_events'})
        result['capabilities'] = sorted(capabilities)
        return result

    def _publish_capabilities(self, agent_id):
        agent = self.store.get_agent(agent_id)
        if isinstance(agent, dict):
            self._server_event('agent.upsert', agent_id=agent_id, session_id=None, data=agent)

    def clear_native_history_handler(self, agent_id: str) -> None:
        self._native_history_handlers.pop(agent_id, None)

    def _publish(self, event: dict[str, Any] | None) -> None:
        if event is not None:
            if event.get('type') == 'agent.upsert':
                event = {**event, 'data': self.effective_agent(event['data'])}
            try:
                loop = asyncio.get_running_loop()
            except RuntimeError:
                loop = None
            if self._loop is not None and loop is not self._loop and not self._loop.is_closed():
                self._loop.call_soon_threadsafe(self.hub.publish, event)
            else:
                self.hub.publish(event)

    def _server_event(
        self,
        event_type: str,
        *,
        agent_id: str | None,
        session_id: str | None,
        data: dict[str, Any],
    ) -> dict[str, Any] | None:
        if event_type == 'agent.upsert':
            data = self.effective_agent(data)
        event = self.store.append_event(
            event_id=f"server-{uuid4()}",
            event_type=event_type,
            agent_id=agent_id,
            session_id=session_id,
            data=data,
        )
        self._publish(event)
        return event

    def record_native_sessions(self, sessions: list[dict[str, Any]]) -> None:
        """Persist read-only native history discovered outside the connector event stream."""
        for data in sessions:
            previous = self.store.get_session(data["agent_id"], data["id"])
            canonical = self.store.upsert_session(data)
            if canonical == previous:
                continue
            self._server_event(
                "session.upsert",
                agent_id=canonical["agent_id"],
                session_id=canonical["id"],
                data=canonical,
            )

    def delete_session(self, agent_id: str, session_id: str) -> bool:
        success = self.store.delete_session(agent_id, session_id)
        if success:
            self._server_event(
                "session.delete",
                agent_id=agent_id,
                session_id=session_id,
                data={"id": session_id, "agent_id": agent_id},
            )
        return success

    def delete_project(
        self,
        *,
        project_key: str | None = None,
        project_id: str | None = None,
        source_id: str | None = None,
        workspace: str | None = None,
        session_keys: list[tuple[str, str]] | None = None,
        delete_sessions: bool = True,
    ) -> dict[str, Any]:
        success, deleted_sessions = self.store.delete_project(
            project_key=project_key,
            project_id=project_id,
            source_id=source_id,
            workspace=workspace,
            session_keys=session_keys,
            delete_sessions=delete_sessions,
        )
        if success:
            self._server_event(
                "project.delete",
                agent_id=None,
                session_id=None,
                data={
                    "project_key": project_key,
                    "project_id": project_id,
                    "source_id": source_id,
                    "workspace": workspace,
                    "deleted_sessions": deleted_sessions,
                },
            )
            for sess in deleted_sessions:
                self._server_event(
                    "session.delete",
                    agent_id=sess["agent_id"],
                    session_id=sess["id"],
                    data={"id": sess["id"], "agent_id": sess["agent_id"]},
                )
        return {"ok": success, "deleted_sessions": deleted_sessions}

    def record_native_projects(self, projects: list[dict[str, Any]]) -> None:
        """Persist the native project catalog, including projects with no sessions."""
        for canonical in self.store.upsert_projects(projects):
            self._server_event(
                "project.upsert",
                agent_id=canonical.get("agent_id"),
                session_id=None,
                data=canonical,
            )

    def mark_persisted_connectors_disconnected(self) -> None:
        """A process restart cannot preserve a live WebSocket, so stale rows must not look controllable."""
        for agent in self.store.list_agents():
            if agent["status"] == "disconnected":
                continue
            updated = self.store.set_agent_status(agent["id"], "disconnected")
            if updated is not None:
                self._server_event("agent.upsert", agent_id=agent["id"], session_id=None, data=updated)

    async def register_connector(self, websocket: WebSocket, agent: dict[str, Any]) -> ConnectorConnection:
        existing = self.connections.get(agent["id"])
        if existing is not None:
            await self.disconnect(existing, "Connector connection replaced")
        canonical = self.store.upsert_agent(agent)
        if (
            canonical.get("kind") == "hermes"
            and canonical.get("connection_id") is None
            and str(canonical.get("source_id") or "").startswith("hermes-local-")
        ):
            reconciled = self.store.reconcile_legacy_local_source(
                source_id=str(canonical["source_id"]),
                current_agent_id=str(canonical["id"]),
                profile_name=str(canonical.get("profile_name") or "default"),
            )
            for legacy_agent in reconciled["agents"]:
                self._server_event(
                    "agent.upsert",
                    agent_id=legacy_agent["id"],
                    session_id=None,
                    data=legacy_agent,
                )
            for legacy_session in reconciled["sessions"]:
                self._server_event(
                    "session.upsert",
                    agent_id=legacy_session["agent_id"],
                    session_id=legacy_session["id"],
                    data=legacy_session,
                )
        connection = ConnectorConnection(websocket=websocket, agent=canonical)
        self.connections[canonical["id"]] = connection
        hello_event = self._server_event(
            "agent.upsert", agent_id=canonical["id"], session_id=None, data=canonical
        )
        if hello_event is not None:
            await connection.send({"type": "event", "event": hello_event})
        await self._dispatch_queued(connection)
        return connection

    async def disconnect(self, connection: ConnectorConnection, reason: str) -> None:
        agent_id = connection.agent["id"]
        if self.connections.get(agent_id) is connection:
            self.connections.pop(agent_id, None)
            # Native RPC handlers belong to ConnectionController, not this event
            # socket. Only explicit runtime disconnect clears those handlers.
            for session_id, command_id in tuple(connection.sent):
                command = self.store.get_command(agent_id, session_id, command_id)
                if command and command["state"] in {"accepted", "running", "received"}:
                    updated = self.store.set_command_state(
                        agent_id,
                        session_id,
                        command_id,
                        "unknown",
                        reason,
                    )
                    self._server_event(
                        "command.upsert",
                        agent_id=agent_id,
                        session_id=session_id,
                        data=updated,
                    )
            updated_agent = self.store.set_agent_status(agent_id, "disconnected")
            if updated_agent is not None:
                self._server_event(
                    "agent.upsert", agent_id=agent_id, session_id=None, data=updated_agent
                )

    def _capability_error(self, agent: dict[str, Any], command: dict[str, Any]) -> str | None:
        agent = self.effective_agent(agent)
        native_stop = agent["id"] in self._native_command_handlers and command["action"] == "stop"
        if native_stop and command.get("target_id") != command["session_id"]:
            return "停止原生会话必须使用当前会话 ID；未停止其他任务。"
        required = {
            "send": "chat",
            "enqueue": "queue",
            "stop": "stop",
            "approve": "approvals",
            "cancel": "stop",
        }[command["action"]]
        if required not in agent["capabilities"] and not native_stop:
            return f"Action {command['action']} is unsupported by this connector"
        if command["attachments"] and "attachments" not in agent["capabilities"]:
            return "Attachments are unsupported by this connector"
        if command["action"] in {"stop", "approve", "cancel"} and not command.get("target_id"):
            return f"Action {command['action']} requires target_id"
        return None

    async def submit_browser_command(self, payload: dict[str, Any]) -> dict[str, Any]:
        agent = self.store.get_agent(payload["agent_id"])
        if agent is None:
            raise CommandRejected("Agent was not found", 404)
        session = self.store.get_session(payload["agent_id"], payload["session_id"])
        if session is None:
            raise CommandRejected("Session was not found", 404)
        legacy_local = session.get("history_state") == "local" and session.get("control_state") == "unknown"
        # 允许用户在任意历史会话或活动会话中直接发消息交互，不再阻塞抛错
        if session.get("control_state") not in {"owned", "live"} and not legacy_local:
            pass
        try:
            attachments = self.store.attachment_rows(payload["attachment_ids"])
        except ScopeNotFound:
            raise CommandRejected("Attachment was not found", 404) from None
        initial_state = "queued" if payload["action"] == "enqueue" else "received"
        try:
            command, created = self.store.create_command(
                command=payload, attachments=attachments, initial_state=initial_state
            )
        except DuplicateCommand as exc:
            if exc.existing.get("mismatch"):
                raise CommandRejected("Command ID already exists with a different payload") from None
            raise
        if not created:
            return command

        self._server_event(
            "command.upsert",
            agent_id=command["agent_id"],
            session_id=command["session_id"],
            data=command,
        )

        capability_error = self._capability_error(agent, command)
        if capability_error:
            updated = self.store.set_command_state(
                command["agent_id"], command["session_id"], command["id"], "failed", capability_error
            )
            self._server_event(
                "command.upsert",
                agent_id=command["agent_id"],
                session_id=command["session_id"],
                data=updated,
            )
            raise CommandRejected(capability_error)

        if command["action"] == "send":
            # 若会话标题仍为默认占位符，且本次包含有意义用户输入，即刻更新会话标题，避免长久显示项目名
            try:
                s_row = self.store.get_session(command["agent_id"], command["session_id"])
                if s_row and _is_placeholder_title(s_row.get("title")):
                    raw_text = (command.get("text") or "").strip()
                    cleaned = re.sub(r"【[^】]+】", "", raw_text)
                    cleaned = re.sub(r'@(?:"[^"]+"|[^s@]+)', '', cleaned).strip()
                    first_l = cleaned.splitlines()[0].strip() if cleaned else raw_text.splitlines()[0].strip()
                    if first_l and not _is_placeholder_title(first_l):
                        new_t = first_l[:50]
                        now_iso = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
                        up_s = {**s_row, "title": new_t, "updated_at": now_iso}
                        self.store.upsert_session(up_s)
                        self._server_event(
                            "session.upsert",
                            agent_id=command["agent_id"],
                            session_id=command["session_id"],
                            data=up_s,
                        )
            except Exception:
                pass

            binding = self.store.get_session_model_binding(command["agent_id"], command["session_id"])
            restorer = self._model_binding_restorers.get(agent["kind"])
            if binding is not None and restorer is not None:
                try:
                    await restorer(
                        command["agent_id"], command["session_id"], binding["provider"],
                        binding["model"], binding.get("effort")
                    )
                except Exception:  # noqa: BLE001 - sending with a different model would violate the saved binding
                    detail = "保存的会话模型未通过原生读回确认；消息未发送。"
                    updated = self.store.set_command_state(
                        command["agent_id"], command["session_id"], command["id"], "failed", detail
                    )
                    self._server_event(
                        "command.upsert",
                        agent_id=command["agent_id"],
                        session_id=command["session_id"],
                        data=updated,
                    )
                    raise CommandRejected(detail) from None

        connection = self.connections.get(agent["id"])
        if payload["action"] == "enqueue":
            if connection is not None:
                await self._send_command(connection, command)
            return self.store.get_command(command["agent_id"], command["session_id"], command["id"]) or command

        native_handler = self._native_command_handlers.get(agent["id"])
        if command['action'] in {'approve', 'cancel'}:
            approval_handler = next(
                (handler for handler in self._approval_handlers if handler.matches(command)), None
            )
            if approval_handler is not None:
                native_handler = approval_handler.submit
        if connection is None and native_handler is None:
            detail = "Connector is not connected; submission was not attempted"
            updated = self.store.set_command_state(
                command["agent_id"], command["session_id"], command["id"], "failed", detail
            )
            self._server_event(
                "command.upsert",
                agent_id=command["agent_id"],
                session_id=command["session_id"],
                data=updated,
            )
            raise CommandRejected(detail)

        handoff_context = (
            self.store.claim_session_handoff_context(command["agent_id"], command["session_id"])
            if command["action"] == "send"
            else None
        )
        forwarded_command = (
            {**command, "text": f"{handoff_context}\n\n{HANDOFF_USER_MARKER}\n{command['text']}"}
            if handoff_context
            else command
        )

        if native_handler is not None:
            try:
                state, error = await native_handler(forwarded_command)
                if state == "accepted" and command["action"] == "send":
                    updated_sess = self.store.update_session(command["agent_id"], command["session_id"], {"status": "running"})
                    if updated_sess:
                        self._server_event(
                            "session.upsert",
                            agent_id=command["agent_id"],
                            session_id=command["session_id"],
                            data=updated_sess,
                        )
            except Exception as exc:
                logger.exception("native_handler execution failed")
                state, error = "unknown", f"Native command execution error: {exc}"
            if state not in {"accepted", "failed", "unknown"}:
                state, error = "unknown", "Native runtime returned an invalid command outcome"
            confirmed = self.store.get_command(command['agent_id'], command['session_id'], command['id'])
            if confirmed and (confirmed['state'] in {'completed', 'failed', 'cancelled'} or (state == 'accepted' and confirmed['state'] == 'running')):
                if handoff_context and confirmed['state'] == 'failed':
                    self.store.restore_session_handoff_context(
                        command["agent_id"], command["session_id"], handoff_context
                    )
                return confirmed
            if state == "accepted" and connection is not None:
                connection.sent.add((command["session_id"], command["id"]))
            updated = self.store.set_command_state(
                command["agent_id"], command["session_id"], command["id"], state, error, preserve_progress=True
            )
            self._server_event(
                "command.upsert",
                agent_id=command["agent_id"],
                session_id=command["session_id"],
                data=updated,
            )
            if handoff_context and state == "failed":
                self.store.restore_session_handoff_context(
                    command["agent_id"], command["session_id"], handoff_context
                )
            return updated

        try:
            await self._send_command(connection, forwarded_command)
        except (OSError, RuntimeError):
            await self.disconnect(
                connection, "Submission outcome is unknown; connector disconnected before confirmation"
            )
            unknown = self.store.get_command(command["agent_id"], command["session_id"], command["id"])
            if unknown is not None:
                return unknown
        return self.store.get_command(command["agent_id"], command["session_id"], command["id"]) or command

    async def load_native_history(self, agent_id: str, session_id: str) -> list[dict[str, Any]]:
        session = self.store.get_session(agent_id, session_id)
        if session is None:
            raise CommandRejected("Session was not found", 404)
        handler = self._native_history_handlers.get(agent_id)
        if handler is None:
            raise CommandRejected("会话所属运行时尚未连接，暂时无法同步消息。", 503)
        try:
            messages = await handler(session_id)
        except Exception as exc:
            self.store.set_session_history_state(agent_id, session_id, "error")
            raise CommandRejected("原生会话消息同步失败，请稍后重试。", 502) from exc
        updated_session = self.store.set_session_history_state(agent_id, session_id, "loaded")
        if updated_session is not None:
            self._server_event(
                "session.upsert",
                agent_id=agent_id,
                session_id=session_id,
                data=updated_session,
            )
        for message in messages:
            canonical = self.store.upsert_message(message)
            self._server_event(
                "message.upsert",
                agent_id=agent_id,
                session_id=session_id,
                data=canonical,
            )
        return messages

    async def _send_command(self, connection: ConnectorConnection, command: dict[str, Any]) -> None:
        connection.sent.add((command["session_id"], command["id"]))
        await connection.send({"type": "command", "command": command})

    async def _dispatch_queued(self, connection: ConnectorConnection) -> None:
        for command in self.store.pending_commands(connection.agent["id"]):
            if command["state"] == "queued":
                try:
                    await self._send_command(connection, command)
                except (OSError, RuntimeError):
                    await self.disconnect(connection, "Connector disconnected before queue acceptance")
                    return

    def accept_connector_event(self, agent_id: str, event: dict[str, Any]) -> dict[str, Any] | None:
        event_id = event.get("id")
        event_type = event.get("type")
        event_agent_id = event.get("agent_id")
        session_id = event.get("session_id")
        data = event.get("data")
        if not isinstance(event_id, str) or not event_id or len(event_id) > 256:
            raise ProtocolError("Connector event ID is invalid")
        if not isinstance(event_type, str) or event_type not in KNOWN_EVENT_TYPES:
            raise ProtocolError("Connector event type is unsupported")
        if event_agent_id != agent_id:
            raise ProtocolError("Connector event agent identity does not match hello")
        if not isinstance(data, dict):
            raise ProtocolError("Connector event data must be an object")

        if event_type == "agent.upsert":
            model = AgentModel.model_validate(data)
            if model.id != agent_id:
                raise ProtocolError("Agent event identity does not match hello")
            canonical_data = model.model_dump()
        elif event_type == "session.upsert":
            model = SessionModel.model_validate(data)
            if model.agent_id != agent_id or model.id != session_id:
                raise ProtocolError("Session event identity does not match envelope")
            canonical_data = model.model_dump()
        elif event_type == "message.upsert":
            model = MessageModel.model_validate(data)
            if model.agent_id != agent_id or model.session_id != session_id:
                raise ProtocolError("Message event identity does not match envelope")
            if self.store.get_session(agent_id, session_id) is None:
                raise ProtocolError("Message event references an unknown session")
            attachment_ids = [item.id for item in model.attachments]
            try:
                canonical_attachments = self.store.attachment_rows(attachment_ids)
            except ScopeNotFound:
                raise ProtocolError("Message references an unknown attachment") from None
            canonical_data = model.model_dump()
            canonical_data["attachments"] = canonical_attachments
        elif event_type == "task.upsert":
            model = TaskModel.model_validate(data)
            if model.agent_id != agent_id or model.session_id != session_id:
                raise ProtocolError("Task event identity does not match envelope")
            if self.store.get_session(agent_id, session_id) is None:
                raise ProtocolError("Task event references an unknown session")
            canonical_data = model.model_dump()
        elif event_type == "command.upsert":
            command_id = data.get("id")
            state = data.get("state")
            if not isinstance(command_id, str) or not isinstance(state, str) or state not in COMMAND_STATES:
                raise ProtocolError("Command confirmation is invalid")
            if data.get("agent_id") != agent_id or data.get("session_id") != session_id:
                raise ProtocolError("Command confirmation identity does not match envelope")
            if self.store.get_session(agent_id, session_id) is None:
                raise ProtocolError("Command confirmation references an unknown session")
            command_error = data.get("error")
            if command_error is not None and not isinstance(command_error, str):
                raise ProtocolError("Command confirmation error is invalid")
            canonical_data = {"id": command_id, "state": state, "error": data.get("error")}
        else:
            if data.get("agent_id", agent_id) != agent_id:
                raise ProtocolError("Approval identity does not match hello")
            if session_id is not None and self.store.get_session(agent_id, session_id) is None:
                raise ProtocolError("Approval references an unknown session")
            canonical_data = data

        try:
            result = self.store.apply_connector_event(
                event_id=event_id,
                event_type=event_type,
                agent_id=agent_id,
                session_id=session_id,
                data=canonical_data,
            )
        except UnknownCommand:
            raise ProtocolError("Command confirmation refers to an unknown command") from None
        self._publish(result)
        return result

    def apply_connector_hello_event(self, agent: dict[str, Any]) -> None:
        self._server_event("agent.upsert", agent_id=agent["id"], session_id=None, data=agent)

    async def shutdown(self) -> None:
        sent = {
            (connection.agent["id"], session_id, command_id)
            for connection in self.connections.values()
            for session_id, command_id in connection.sent
        }
        for command in self.store.active_commands():
            was_sent = (command["agent_id"], command["session_id"], command["id"]) in sent
            if was_sent and command["state"] in {"received", "accepted", "running"}:
                state, error = "unknown", "Server shutdown before connector confirmation"
            elif command["state"] in {"received", "queued"}:
                state, error = "cancelled", "Server shutdown cancelled the queued command"
            else:
                state, error = "unknown", "Server shutdown before connector confirmation"
            updated = self.store.set_command_state(
                command["agent_id"], command["session_id"], command["id"], state, error
            )
            self._server_event(
                "command.upsert",
                agent_id=command["agent_id"],
                session_id=command["session_id"],
                data=updated,
            )
        for connection in tuple(self.connections.values()):
            await self.disconnect(connection, "Server shutdown")
            try:
                await connection.websocket.close(code=1012)
            except (OSError, RuntimeError) as exc:
                logger.debug("connector websocket was already closed: %s", exc)
