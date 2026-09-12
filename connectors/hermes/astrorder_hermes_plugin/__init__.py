from __future__ import annotations

import re
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


_SECRET_TEXT = re.compile(r"(?i)\\b(api[_-]?key|token|password|secret|authorization)\\s*[:=]\\s*[^\\s,;]+")


def _safe_text(value: Any, limit: int = 1200) -> str:
    text = value if isinstance(value, str) else str(value)
    return _SECRET_TEXT.sub(r"\\1=[REDACTED]", text)[:limit]


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
            config.endpoint, config.secret, self._on_command, self._on_transport_connected
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
        self.ctx.register_hook("pre_tool_call", self._on_pre_tool_call)
        self.ctx.register_hook("post_tool_call", self._on_post_tool_call)
        self.ctx.register_hook("subagent_start", self._on_subagent_start)
        self.ctx.register_hook("subagent_stop", self._on_subagent_stop)
        if self.config.connect_on_register:
            self.transport.start(self._agent())
        return self

    def _agent(self, status: str = "connecting") -> dict[str, Any]:
        return {
            "id": self.config.agent_id,
            "kind": "hermes",
            "name": self.config.agent_name,
            "status": status,
            "capabilities": ["chat", "events", "task_events"],
            "source_id": self.config.source_id or self.config.agent_id,
            "connection_id": self.config.connection_id,
            "profile_name": self.config.profile_name,
            "runtime_id": self.config.agent_id,
            "control_state": "owned",
            "limitation": (
                "Astrorder-owned TUI sessions use documented native prompt.submit; other host injection "
                "depends on ctx.inject_message(). Attachments, stop, queue, approvals and history are unsupported."
            ),
        }

    def _on_session_start(self, session_id: str, platform: str = "cli", **_kwargs: Any) -> None:
        with self._lock:
            self._session_id = session_id
            self._platform = platform or "cli"
        self.transport.start(self._agent())
        self._send_session("running")

    def _on_transport_connected(self) -> None:
        self.transport.send_event(
            _event("agent.upsert", self.config.agent_id, None, self._agent("ready"))
        )

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
            "source_id": self.config.source_id or self.config.agent_id,
            "connection_id": self.config.connection_id,
            "source_session_id": session_id,
            "history_state": "live",
            "control_state": "owned",
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
        tool: dict[str, Any] | None = None,
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
            "tool": tool,
        }
        self.transport.send_event(_event("message.upsert", self.config.agent_id, session_id, data))

    def _on_post_llm_call(
        self,
        session_id: str,
        user_message: str,
        assistant_response: str,
        **_kwargs: Any,
    ) -> None:
        # This callback reports completion, not a new user turn. Native history
        # owns user rows; command submission already emits its correlated echo.
        with self._lock:
            self._session_id = session_id
            streamed = session_id in self._streamed_sessions
            self._streamed_sessions.discard(session_id)
        if not streamed:
            self._emit_message(
                message_id=f"hermes-assistant-{uuid4()}",
                session_id=session_id,
                role="assistant",
                text=assistant_response,
            )
        self._send_session("idle")

    def _stream_key(self, session_id: str, turn_id: str) -> tuple[str, str] | None:
        return (session_id, turn_id) if session_id and turn_id else None

    def _on_stream_start(self, session_id: str, turn_id: str = "", **_kwargs: Any) -> None:
        if session_id:
            with self._lock:
                self._session_id = session_id
        key = self._stream_key(session_id, turn_id)
        if key is not None:
            with self._lock:
                self._streams[key] = {"text": "", "created_at": _timestamp()}
        self._send_session("running")

    def _on_stream_delta(
        self, session_id: str, turn_id: str = "", delta: str = "", kind: str = "text", **_kwargs: Any
    ) -> None:
        if kind not in {"text", "reasoning"}:
            return
        key = self._stream_key(session_id, turn_id)
        if key is None:
            return
        with self._lock:
            stream = self._streams.setdefault(key, {"text": "", "created_at": _timestamp()})
            stream[kind] = stream.get(kind, "") + delta
            text = stream[kind]
            created_at = stream["created_at"]
        self._emit_message(
            message_id=f"hermes-assistant-{session_id}-{turn_id}" + (":thinking" if kind == "reasoning" else ""),
            session_id=session_id,
            role="assistant",
            kind="thinking" if kind == "reasoning" else "message",
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

    def _emit_task(
        self,
        *,
        task_id: str,
        session_id: str,
        kind: str,
        title: str,
        status: str,
        target_id: str | None = None,
        progress: dict[str, Any] | None = None,
        logs: list[dict[str, Any]] | None = None,
        created_at: str | None = None,
    ) -> None:
        now = _timestamp()
        data = {
            "id": task_id,
            "session_id": session_id,
            "agent_id": self.config.agent_id,
            "kind": kind,
            "title": _safe_text(title, 240),
            "status": status,
            "progress": progress,
            "command": None,
            "logs": logs or [],
            "target_id": target_id,
            "created_at": created_at or now,
            "updated_at": now,
        }
        self.transport.send_event(_event("task.upsert", self.config.agent_id, session_id, data))

    def _on_pre_tool_call(
        self,
        *,
        tool_name: str = "",
        session_id: str = "",
        tool_call_id: str = "",
        task_id: str = "",
        **_kwargs: Any,
    ) -> None:
        session_id = session_id or self._session_id or ""
        event_id = tool_call_id or task_id
        if not session_id or not event_id:
            return
        background = tool_name.startswith("skill_")
        with self._lock:
            self._session_id = session_id or self._session_id
        if not background:
            self._send_session("running")
        self._emit_message(
            message_id=f"hermes-tool-{event_id}", session_id=session_id,
            role="tool", kind="tool", text="",
            tool={"name": tool_name or "未命名", "call_id": event_id, "status": "running", "background": background},
        )
        self._emit_task(
            task_id=f"tool:{event_id}",
            session_id=session_id,
            kind="background" if background else "tool",
            title=f"工具：{tool_name or '未命名'}",
            status="running",
            target_id=event_id,
            progress={"blocking": False} if background else None,
        )

    def _on_post_tool_call(
        self,
        *,
        tool_name: str = "",
        result: Any = None,
        session_id: str = "",
        tool_call_id: str = "",
        task_id: str = "",
        **_kwargs: Any,
    ) -> None:
        session_id = session_id or self._session_id or ""
        event_id = tool_call_id or task_id
        if not session_id or not event_id:
            return
        background = tool_name.startswith("skill_")
        failed = isinstance(result, dict) and bool(result.get("error"))
        summary = _safe_text(result) if result is not None else "服务端未提供工具结果。"
        self._emit_message(
            message_id=f"hermes-tool-{event_id}", session_id=session_id,
            role="tool", kind="tool", text=summary,
            tool={"name": tool_name or "未命名", "call_id": event_id, "status": "failed" if failed else "completed", "background": background},
        )
        self._emit_task(
            task_id=f"tool:{event_id}",
            session_id=session_id,
            kind="background" if background else "tool",
            title=f"工具：{tool_name or '未命名'}",
            status="failed" if failed else "completed",
            target_id=event_id,
            progress={"blocking": False} if background else None,
            logs=[{"id": f"tool-log:{event_id}", "text": summary, "level": "error" if failed else "info", "created_at": _timestamp()}],
        )

    def _on_subagent_start(
        self,
        *,
        child_session_id: Any = None,
        child_role: str = "",
        session_id: str = "",
        **_kwargs: Any,
    ) -> None:
        session_id = session_id or self._session_id or ""
        if not session_id or not child_session_id:
            return
        child_id = str(child_session_id)
        self._emit_task(
            task_id=f"subagent:{child_id}",
            session_id=session_id,
            kind="subagent",
            title=f"子代理：{child_role or '未命名'}",
            status="running",
            target_id=child_id,
        )

    def _on_subagent_stop(
        self,
        *,
        child_session_id: Any = None,
        child_role: str = "",
        child_status: Any = None,
        child_summary: Any = None,
        session_id: str = "",
        **_kwargs: Any,
    ) -> None:
        session_id = session_id or self._session_id or ""
        if not session_id or not child_session_id:
            return
        child_id = str(child_session_id)
        failed = str(child_status or "").lower() in {"failed", "error", "cancelled"}
        self._emit_task(
            task_id=f"subagent:{child_id}",
            session_id=session_id,
            kind="subagent",
            title=f"子代理：{child_role or '未命名'}",
            status="failed" if failed else "completed",
            target_id=child_id,
            logs=[{"id": f"subagent-log:{child_id}", "text": _safe_text(child_summary or "服务端未提供子代理摘要。"), "level": "error" if failed else "info", "created_at": _timestamp()}],
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
        # 允许向活动会话或当前运行时发送指令，无感转发生效
        # if command.get("session_id") != self._session_id:
        #     self._send_command_update(command, "failed", "Hermes session is not active")
        #     return
        # 用户消息由前端发送触发时立即上报消息事件，确保用户消息在 Agent 响应前落库
        self._emit_message(
            message_id=f"hermes-user-{command.get('id', uuid4())}",
            session_id=command.get("session_id") or self._session_id or "default",
            role="user",
            text=command.get("text", ""),
            created_at=_timestamp(),
        )
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
