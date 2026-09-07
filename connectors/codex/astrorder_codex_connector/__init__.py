from __future__ import annotations

import threading
from datetime import UTC, datetime
from typing import Any
from uuid import uuid4

from .app_server import CodexAppServer
from .config import CodexConnectorConfig
from .protocol import CodexAppServerProtocol
from .transport import CodexTransport


def _timestamp() -> str:
    return datetime.now(UTC).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def _event(event_type: str, agent_id: str, session_id: str | None, data: dict[str, Any]) -> dict[str, Any]:
    return {
        "id": f"codex-event-{uuid4()}",
        "cursor": 0,
        "type": event_type,
        "agent_id": agent_id,
        "session_id": session_id,
        "data": data,
    }


class CodexBridge:
    """Codex app-server companion using its documented JSON-RPC transport."""

    def __init__(
        self,
        config: CodexConnectorConfig,
        app_server: CodexAppServer | Any | None = None,
        transport: CodexTransport | Any | None = None,
    ):
        self.config = config
        self.protocol = CodexAppServerProtocol()
        self.transport = transport or CodexTransport(
            config.endpoint, config.secret, self._on_command
        )
        self.app_server = app_server or CodexAppServer(config, self._on_notification, self.protocol)
        self.session_id: str | None = None
        self._turn_commands: dict[str, dict[str, Any]] = {}
        self._streams: dict[str, dict[str, Any]] = {}
        self._lock = threading.RLock()

    def _agent(self) -> dict[str, Any]:
        return {
            "id": self.config.agent_id,
            "kind": "codex",
            "name": self.config.agent_name,
            "status": "ready",
            "capabilities": ["chat", "stop", "events"],
            "limitation": (
                "The companion uses documented app-server JSON-RPC. Attachments, queue, approvals "
                "and history are not claimed; command IDs are not sent to Codex because persistence "
                "of that association is not documented."
            ),
        }

    def start(self) -> str:
        self.transport.start(self._agent())
        self.app_server.start()
        self.app_server.request("initialize", self.protocol.initialize()["params"])
        self.app_server.send(self.protocol.initialized())
        if self.config.thread_id:
            result = self.app_server.request(
                "thread/resume", {"threadId": self.config.thread_id}
            )
        else:
            result = self.app_server.request("thread/start", {"cwd": str(self.config.workspace)})
        thread = result.get("thread")
        if not isinstance(thread, dict) or not isinstance(thread.get("id"), str):
            raise RuntimeError("Codex app-server did not return a thread ID")  # noqa: TRY004
        self.session_id = thread["id"]
        self.transport.send_event(
            _event(
                "session.upsert",
                self.config.agent_id,
                self.session_id,
                {
                    "id": self.session_id,
                    "agent_id": self.config.agent_id,
                    "title": self.session_id,
                    "workspace": str(self.config.workspace),
                    "status": "idle",
                    "updated_at": _timestamp(),
                },
            )
        )
        return self.session_id

    def stop(self) -> None:
        self.app_server.stop()
        self.transport.stop()

    def _send_update(self, command: dict[str, Any], state: str, error: str | None = None) -> None:
        data = dict(command)
        data["state"] = state
        data["error"] = error
        self.transport.send_event(
            _event("command.upsert", self.config.agent_id, command.get("session_id"), data)
        )

    def _on_command(self, command: dict[str, Any]) -> None:
        if command.get("agent_id") != self.config.agent_id:
            return
        action = command.get("action")
        if action in {"enqueue", "approve"}:
            self._send_update(command, "failed", "This Codex companion does not support that action")
            return
        if command.get("session_id") != self.session_id:
            self._send_update(command, "failed", "Codex thread is not attached")
            return
        if action in {"stop", "cancel"}:
            target_id = command.get("target_id")
            if not isinstance(target_id, str) or not target_id:
                self._send_update(command, "failed", "Action requires target_id")
                return
            try:
                self.app_server.request(
                    "turn/interrupt", {"threadId": self.session_id, "turnId": target_id}
                )
            except RuntimeError:
                self._send_update(command, "failed", "Codex did not accept interruption")
                return
            self._send_update(command, "accepted")
            return
        if action != "send":
            self._send_update(command, "failed", "Unsupported Codex action")
            return
        if command.get("attachments"):
            self._send_update(command, "failed", "Codex app-server attachment mapping is unsupported")
            return
        try:
            result = self.app_server.request(
                "turn/start",
                {
                    "threadId": self.session_id,
                    "input": [{"type": "text", "text": command.get("text", "")}],
                    "cwd": str(self.config.workspace),
                },
            )
            turn = result.get("turn")
            turn_id = turn.get("id") if isinstance(turn, dict) else None
            if not isinstance(turn_id, str):
                raise RuntimeError("Codex did not return a turn ID")  # noqa: TRY004
        except RuntimeError:
            self._send_update(command, "failed", "Codex did not accept the turn")
            return
        with self._lock:
            self._turn_commands[turn_id] = dict(command)
            self._streams[turn_id] = {"text": "", "created_at": _timestamp()}
        self._send_update(command, "running")
        self._send_message(
            f"codex-user-{uuid4()}",
            command.get("text", ""),
            role="user",
            session_id=self.session_id,
        )

    def _send_message(
        self,
        message_id: str,
        text: str,
        *,
        role: str,
        session_id: str,
        created_at: str | None = None,
    ) -> None:
        self.transport.send_event(
            _event(
                "message.upsert",
                self.config.agent_id,
                session_id,
                {
                    "id": message_id,
                    "session_id": session_id,
                    "agent_id": self.config.agent_id,
                    "role": role,
                    "kind": "message",
                    "text": text,
                    "attachments": [],
                    "created_at": created_at or _timestamp(),
                    "command_id": None,
                    "tool": None,
                },
            )
        )

    def _on_notification(self, message: dict[str, Any]) -> None:
        notification = self.protocol.notification(message)
        if notification is None:
            return
        params = notification.params
        if notification.method == "item/agentMessage/delta":
            turn_id = params.get("turnId")
            delta = params.get("delta")
            if not isinstance(turn_id, str) or not isinstance(delta, str) or not self.session_id:
                return
            with self._lock:
                stream = self._streams.setdefault(
                    turn_id, {"text": "", "created_at": _timestamp()}
                )
                stream["text"] += delta
                text = stream["text"]
                created_at = stream["created_at"]
            self._send_message(
                f"codex-assistant-{self.session_id}-{turn_id}",
                text,
                role="assistant",
                session_id=self.session_id,
                created_at=created_at,
            )
        elif notification.method == "turn/completed":
            turn = params.get("turn")
            turn_id = turn.get("id") if isinstance(turn, dict) else params.get("turnId")
            if not isinstance(turn_id, str):
                return
            with self._lock:
                command = self._turn_commands.pop(turn_id, None)
                stream = self._streams.pop(turn_id, None)
            if stream and self.session_id and not stream["text"]:
                self._send_message(
                    f"codex-assistant-{self.session_id}-{turn_id}",
                    "",
                    role="assistant",
                    session_id=self.session_id,
                    created_at=stream["created_at"],
                )
            if command is not None:
                status = turn.get("status") if isinstance(turn, dict) else None
                state = "completed" if status in {"completed", "interrupted", "cancelled"} else "failed"
                self._send_update(
                    command,
                    state,
                    None if state == "completed" else "Codex turn failed",
                )


def run() -> None:
    config = CodexConnectorConfig.from_env()
    bridge = CodexBridge(config)
    bridge.start()
    stop = threading.Event()
    try:
        stop.wait()
    except KeyboardInterrupt:
        pass
    finally:
        bridge.stop()
