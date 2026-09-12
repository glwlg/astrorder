"""App-side control/projection proxy for daemon-owned local Hermes."""
from __future__ import annotations

import asyncio
import hashlib
import threading
from collections.abc import Mapping
from datetime import UTC, datetime
from typing import Any

from ..connections import ConnectionError
from .bridge import DaemonBridge, DaemonBridgeError


def hermes_runtime_control_id(connection_scope: str) -> str:
    """Return a stable opaque daemon binding for one Hermes connection scope."""
    if not isinstance(connection_scope, str) or not connection_scope or len(connection_scope) > 256:
        raise ValueError("connection scope must be a non-empty string up to 256 characters")
    digest = hashlib.sha256(f"astrorder-hermes-runtime\0{connection_scope}".encode()).hexdigest()
    return f"daemon-hermes-{digest[:48]}"


class DaemonHermesController:
    """Delegate local Hermes ownership to the Session Daemon without App Popen.

    This proxy intentionally does not expose native RPC/history/mutation methods:
    those must gain explicit daemon projection contracts before they are enabled.
    """

    daemon_owned = True
    supports_native_history = False
    supports_native_mutation = True

    def __init__(self, bridge: DaemonBridge, *, connection_scope: str = "local") -> None:
        self._bridge = bridge
        self._connection_scope = connection_scope
        self._control_session_id = hermes_runtime_control_id(connection_scope)
        self._lock = threading.RLock()
        self._state = "offline"
        self._detail = "守护进程托管的本机 Hermes 尚未连接。"
        self._agent_id: str | None = None
        self._source_id: str | None = None
        self._profile_name: str | None = None
        self._runtime_id: str | None = None
        self._discovery_callback: Any = None
        self.store: Any = None
        self.service: Any = None
        self.app_settings: Any = None

    def snapshot(self) -> dict[str, object]:
        with self._lock:
            return {
                "kind": "hermes",
                "state": self._state,
                "available": True,
                "agent_id": self._agent_id,
                "source_id": self._source_id,
                "profile_name": self._profile_name,
                "runtime_id": self._runtime_id or self._agent_id,
                "session_id": None,
                "version": None,
                "detail": self._detail,
                "daemon_mode": True,
            }

    def set_discovery_callback(self, callback: Any) -> None:
        # Native discovery belongs to a later daemon projection adapter. Keep the
        # existing ConnectionController call shape without pretending it ran.
        self._discovery_callback = callback

    def sync_connection(self, connected: bool) -> None:
        with self._lock:
            if self._agent_id is None:
                return
            if connected:
                self._state = "connected"
                self._detail = "本机 Hermes 已通过守护进程托管的原生插件连接。"
            elif self._state in {"connecting", "connected"}:
                self._state = "connecting"
                self._detail = "守护进程仍托管本机 Hermes，等待原生插件重新连接。"

    def connect(self) -> dict[str, object]:
        with self._lock:
            if self._state in {"connecting", "connected"} and self._agent_id:
                return self.snapshot()
        response = self._control(
            "session.spawn",
            {
                "session_id": self._control_session_id,
                "agent_type": "hermes",
                "params": {"runtime_control": True},
            },
            "守护进程未确认本机 Hermes 启动。",
        )
        self._apply_identity(response.get("result"))
        with self._lock:
            self._state = "connecting"
            self._detail = "正在等待守护进程托管的 Hermes 原生插件连接。"
        return self.snapshot()

    def disconnect(self) -> dict[str, object]:
        with self._lock:
            should_disconnect = self._agent_id is not None
        if should_disconnect:
            self._control(
                "session.disconnect",
                {"session_id": self._control_session_id},
                "守护进程未确认本机 Hermes 断开。",
            )
        with self._lock:
            self._state = "offline"
            self._detail = "已停止守护进程托管的本机 Hermes 运行时。"
            self._agent_id = None
            self._source_id = None
            self._profile_name = None
            self._runtime_id = None
        return self.snapshot()

    def close_for_app_shutdown(self) -> None:
        """Release App-only projection state without stopping the daemon runtime."""
        self.set_discovery_callback(None)

    def create_session(self, workspace: str | None = None, title: str | None = None) -> dict[str, object]:
        with self._lock:
            agent_id = self._agent_id
        if not agent_id:
            raise ConnectionError("守护进程托管的本机 Hermes 尚未连接；无法新建会话。", 503)
        if workspace is not None and (not isinstance(workspace, str) or not workspace):
            raise ConnectionError("本机 Hermes workspace 无效。", 422)
        if title is not None and (not isinstance(title, str) or not title):
            raise ConnectionError("本机 Hermes 标题无效。", 422)
        result = self._control(
            "session.create",
            {"agent_type": "hermes", "cwd": workspace, "title": title or "新会话"},
            "守护进程未确认本机 Hermes 新会话创建。",
        ).get("result")
        self._assert_identity(result)
        if not isinstance(result, Mapping):
            raise ConnectionError("守护进程未返回本机 Hermes 原生会话。", 502)
        session_id = result.get("session_id")
        if not isinstance(session_id, str) or not session_id:
            raise ConnectionError("守护进程未返回本机 Hermes 原生会话 ID。", 502)
        source_id = result.get("source_id") if isinstance(result.get("source_id"), str) else self._source_id
        return {
            "id": session_id,
            "agent_id": agent_id,
            "title": title or "新会话",
            "workspace": workspace,
            "status": result.get("status", "idle"),
            "source_id": source_id,
            "connection_id": None,
            "source_session_id": session_id,
            "history_state": "live",
            "control_state": "owned",
            "updated_at": datetime.now(UTC).isoformat().replace("+00:00", "Z"),
        }

    def load_native_history_page(
        self, session_id: str, before: str | None, limit: int
    ) -> dict[str, Any]:
        if not isinstance(session_id, str) or not session_id:
            raise ConnectionError("Hermes session identity is invalid.", 422)
        if before is not None and (not isinstance(before, str) or len(before) > 512):
            raise ValueError("invalid native history cursor")
        if not isinstance(limit, int) or isinstance(limit, bool) or not 1 <= limit <= 200:
            raise ValueError("invalid native history limit")
        spawned = self._control(
            "session.spawn",
            {"session_id": session_id, "agent_type": "hermes", "params": {}},
            "守护进程未确认 Hermes history session binding。",
        )
        self._apply_identity(spawned.get("result"))
        result = self._control(
            "session.history_page",
            {"session_id": session_id, "before": before, "limit": limit},
            "守护进程未返回 Hermes 原生消息分页。",
        ).get("result")
        items = result.get("items") if isinstance(result, Mapping) else None
        next_cursor = result.get("next_cursor") if isinstance(result, Mapping) else None
        if not isinstance(items, list) or next_cursor is not None and not isinstance(next_cursor, str):
            raise ConnectionError("守护进程返回了无效的 Hermes 原生消息分页。", 502)
        return {"items": items, "next_cursor": next_cursor}

    def mutate_session(self, session_id: str, updates: dict[str, Any] | None) -> None:
        if updates is not None:
            title = updates.get("title") if set(updates) == {"title"} else None
            if not isinstance(title, str) or not title.strip():
                raise ConnectionError("会话属性由原生运行时管理；当前支持修改标题。", 422)
            spawned = self._control(
                "session.spawn",
                {"session_id": session_id, "agent_type": "hermes", "params": {}},
                "守护进程未确认 Hermes rename session binding。",
            )
            self._apply_identity(spawned.get("result"))
            result = self._control(
                "session.rename",
                {"session_id": session_id, "title": title},
                "守护进程未确认 Hermes 原生会话重命名。",
            ).get("result")
            if not isinstance(result, Mapping) or result.get("title") != title:
                raise ConnectionError("守护进程未确认 Hermes 原生标题读回。", 502)
            return
        spawned = self._control(
            "session.spawn",
            {"session_id": session_id, "agent_type": "hermes", "params": {}},
            "守护进程未确认 Hermes delete session binding。",
        )
        self._apply_identity(spawned.get("result"))
        result = self._control(
            "session.delete",
            {"session_id": session_id},
            "守护进程未确认 Hermes 原生会话删除。",
        ).get("result")
        if not isinstance(result, Mapping) or result.get("deleted") != session_id:
            raise ConnectionError("守护进程未确认 Hermes 原生会话删除。", 502)

    def _native_control(
        self, session_id: str, action: str, fields: Mapping[str, Any] | None = None
    ) -> dict[str, Any]:
        spawned = self._control(
            "session.spawn",
            {"session_id": session_id, "agent_type": "hermes", "params": {}},
            "守护进程未确认 Hermes native-control session binding。",
        )
        self._apply_identity(spawned.get("result"))
        result = self._control(
            action,
            {"session_id": session_id, **dict(fields or {})},
            "守护进程未确认 Hermes 原生控制操作。",
        ).get("result")
        if not isinstance(result, Mapping):
            raise ConnectionError("守护进程返回了无效的 Hermes 原生控制结果。", 502)
        return dict(result)

    def models(self, session_id: str) -> list[dict[str, str]]:
        result = self._native_control(session_id, "session.models")
        items = result.get("items")
        if not isinstance(items, list):
            raise ConnectionError("守护进程返回了无效的 Hermes 模型目录。", 502)
        return items

    def model(self, session_id: str) -> dict[str, Any]:
        result = self._native_control(session_id, "session.model.read")
        return {key: result.get(key) for key in ("provider", "model", "branch", "effort") if key in result}

    def set_model(self, session_id: str, provider: str, model: str) -> dict[str, Any]:
        result = self._native_control(
            session_id, "session.model.set", {"provider": provider, "model": model}
        )
        return {"provider": result.get("provider"), "model": result.get("model")}

    def current_effort(self, session_id: str) -> str | None:
        return self.model(session_id).get("effort")

    def set_effort(self, session_id: str, effort: str) -> dict[str, Any]:
        result = self._native_control(
            session_id, "session.reasoning.set", {"effort": effort}
        )
        return {"effort": result.get("effort")}

    def get_approval_mode(self, session_id: str) -> str:
        result = self._native_control(session_id, "session.approval.read")
        mode = result.get("mode")
        if not isinstance(mode, str):
            raise ConnectionError("守护进程返回了无效的 Hermes 审批模式。", 502)
        return mode

    def set_approval_mode(self, session_id: str, mode: str) -> dict[str, Any]:
        result = self._native_control(
            session_id, "session.approval.set", {"mode": mode}
        )
        return {"mode": result.get("mode")}

    async def submit_tui_command(self, command: dict[str, Any]) -> tuple[str, str | None]:
        with self._lock:
            agent_id = self._agent_id
        if not agent_id or command.get("agent_id") != agent_id:
            return "failed", "daemon Hermes command does not belong to the connected Agent"
        session_id = command.get("session_id")
        if not isinstance(session_id, str) or not session_id:
            return "failed", "daemon Hermes session identity is invalid."
        action = command.get("action")
        if action == "send":
            text = command.get("text")
            if not isinstance(text, str) or not text and not command.get("attachments"):
                return "failed", "daemon Hermes command text is invalid."
            command_id = command.get("id")
            if not isinstance(command_id, str) or not command_id:
                return "failed", "daemon Hermes command identity is invalid."
            from ..hermes_inputs import pack_daemon_attachments

            try:
                attachments = await asyncio.to_thread(
                    pack_daemon_attachments, command, self.app_settings, self.store
                )
            except ConnectionError as exc:
                return "failed", exc.detail
            daemon_action = "session.send"
            fields: dict[str, Any] = {
                "session_id": session_id,
                "text": text,
                "command_id": command_id,
            }
            if attachments:
                fields["attachments"] = attachments
        elif action == "stop":
            if command.get("target_id") not in {None, session_id}:
                return "failed", "daemon Hermes stop must target the current session ID."
            daemon_action = "session.interrupt"
            fields = {"session_id": session_id}
        else:
            return "failed", "daemon-owned Hermes does not support this command action."
        try:
            spawned = await self._bridge.request_control(
                "session.spawn",
                {"session_id": session_id, "agent_type": "hermes", "params": {}},
            )
            self._apply_identity(spawned.get("result"))
            response = await self._bridge.request_control(daemon_action, fields)
        except DaemonBridgeError:
            return "unknown", "daemon Hermes delivery was not confirmed; command will not retry."
        result = response.get("result")
        if not isinstance(result, Mapping) or result.get("accepted") is not True:
            return "unknown", "daemon Hermes did not confirm native command acceptance; command will not retry."
        return "accepted", None

    def _control(self, action: str, fields: Mapping[str, Any], failure: str) -> dict[str, Any]:
        try:
            response = asyncio.run(self._bridge.request_control(action, fields))
        except DaemonBridgeError as exc:
            raise ConnectionError(failure, 503) from exc
        except RuntimeError as exc:
            raise ConnectionError("守护进程控制必须从服务端同步路径调用。", 503) from exc
        if not isinstance(response, Mapping):
            raise ConnectionError(failure, 502)
        return dict(response)

    def _apply_identity(self, result: Any) -> None:
        self._assert_identity(result)
        if not isinstance(result, Mapping):
            return
        agent_id = result.get("agent_id")
        if not isinstance(agent_id, str) or not agent_id:
            raise ConnectionError("守护进程未确认本机 Hermes Agent identity。", 502)
        with self._lock:
            self._agent_id = agent_id
            for key, attribute in (
                ("source_id", "_source_id"),
                ("profile_name", "_profile_name"),
                ("runtime_id", "_runtime_id"),
            ):
                value = result.get(key)
                if isinstance(value, str) and value:
                    setattr(self, attribute, value)

    def _assert_identity(self, result: Any) -> None:
        if not isinstance(result, Mapping):
            return
        candidate = result.get("agent_id")
        if candidate is not None and (not isinstance(candidate, str) or not candidate):
            raise ConnectionError("守护进程返回了无效的本机 Hermes Agent identity。", 502)
        with self._lock:
            if self._agent_id and isinstance(candidate, str) and candidate != self._agent_id:
                raise ConnectionError("守护进程返回了不同的本机 Hermes Agent identity。", 409)
