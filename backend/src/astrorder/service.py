from __future__ import annotations

import asyncio
import logging
from dataclasses import dataclass, field
from typing import Any
from uuid import uuid4

from fastapi import WebSocket

from .config import Settings
from .events import EventHub
from .schemas import AgentModel, MessageModel, SessionModel
from .store import DuplicateCommand, ScopeNotFound, Store, UnknownCommand

logger = logging.getLogger(__name__)


KNOWN_EVENT_TYPES = {
    "agent.upsert",
    "session.upsert",
    "message.upsert",
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


class ProtocolError(ValueError):
    pass


class CommandRejected(RuntimeError):
    def __init__(self, detail: str, status_code: int = 409):
        self.detail = detail
        self.status_code = status_code
        super().__init__(detail)


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

    def _publish(self, event: dict[str, Any] | None) -> None:
        if event is not None:
            self.hub.publish(event)

    def _server_event(
        self,
        event_type: str,
        *,
        agent_id: str | None,
        session_id: str | None,
        data: dict[str, Any],
    ) -> dict[str, Any] | None:
        event = self.store.append_event(
            event_id=f"server-{uuid4()}",
            event_type=event_type,
            agent_id=agent_id,
            session_id=session_id,
            data=data,
        )
        self._publish(event)
        return event

    async def register_connector(self, websocket: WebSocket, agent: dict[str, Any]) -> ConnectorConnection:
        existing = self.connections.get(agent["id"])
        if existing is not None:
            await self.disconnect(existing, "Connector connection replaced")
        canonical = self.store.upsert_agent(agent)
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
        required = {
            "send": "chat",
            "enqueue": "queue",
            "stop": "stop",
            "approve": "approvals",
            "cancel": "stop",
        }[command["action"]]
        if required not in agent["capabilities"]:
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

        connection = self.connections.get(agent["id"])
        if payload["action"] == "enqueue":
            if connection is not None:
                await self._send_command(connection, command)
            return self.store.get_command(command["agent_id"], command["session_id"], command["id"]) or command

        if connection is None:
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

        try:
            await self._send_command(connection, command)
        except (OSError, RuntimeError):
            await self.disconnect(
                connection, "Submission outcome is unknown; connector disconnected before confirmation"
            )
            unknown = self.store.get_command(command["agent_id"], command["session_id"], command["id"])
            if unknown is not None:
                return unknown
        return self.store.get_command(command["agent_id"], command["session_id"], command["id"]) or command

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
