"""App-side command controller for explicitly daemon-owned Codex sessions."""
from __future__ import annotations

import asyncio
import threading
from collections.abc import Mapping
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from ..codex_inputs import command_input
from ..codex_policy import codex_turn_policy
from ..connections import ConnectionError
from .bridge import DaemonBridge, DaemonBridgeError
from .codex_projection import CodexNativeFrameRouter


class DaemonCodexController:
    """Delegate exact Codex turns to the daemon while retaining App projection.

    The controller is intentionally opt-in.  It does not replace a regular
    ``CodexConnection`` unless the App Server explicitly registers it as that
    agent's native command handler.
    """

    def __init__(self, bridge: DaemonBridge, router: CodexNativeFrameRouter, connection: Any) -> None:
        self.bridge = bridge
        self.router = router
        self.connection = connection
        self._unregister = None
        self._lock = threading.RLock()

    def activate(self) -> None:
        with self._lock:
            if self._unregister is not None:
                return
            agent_id = self._agent_id()
            self._unregister = self.router.register(agent_id, self._on_notification)

    def close(self) -> None:
        with self._lock:
            unregister, self._unregister = self._unregister, None
        if unregister is not None:
            unregister()

    def set_model(self, session_id: str, provider: str, model: str) -> dict[str, Any]:
        choices = self.connection.models(session_id)
        if not any(
            row.get("provider") == provider and row.get("model") == model
            for row in choices
            if isinstance(row, Mapping)
        ):
            raise ConnectionError("所选模型不在当前 Codex 原生目录中。", 422)
        return self._settings(
            session_id,
            {"model": model},
            lambda: self.connection._bindings.get(session_id, {}).get("model") == model
            and self.connection._bindings.get(session_id, {}).get("provider") == provider,
            lambda: dict(self.connection._bindings[session_id]),
            "Codex 模型切换尚未通过原生状态确认。",
        )

    def set_effort(self, session_id: str, effort: str) -> dict[str, Any]:
        from ..native_controls import REASONING_EFFORTS

        if effort not in REASONING_EFFORTS:
            raise ConnectionError("思考强度不在原生支持范围内。", 422)
        return self._settings(
            session_id,
            {"effort": effort},
            lambda: self.connection._efforts.get(session_id) == effort,
            lambda: {"effort": effort},
            "Codex 思考强度尚未读回确认。",
        )

    def create(
        self,
        workspace: str | None,
        title: str | None,
        *,
        ephemeral: bool = False,
        parent_session_id: str | None = None,
    ) -> dict[str, Any]:
        path = self.connection.validate_workspace(workspace)
        if parent_session_id:
            self.connection._scope(parent_session_id)
        fields: dict[str, Any] = {
            "agent_type": self._agent_type(),
            "cwd": path,
            "ephemeral": bool(ephemeral),
            "title": title or "新会话",
        }
        runtime_params = self._runtime_params()
        if runtime_params:
            fields["params"] = runtime_params
        if parent_session_id:
            fields["parent_session_id"] = parent_session_id
        try:
            response = asyncio.run(self.bridge.request_control("session.create", fields))
        except DaemonBridgeError as exc:
            raise ConnectionError("守护进程未确认 Codex 新会话创建。", 503) from exc
        except RuntimeError as exc:
            raise ConnectionError("Codex 新会话必须从服务端同步控制路径调用。", 503) from exc
        result = response.get("result")
        session_id = result.get("session_id") if isinstance(result, Mapping) else None
        if not isinstance(session_id, str) or not session_id:
            raise ConnectionError("守护进程未返回原生 Codex 会话 ID。", 502)
        row_title = title or "新会话"
        thread = {"id": session_id, "cwd": path, "name": row_title}
        with self.connection._lock:
            self.connection._threads[session_id] = thread
            owned_threads = getattr(self.connection, "_owned_threads", None)
            if owned_threads is not None:
                owned_threads.add(session_id)
            model = result.get("model") if isinstance(result, Mapping) else None
            provider = result.get("provider") if isinstance(result, Mapping) else None
            if isinstance(model, str) and model:
                self.connection._bindings[session_id] = {
                    "model": model,
                    "provider": provider if isinstance(provider, str) and provider else None,
                }
        row = {
            "id": session_id,
            "agent_id": self._agent_id(),
            "source_id": self._agent_id(),
            "source_session_id": session_id,
            "title": row_title,
            "workspace": path,
            "status": "idle",
            "updated_at": datetime.now(UTC).isoformat().replace("+00:00", "Z"),
            "history_state": "available",
            "control_state": "owned",
            "project_name": Path(path).name or None,
        }
        connection_id = getattr(self.connection, "connection_id", None)
        if isinstance(connection_id, str) and connection_id:
            row["connection_id"] = connection_id
        if ephemeral:
            row["ephemeral"] = True
        return row

    async def submit(self, command: dict[str, Any]) -> tuple[str, str | None]:
        if command.get("agent_id") != self._agent_id():
            return "failed", "daemon Codex command does not belong to this Agent"
        if getattr(self.connection, "state", None) != "connected":
            return "failed", "Codex projection is not connected."
        session_id = command.get("session_id")
        if not isinstance(session_id, str) or not session_id:
            return "failed", "Codex session identity is invalid."
        action = command.get("action")
        if action == "send":
            return await self._send(session_id, command)
        if action == "stop":
            return await self._stop(session_id, command)
        if action in {"approve", "cancel"}:
            return await self._approve(session_id, command)
        if action == "enqueue":
            return "failed", "daemon-owned Codex does not expose native queue control yet."
        return "failed", "daemon-owned Codex does not support this command action."

    async def _send(self, session_id: str, command: dict[str, Any]) -> tuple[str, str | None]:
        text = command.get("text")
        attachments = command.get("attachments") or []
        if not isinstance(text, str) or not text:
            return "failed", "Codex command text is invalid."
        if not isinstance(attachments, list):
            return "failed", "Codex command attachments are invalid."
        try:
            inputs = command_input(
                getattr(self.connection, "settings", None),
                getattr(self.connection, "store", None),
                command,
            )
        except ConnectionError as exc:
            return "failed", exc.detail
        with self.connection._lock:
            active_turn_id = self.connection._active.get(session_id)
        if isinstance(active_turn_id, str) and active_turn_id:
            try:
                response = await self.bridge.request_control(
                    "session.steer",
                    {"session_id": session_id, "turn_id": active_turn_id, "input": inputs},
                )
                result = response.get("result")
                if not isinstance(result, Mapping) or result.get("turn_id") != active_turn_id:
                    return "unknown", "daemon did not confirm the active Codex turn; command will not retry."
                with self.connection._lock:
                    self.connection._commands[(session_id, active_turn_id)] = dict(command)
                return "accepted", None
            except DaemonBridgeError:
                return "unknown", "daemon Codex steering was not confirmed; command will not retry."
        with self.connection._lock:
            self.connection._pending[session_id] = dict(command)
        try:
            await self._attach(session_id)
            policy = codex_turn_policy(self.connection.get_approval_mode(session_id))
            response = await self.bridge.request_control(
                "session.send",
                {
                    "session_id": session_id,
                    "input": inputs,
                    "params": policy,
                },
            )
            result = response.get("result")
            turn_id = result.get("turn_id") if isinstance(result, Mapping) else None
            if not isinstance(turn_id, str) or not turn_id:
                return "unknown", "daemon did not confirm a native Codex turn ID; command will not retry."
            with self.connection._lock:
                finished = getattr(self.connection, "_finished_pending", set())
                if (session_id, turn_id) not in finished:
                    self.connection._commands[(session_id, turn_id)] = dict(command)
                    if result.get("status") not in {"idle", "error"}:
                        self.connection._active[session_id] = turn_id
            return "accepted", None
        except DaemonBridgeError:
            return "unknown", "daemon Codex delivery was not confirmed; command will not retry."
        finally:
            with self.connection._lock:
                self.connection._pending.pop(session_id, None)
                if hasattr(self.connection, "_finished_pending"):
                    self.connection._finished_pending = {
                        key for key in self.connection._finished_pending if key[0] != session_id
                    }

    async def _stop(self, session_id: str, command: dict[str, Any]) -> tuple[str, str | None]:
        if command.get("target_id") != session_id:
            return "failed", "daemon Codex stop must target the current session ID."
        with self.connection._lock:
            turn_id = self.connection._active.get(session_id)
            if not isinstance(turn_id, str) or not turn_id:
                return "failed", "daemon-owned Codex has no confirmed active turn."
            stops = self.connection._stop_commands.setdefault((session_id, turn_id), [])
            stops.append(dict(command))
        try:
            await self.bridge.request_control(
                "session.interrupt",
                {"session_id": session_id, "turn_id": turn_id},
            )
            return "accepted", None
        except DaemonBridgeError:
            with self.connection._lock:
                stops = self.connection._stop_commands.get((session_id, turn_id), [])
                self.connection._stop_commands[(session_id, turn_id)] = [
                    row for row in stops if row.get("id") != command.get("id")
                ]
            return "unknown", "daemon Codex stop delivery was not confirmed; command will not retry."

    async def _approve(self, session_id: str, command: dict[str, Any]) -> tuple[str, str | None]:
        target_id = command.get("target_id")
        with self.connection._lock:
            approval = self.connection._approvals.get(target_id)
            if not isinstance(approval, Mapping) or approval.get("session_id") != session_id:
                return "failed", "Codex approval does not belong to this daemon-owned session."
            native_id = approval.get("native_id")
            if not isinstance(native_id, (str, int)) or isinstance(native_id, bool):
                return "failed", "Codex approval native identity is invalid."
            key = (session_id, native_id)
            if key in self.connection._approval_commands:
                return "failed", "Codex approval is already awaiting native confirmation."
            self.connection._approval_commands[key] = (dict(command), dict(approval))
        try:
            await self.bridge.request_control(
                "session.approve",
                {
                    "session_id": session_id,
                    "approval_id": f"codex:{native_id}",
                    "decision": "accept" if command.get("action") == "approve" else "decline",
                },
            )
            return "accepted", None
        except DaemonBridgeError:
            with self.connection._lock:
                self.connection._approval_commands.pop((session_id, native_id), None)
            return "unknown", "daemon Codex approval delivery was not confirmed; command will not retry."

    async def _attach(self, session_id: str) -> None:
        fields: dict[str, Any] = {
            "session_id": session_id,
            "agent_type": self._agent_type(),
            "params": self._runtime_params(),
        }
        threads = getattr(self.connection, "_threads", {})
        thread = threads.get(session_id) if isinstance(threads, Mapping) else None
        cwd = thread.get("cwd") if isinstance(thread, Mapping) else None
        if isinstance(cwd, str) and cwd:
            fields["cwd"] = cwd
        await self.bridge.request_control("session.spawn", fields)

    def _agent_type(self) -> str:
        connection_id = getattr(self.connection, "connection_id", None)
        return "codex-ssh" if isinstance(connection_id, str) and connection_id else "codex"

    def _runtime_params(self) -> dict[str, Any]:
        connection_id = getattr(self.connection, "connection_id", None)
        if not isinstance(connection_id, str) or not connection_id:
            return {}
        ssh_settings = getattr(self.connection, "ssh_settings", None)
        executable = getattr(self.connection, "remote_executable", None)
        if not isinstance(ssh_settings, Mapping) or not isinstance(executable, str) or not executable:
            raise ConnectionError("远程 Codex daemon binding 配置不完整。", 503)
        return {
            "connection_id": connection_id,
            "ssh_settings": {**dict(ssh_settings), "codex_executable": executable},
        }

    def _settings(
        self,
        session_id: str,
        updates: Mapping[str, str],
        confirmed,
        result,
        timeout_detail: str,
    ) -> dict[str, Any]:
        async def send() -> None:
            await self._attach(session_id)
            await self.bridge.request_control(
                "session.settings", {"session_id": session_id, **dict(updates)}
            )

        try:
            asyncio.run(send())
        except DaemonBridgeError as exc:
            raise ConnectionError("守护进程未确认 Codex 设置修改。", 503) from exc
        except RuntimeError as exc:
            raise ConnectionError("Codex 设置必须从服务端同步控制路径调用。", 503) from exc
        with self.connection._binding_changed:
            if not self.connection._binding_changed.wait_for(confirmed, timeout=3):
                raise ConnectionError(timeout_detail, 502)
            return result()

    def _agent_id(self) -> str:
        agent_id = getattr(self.connection, "agent_id", None)
        if not isinstance(agent_id, str) or not agent_id:
            raise RuntimeError("Codex connection has no stable agent ID")
        return agent_id

    def _on_notification(self, frame: dict[str, Any]) -> None:
        self.connection._notification(frame)
