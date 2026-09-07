from __future__ import annotations

import threading
from datetime import UTC, datetime
from typing import Any
from uuid import uuid4

from .config import HermesConnectorConfig
from .transport import HermesTransport


def _timestamp() -> str:
    return datetime.now(UTC).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def _event(event_type: str, agent_id: str, session_id: str | None, data: dict[str, Any]) -> dict[str, Any]:
    return {
        "id": f"hermes-event-{uuid4()}",
        "cursor": 0,
        "type": event_type,
        "agent_id": agent_id,
        "session_id": session_id,
        "data": data,
    }


class HermesBridge:
    """Hermes public-plugin bridge; no Desktop or renderer APIs are used."""

    def __init__(
        self,
        ctx: Any,
        config: HermesConnectorConfig,
        transport: HermesTransport | Any | None = None,
    ):
        self.ctx = ctx
        self.config = config
        self.transport = transport or HermesTransport(
            config.endpoint, config.secret, self._on_command
        )
        self._session_id: str | None = None
        self._platform = "cli"
        self._streams: dict[tuple[str, str], dict[str, Any]] = {}
        self._streamed_sessions: set[str] = set()
        self._lock = threading.RLock()

    def register(self) -> HermesBridge:
        self.ctx.register_hook("on_session_start", self._on_session_start)
        self.ctx.register_hook("on_session_end", self._on_session_end)
        self.ctx.register_hook("on_session_finalize", self._on_session_finalize)
        self.ctx.register_hook("post_llm_call", self._on_post_llm_call)
        self.ctx.register_hook("on_stream_start", self._on_stream_start)
        self.ctx.register_hook("on_stream_delta", self._on_stream_delta)
        self.ctx.register_hook("on_stream_end", self._on_stream_end)
        return self

    def _agent(self) -> dict[str, Any]:
        return {
            "id": self.config.agent_id,
            "kind": "hermes",
            "name": self.config.agent_name,
            "status": "ready",
            "capabilities": ["chat", "events"],
            "limitation": (
                "Text send uses documented ctx.inject_message(); attachments, stop, queue, "
                "approvals and history are not exposed by this plugin bridge."
            ),
        }

    def _on_session_start(self, session_id: str, platform: str = "cli", **_kwargs: Any) -> None:
        with self._lock:
            self._session_id = session_id
            self._platform = platform or "cli"
        self.transport.start(self._agent())
        self._send_session("running")

    def _on_session_end(self, session_id: str, completed: bool = True, **_kwargs: Any) -> None:
        if session_id == self._session_id:
            self._send_session("idle" if completed else "error")

    def _on_session_finalize(self, **_kwargs: Any) -> None:
        self.transport.stop()

    def _send_session(self, status: str) -> None:
        session_id = self._session_id
        if not session_id:
            return
        data = {
            "id": session_id,
            "agent_id": self.config.agent_id,
            "title": session_id,
            "workspace": self.config.workspace,
            "status": status,
            "updated_at": _timestamp(),
        }
        self.transport.send_event(_event("session.upsert", self.config.agent_id, session_id, data))

    def _emit_message(
        self,
        *,
        message_id: str,
        session_id: str,
        role: str,
        text: str,
        kind: str = "message",
        created_at: str | None = None,
    ) -> None:
        data = {
            "id": message_id,
            "session_id": session_id,
            "agent_id": self.config.agent_id,
            "role": role,
            "kind": kind,
            "text": text,
            "attachments": [],
            "created_at": created_at or _timestamp(),
            "command_id": None,
            "tool": None,
        }
        self.transport.send_event(_event("message.upsert", self.config.agent_id, session_id, data))

    def _on_post_llm_call(
        self,
        session_id: str,
        user_message: str,
        assistant_response: str,
        **_kwargs: Any,
    ) -> None:
        self._emit_message(
            message_id=f"hermes-user-{uuid4()}",
            session_id=session_id,
            role="user",
            text=user_message,
        )
        with self._lock:
            streamed = session_id in self._streamed_sessions
            self._streamed_sessions.discard(session_id)
        if not streamed:
            self._emit_message(
                message_id=f"hermes-assistant-{uuid4()}",
                session_id=session_id,
                role="assistant",
                text=assistant_response,
            )

    def _stream_key(self, session_id: str, turn_id: str) -> tuple[str, str] | None:
        return (session_id, turn_id) if session_id and turn_id else None

    def _on_stream_start(self, session_id: str, turn_id: str = "", **_kwargs: Any) -> None:
        key = self._stream_key(session_id, turn_id)
        if key is not None:
            with self._lock:
                self._streams[key] = {"text": "", "created_at": _timestamp()}

    def _on_stream_delta(
        self, session_id: str, turn_id: str = "", delta: str = "", kind: str = "text", **_kwargs: Any
    ) -> None:
        if kind != "text":
            return
        key = self._stream_key(session_id, turn_id)
        if key is None:
            return
        with self._lock:
            stream = self._streams.setdefault(key, {"text": "", "created_at": _timestamp()})
            stream["text"] += delta
            text = stream["text"]
            created_at = stream["created_at"]
        self._emit_message(
            message_id=f"hermes-assistant-{session_id}-{turn_id}",
            session_id=session_id,
            role="assistant",
            text=text,
            created_at=created_at,
        )

    def _on_stream_end(
        self,
        session_id: str,
        turn_id: str = "",
        final_text: str = "",
        **_kwargs: Any,
    ) -> None:
        key = self._stream_key(session_id, turn_id)
        if key is None:
            return
        with self._lock:
            stream = self._streams.pop(key, {"created_at": _timestamp()})
            self._streamed_sessions.add(session_id)
        self._emit_message(
            message_id=f"hermes-assistant-{session_id}-{turn_id}",
            session_id=session_id,
            role="assistant",
            text=final_text or stream.get("text", ""),
            created_at=stream["created_at"],
        )

    def _send_command_update(
        self, command: dict[str, Any], state: str, error: str | None = None
    ) -> None:
        data = dict(command)
        data["state"] = state
        data["error"] = error
        self.transport.send_event(
            _event("command.upsert", self.config.agent_id, command.get("session_id"), data)
        )

    def _on_command(self, command: dict[str, Any]) -> None:
        if command.get("agent_id") != self.config.agent_id:
            return
        if command.get("action") != "send":
            self._send_command_update(command, "failed", "This Hermes bridge does not support that action")
            return
        if command.get("attachments"):
            self._send_command_update(command, "failed", "Hermes plugin injection does not accept attachments")
            return
        if command.get("session_id") != self._session_id:
            self._send_command_update(command, "failed", "Hermes session is not active")
            return
        try:
            accepted = bool(
                self.ctx.inject_message(
                    command.get("text", ""),
                    role="user",
                    session_key=self.config.session_key if self._platform != "cli" else None,
                )
            )
        except Exception:  # noqa: BLE001 - plugin callback failures become explicit command failures
            accepted = False
        if accepted:
            self._send_command_update(command, "accepted")
        else:
            self._send_command_update(command, "failed", "Hermes rejected message injection")


def register(ctx: Any) -> HermesBridge:
    return HermesBridge(ctx, HermesConnectorConfig.from_env()).register()
