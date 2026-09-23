"""Grok Build ACP connection and daemon event projection."""
from __future__ import annotations

import asyncio
import json
import threading
from collections.abc import Callable, Mapping
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from astrorder.connections import ConnectionError
from astrorder.daemon.bridge import DaemonBridge, DaemonBridgeError


def _now() -> str:
    return datetime.now(UTC).isoformat().replace("+00:00", "Z")


def _timestamp(frame: Mapping[str, Any]) -> str:
    params = frame.get("params") if isinstance(frame, Mapping) else {}
    meta = (
        (params.get("_meta") if isinstance(params, Mapping) else None)
        or (frame.get("_meta") if isinstance(frame, Mapping) else None)
    )
    millis = meta.get("agentTimestampMs") if isinstance(meta, Mapping) else None
    if isinstance(millis, (int, float)):
        return datetime.fromtimestamp(millis / 1000, UTC).isoformat().replace("+00:00", "Z")
    if isinstance(frame, Mapping) and isinstance(frame.get("timestamp"), (int, float)):
        return datetime.fromtimestamp(frame["timestamp"], UTC).isoformat().replace("+00:00", "Z")
    return _now()


def _text(value: Any) -> str:
    if isinstance(value, str):
        return value
    if isinstance(value, Mapping):
        if isinstance(value.get("text"), str):
            return value["text"]
        return _text(value.get("content"))
    if isinstance(value, list):
        return "\n".join(filter(None, (_text(item) for item in value)))
    return ""


class GrokProjection:
    def __init__(self, bridge: DaemonBridge, store: Any, service: Any) -> None:
        self.store, self.service = store, service
        self._unregister = (
            bridge.register_native_frame_handler("grok.notification", self._notification),
            bridge.register_native_frame_handler("grok.completed", self._completed),
        )

    def close(self) -> None:
        for unregister in self._unregister:
            unregister()

    def _notification(self, session_id: str, payload: Mapping[str, Any]) -> None:
        agent_id = payload.get("agent_id")
        frame = payload.get("frame")
        if not isinstance(agent_id, str) or not isinstance(frame, Mapping):
            raise TypeError("invalid Grok notification")
        params = frame.get("params")
        update = params.get("update") if isinstance(params, Mapping) else None
        if not isinstance(update, Mapping):
            return
        kind = update.get("sessionUpdate")
        if kind == "turn_completed":
            self._session_status(agent_id, session_id, "idle")
            for task in self.store.list_tasks(agent_id, session_id):
                if task.get("status") == "running":
                    updated_task = {**task, "status": "completed", "updated_at": _now()}
                    self.store.upsert_task(updated_task)
                    self.service._server_event(
                        "task.upsert", agent_id=agent_id, session_id=session_id, data=updated_task
                    )
            return
        if kind in {"tool_call", "tool_call_update"}:
            tool_id = update.get("toolCallId")
            if isinstance(tool_id, str) and tool_id:
                task_status = "completed" if update.get("status") in {"completed", "done", "success"} else "running"
                title = update.get("title") or "Grok tool"
                raw_input = update.get("rawInput")
                cmd_str = json.dumps(raw_input, ensure_ascii=False) if isinstance(raw_input, Mapping) else str(raw_input or "")
                task = {
                    "id": f"grok:task:{tool_id}",
                    "agent_id": agent_id,
                    "session_id": session_id,
                    "kind": "tool",
                    "title": title[:512],
                    "status": task_status,
                    "command": cmd_str,
                    "logs": [],
                    "created_at": _timestamp(frame),
                    "updated_at": _timestamp(frame),
                }
                self.store.upsert_task(task)
                self.service._server_event(
                    "task.upsert", agent_id=agent_id, session_id=session_id, data=task
                )
        if kind == "plan":
            entries = update.get("entries")
            if isinstance(entries, list):
                for i, entry in enumerate(entries):
                    if isinstance(entry, Mapping):
                        step_title = entry.get("content") or entry.get("title") or f"Plan step {i + 1}"
                        raw_st = entry.get("status")
                        step_status = "completed" if raw_st in {"completed", "done"} else "running" if raw_st == "in_progress" else "pending"
                        task = {
                            "id": f"grok:plan:{session_id}:{i}",
                            "agent_id": agent_id,
                            "session_id": session_id,
                            "kind": "todo",
                            "title": step_title[:512],
                            "status": step_status,
                            "command": None,
                            "logs": [],
                            "created_at": _timestamp(frame),
                            "updated_at": _timestamp(frame),
                        }
                        self.store.upsert_task(task)
                        self.service._server_event(
                            "task.upsert", agent_id=agent_id, session_id=session_id, data=task
                        )
        message = self._message(agent_id, session_id, update, frame)
        if message is None:
            return
        existing = self.store.get_message(agent_id, session_id, message["id"])
        if existing:
            if kind in {"user_message_chunk", "agent_message_chunk", "agent_thought_chunk"}:
                message["text"] = existing.get("text", "") + message["text"]
            elif kind in {"tool_call", "tool_call_update"} and existing.get("tool"):
                prev_tool = existing.get("tool") or {}
                cur_tool = message.get("tool") or {}
                if cur_tool.get("name") in {"Grok tool", "", None} and prev_tool.get("name"):
                    cur_tool["name"] = prev_tool["name"]
                if not cur_tool.get("arguments") and prev_tool.get("arguments"):
                    cur_tool["arguments"] = prev_tool["arguments"]
                message["tool"] = cur_tool
                if not message.get("text") and existing.get("text"):
                    message["text"] = existing["text"]
        canonical = self.store.upsert_message(message)
        self.service._server_event(
            "message.upsert", agent_id=agent_id, session_id=session_id, data=canonical
        )

    def _completed(self, session_id: str, payload: Mapping[str, Any]) -> None:
        agent_id = payload.get("agent_id")
        command_id = payload.get("command_id")
        if not isinstance(agent_id, str) or not isinstance(command_id, str):
            raise TypeError("invalid Grok completion")
        state = "failed" if payload.get("error") else "cancelled" if payload.get("cancelled") else "completed"
        command = self.store.get_command(agent_id, session_id, command_id)
        if command is not None:
            updated = self.store.set_command_state(
                agent_id, session_id, command_id, state, payload.get("error")
            )
            self.service._server_event(
                "command.upsert", agent_id=agent_id, session_id=session_id, data=updated
            )
        self._session_status(agent_id, session_id, "error" if state == "failed" else "idle")

    def _session_status(self, agent_id: str, session_id: str, status: str) -> None:
        session = self.store.update_session(agent_id, session_id, {"status": status})
        if session:
            self.service._server_event(
                "session.upsert", agent_id=agent_id, session_id=session_id, data=session
            )

    @staticmethod
    def _message(
        agent_id: str, session_id: str, update: Mapping[str, Any], frame: Mapping[str, Any]
    ) -> dict[str, Any] | None:
        kind = update.get("sessionUpdate")
        params = frame.get("params") if isinstance(frame, Mapping) else {}
        params_meta = params.get("_meta") if isinstance(params, Mapping) and isinstance(params.get("_meta"), Mapping) else {}
        update_meta = update.get("_meta") if isinstance(update, Mapping) and isinstance(update.get("_meta"), Mapping) else {}
        frame_meta = frame.get("_meta") if isinstance(frame, Mapping) and isinstance(frame.get("_meta"), Mapping) else {}

        if kind in {"user_message_chunk", "agent_message_chunk", "agent_thought_chunk"}:
            role = "user" if kind == "user_message_chunk" else "assistant"
            message_kind = "thinking" if kind == "agent_thought_chunk" else "message"
            if kind == "user_message_chunk":
                prompt_index = update_meta.get("promptIndex")
                if prompt_index is not None:
                    token = f"prompt-{prompt_index}"
                else:
                    token = (
                        params_meta.get("eventId")
                        or frame_meta.get("eventId")
                        or params_meta.get("promptId")
                        or frame_meta.get("promptId")
                        or "user"
                    )
                msg_id = f"grok:{token}:user"
            else:
                token = (
                    params_meta.get("promptId")
                    or update_meta.get("promptId")
                    or frame_meta.get("promptId")
                    or params_meta.get("eventId")
                    or frame_meta.get("eventId")
                    or "assistant"
                )
                suffix = "thought" if kind == "agent_thought_chunk" else "message"
                msg_id = f"grok:{token}:{suffix}"
            return {
                "id": msg_id,
                "session_id": session_id,
                "agent_id": agent_id,
                "role": role,
                "kind": message_kind,
                "text": _text(update.get("content")),
                "attachments": [],
                "created_at": _timestamp(frame),
                "command_id": None,
                "tool": None,
            }
        if kind in {"tool_call", "tool_call_update"}:
            tool_id = update.get("toolCallId")
            if not isinstance(tool_id, str) or not tool_id:
                return None
            status = update.get("status") or "running"
            title = update.get("title")
            raw_input = update.get("rawInput")
            return {
                "id": f"grok:tool:{tool_id}",
                "session_id": session_id,
                "agent_id": agent_id,
                "role": "assistant",
                "kind": "tool",
                "text": _text(update.get("content")),
                "attachments": [],
                "created_at": _timestamp(frame),
                "command_id": None,
                "tool": {
                    "name": title or "Grok tool",
                    "arguments": raw_input if isinstance(raw_input, Mapping) else {},
                    "status": status,
                },
            }
        if kind == "plan":
            token = (
                params_meta.get("promptId")
                or update_meta.get("promptId")
                or frame_meta.get("promptId")
                or params_meta.get("eventId")
                or frame_meta.get("eventId")
                or "plan"
            )
            return {
                "id": f"grok:{token}:plan",
                "session_id": session_id,
                "agent_id": agent_id,
                "role": "assistant",
                "kind": "thinking",
                "text": _text(update.get("entries") or update.get("content")),
                "attachments": [],
                "created_at": _timestamp(frame),
                "command_id": None,
                "tool": None,
            }
        return None


class GrokConnection:
    daemon_owned = True

    def __init__(
        self,
        bridge: DaemonBridge,
        store: Any,
        service: Any,
        *,
        executable: str,
        session_reader: Callable[[str | None], list[dict[str, Any]]],
        connection_id: str | None = None,
        ssh_settings: Mapping[str, Any] | None = None,
        display_name: str = "本机",
        session_deleter: Callable[[str], None] | None = None,
    ) -> None:
        self.bridge, self.store, self.service = bridge, store, service
        self.executable = executable
        self.session_reader = session_reader
        self.session_deleter = session_deleter
        self.connection_id = connection_id
        self.ssh_settings = dict(ssh_settings or {})
        self.agent_id = f"ssh-grok-{connection_id}" if connection_id else "local-grok"
        self.display_name = display_name
        self.state = "disconnected"
        self.detail = "尚未连接 Grok Build。"
        self._catalog: list[dict[str, Any]] = []
        self._summaries: dict[str, dict[str, Any]] = {}
        self._attached: set[str] = set()
        self._lock = threading.RLock()
        self._lifecycle = threading.Lock()

    def snapshot(self) -> dict[str, Any]:
        return {
            "kind": "grok",
            "state": self.state,
            "available": bool(self.executable),
            "agent_id": self.agent_id if self.state == "connected" else None,
            "detail": self.detail,
            "daemon_mode": True,
        }

    def connect(self) -> dict[str, Any]:
        with self._lifecycle:
            if self.state == "connected":
                return self.snapshot()
            return self._connect()

    def _connect(self) -> dict[str, Any]:
        initialized = self._control(
            "runtime.request",
            {
                "agent_type": self._agent_type(),
                "method": "initialize",
                "request_params": {},
                "params": self._runtime_params(),
            },
            "小内核未确认 Grok Build ACP 连接。",
        ).get("result")
        meta = initialized.get("_meta") if isinstance(initialized, Mapping) else None
        model_state = meta.get("modelState") if isinstance(meta, Mapping) else None
        self._catalog = list(model_state.get("availableModels") or []) if isinstance(model_state, Mapping) else []
        self._record_sessions(self.session_reader(None))
        self.state, self.detail = "connected", "Grok Build 已通过小内核 ACP 连接。"
        capabilities = [
            "chat",
            "stop",
            "events",
            "history",
            "launch",
            "delete",
            "queue",
            "approvals",
            "task_events",
            "attachments",
        ]
        agent = {
            "id": self.agent_id,
            "kind": "grok",
            "name": f"{self.display_name} · Grok Build" if self.connection_id else "本机 Grok Build",
            "source_id": self.agent_id,
            "connection_id": self.connection_id,
            "status": "ready",
            "capabilities": capabilities,
            "limitation": "Grok Build ACP 当前支持文字会话、历史与停止。",
            "control_state": "owned",
        }
        self.store.upsert_agent(agent)
        self.service.apply_connector_hello_event(agent)
        self.service.register_native_command_handler(
            self.agent_id, self.submit, capabilities=set(capabilities)
        )
        return self.snapshot()

    def disconnect(self) -> dict[str, Any]:
        with self._lifecycle:
            return self._disconnect()

    def _disconnect(self) -> dict[str, Any]:
        for session_id in tuple(self._attached):
            try:
                self._control(
                    "session.disconnect", {"session_id": session_id}, "Grok 会话断开未确认。"
                )
            except ConnectionError:
                pass
        self._attached.clear()
        self.service.clear_native_command_handler(self.agent_id)
        self.state, self.detail = "disconnected", "Grok Build 已断开。"
        updated = self.store.set_agent_status(self.agent_id, "disconnected")
        if updated:
            self.service._server_event(
                "agent.upsert", agent_id=self.agent_id, session_id=None, data=updated
            )
        return self.snapshot()

    def create(self, workspace: str | None, title: str | None, **_: Any) -> dict[str, Any]:
        fields = {
            "agent_type": self._agent_type(),
            "cwd": workspace,
            "params": self._runtime_params(),
        }
        result = self._control(
            "session.create",
            {key: value for key, value in fields.items() if value is not None},
            "小内核未确认 Grok Build 新会话创建。",
        ).get("result")
        session_id = result.get("session_id") if isinstance(result, Mapping) else None
        if not isinstance(session_id, str) or not session_id:
            raise ConnectionError("小内核未返回 Grok Build 会话 ID。", 502)
        self._attached.add(session_id)
        row = self._session_row(
            {"info": {"id": session_id, "cwd": workspace}, "session_summary": title or "新会话"}
        )
        self._summaries[session_id] = {"current_model_id": result.get("model"), **row}
        return row

    async def submit(self, command: dict[str, Any]) -> tuple[str, str | None]:
        session_id = command.get("session_id")
        if not isinstance(session_id, str) or command.get("agent_id") != self.agent_id:
            return "failed", "Grok Build command scope is invalid."
        try:
            await self._attach(session_id)
            action = command.get("action", "send")
            if action in ("send", "enqueue"):
                attachments = None
                raw_attachments = command.get("attachments") or []
                if raw_attachments:
                    from .hermes_inputs import pack_daemon_attachments
                    try:
                        settings = getattr(self.service, "settings", None)
                        attachments = pack_daemon_attachments(command, settings, self.store)
                    except Exception as exc:
                        pass
                await self.bridge.request_control(
                    "session.send",
                    {
                        "session_id": session_id,
                        "prompt": command.get("text") or "",
                        "command_id": command.get("id"),
                        "attachments": attachments,
                    },
                )
                return "accepted", None
            if action == "stop":
                await self.bridge.request_control("session.interrupt", {"session_id": session_id})
                return "accepted", None
            return "failed", "Grok Build does not support this command action."
        except DaemonBridgeError:
            return "unknown", "Grok Build delivery was not confirmed; command will not retry."

    def get_approval_mode(self, session_id: str) -> str:
        saved = self.store.get_session_approval_mode_binding(self.agent_id, session_id)
        return saved or "auto"

    def set_approval_mode(self, session_id: str, mode: str) -> dict[str, Any]:
        self.store.set_session_approval_mode_binding(self.agent_id, session_id, mode)
        return {"mode": mode}

    def models(self, session_id: str) -> list[dict[str, str]]:
        self._scope(session_id)
        return [
            {"provider": "grok", "model": row["modelId"], "label": row.get("name") or row["modelId"]}
            for row in self._catalog
            if isinstance(row, Mapping) and isinstance(row.get("modelId"), str)
        ]

    def commands(self, session_id: str) -> list[dict[str, str | None]]:
        result = self._native_control(session_id, "session.commands", {})
        items = result.get("items")
        if not isinstance(items, list):
            raise ConnectionError("小内核返回了无效的 Grok Build 命令目录。", 502)
        return items

    def model(self, session_id: str) -> dict[str, Any]:
        summary = self._scope(session_id)
        return {
            "provider": "grok",
            "model": self._model_id(summary.get("current_model_id")),
            "effort": summary.get("reasoning_effort"),
        }

    def current_effort(self, session_id: str) -> str | None:
        return self._scope(session_id).get("reasoning_effort")

    def set_model(self, session_id: str, provider: str, model: str) -> dict[str, Any]:
        if provider != "grok" or not any(row["model"] == model for row in self.models(session_id)):
            raise ConnectionError("所选模型不在 Grok Build 原生目录中。", 422)
        result = self._native_control(
            session_id, "session.model.set", {"provider": provider, "model": model}
        )
        self._scope(session_id)["current_model_id"] = model
        return {"provider": result.get("provider"), "model": result.get("model")}

    def set_effort(self, session_id: str, effort: str) -> dict[str, Any]:
        result = self._native_control(
            session_id, "session.reasoning.set", {"effort": effort}
        )
        self._scope(session_id)["reasoning_effort"] = effort
        return {"effort": result.get("effort")}

    def open_ids(self) -> list[str]:
        return list(self._attached)

    def mutate_session(self, session_id: str, updates: dict[str, Any] | None) -> None:
        if updates is None:
            with self._lock:
                self._attached.discard(session_id)
                self._summaries.pop(session_id, None)
            if self.session_deleter is not None:
                try:
                    self.session_deleter(session_id)
                except Exception:
                    pass
            elif not self.connection_id:
                root = Path.home() / ".grok" / "sessions"
                if root.is_dir():
                    import shutil
                    for path in root.rglob(session_id):
                        if path.is_dir() and path.resolve().is_relative_to(root.resolve()):
                            shutil.rmtree(path, ignore_errors=True)
            return
        if "title" in updates:
            title = updates["title"]
            if not isinstance(title, str) or not title.strip():
                raise ConnectionError("会话标题不能为空。", 422)
            with self._lock:
                if session_id in self._summaries:
                    self._summaries[session_id]["title"] = title
                    self._summaries[session_id]["session_summary"] = title

    def messages(self, session_id: str, before: str | None = None, limit: int = 50) -> dict[str, Any]:
        self._scope(session_id)
        if before is None:
            self._sync_session_history(session_id)
        items, next_cursor = self.store.list_messages(self.agent_id, session_id, before, limit)
        return {"items": items, "next_cursor": next_cursor}

    def _sync_session_history(self, session_id: str) -> None:
        frames = self.session_reader(session_id)
        messages: dict[str, dict[str, Any]] = {}
        for frame in frames:
            params = frame.get("params") if isinstance(frame, Mapping) else None
            update = params.get("update") if isinstance(params, Mapping) else None
            if not isinstance(update, Mapping):
                continue
            message = GrokProjection._message(self.agent_id, session_id, update, frame)
            if message is None:
                continue
            previous = messages.get(message["id"])
            if previous:
                if update.get("sessionUpdate") in {
                    "user_message_chunk", "agent_message_chunk", "agent_thought_chunk"
                }:
                    message["text"] = previous["text"] + message["text"]
                elif update.get("sessionUpdate") in {"tool_call", "tool_call_update"}:
                    prev_tool = previous.get("tool") or {}
                    cur_tool = message.get("tool") or {}
                    if cur_tool.get("name") in {"Grok tool", "", None} and prev_tool.get("name"):
                        cur_tool["name"] = prev_tool["name"]
                    if not cur_tool.get("arguments") and prev_tool.get("arguments"):
                        cur_tool["arguments"] = prev_tool["arguments"]
                    message["tool"] = cur_tool
                    if not message.get("text") and previous.get("text"):
                        message["text"] = previous["text"]
            messages[message["id"]] = message
        for message in messages.values():
            self.store.upsert_message(message)

    def load_native_history_page(
        self, session_id: str, before: str | None, limit: int
    ) -> dict[str, Any]:
        return self.messages(session_id, before, limit)

    async def _attach(self, session_id: str) -> None:
        if session_id in self._attached:
            return
        summary = self._scope(session_id)
        await self.bridge.request_control(
            "session.spawn",
            {
                "session_id": session_id,
                "agent_type": self._agent_type(),
                "cwd": (summary.get("info") or {}).get("cwd") or summary.get("workspace"),
                "params": self._runtime_params(),
            },
        )
        self._attached.add(session_id)

    def _native_control(
        self, session_id: str, action: str, fields: Mapping[str, Any]
    ) -> dict[str, Any]:
        async def send() -> Mapping[str, Any]:
            await self._attach(session_id)
            return await self.bridge.request_control(
                action, {"session_id": session_id, **dict(fields)}
            )

        try:
            response = asyncio.run(send())
        except (DaemonBridgeError, RuntimeError) as exc:
            raise ConnectionError("小内核未确认 Grok Build 原生控制。", 503) from exc
        result = response.get("result")
        if not isinstance(result, Mapping):
            raise ConnectionError("小内核返回了无效的 Grok Build 原生控制结果。", 502)
        return dict(result)

    def _record_sessions(self, summaries: list[dict[str, Any]]) -> None:
        rows = []
        for summary in summaries:
            try:
                row = self._session_row(summary)
            except (KeyError, TypeError, ValueError):
                continue
            self._summaries[row["id"]] = summary
            rows.append(row)
        self.service.record_native_sessions(rows)

    def _session_row(self, summary: Mapping[str, Any]) -> dict[str, Any]:
        info = summary.get("info")
        session_id = info.get("id") if isinstance(info, Mapping) else None
        if not isinstance(session_id, str) or not session_id:
            raise ValueError("invalid Grok session")
        title = summary.get("generated_title") or summary.get("session_summary") or session_id
        return {
            "id": session_id,
            "agent_id": self.agent_id,
            "source_id": self.agent_id,
            "connection_id": self.connection_id,
            "source_session_id": session_id,
            "title": str(title),
            "workspace": info.get("cwd") if isinstance(info, Mapping) else None,
            "status": "idle",
            "updated_at": summary.get("updated_at") or _now(),
            "history_state": "available",
            "control_state": "owned",
        }

    def _scope(self, session_id: str) -> dict[str, Any]:
        summary = self._summaries.get(session_id)
        if summary is None:
            rows = self.session_reader(None)
            self._record_sessions(rows)
            summary = self._summaries.get(session_id)
        if summary is None:
            raise ConnectionError("Grok Build 会话不存在。", 404)
        return summary

    def _agent_type(self) -> str:
        return "grok-ssh" if self.connection_id else "grok"

    def _model_id(self, value: Any) -> Any:
        return next(
            (
                row.get("modelId")
                for row in self._catalog
                if isinstance(row, Mapping)
                and value in {row.get("modelId"), row.get("name")}
            ),
            value,
        )

    def _runtime_params(self) -> dict[str, Any]:
        if not self.connection_id:
            return {}
        return {
            "connection_id": self.connection_id,
            "ssh_settings": {**self.ssh_settings, "grok_executable": self.executable},
        }

    def _control(self, action: str, fields: Mapping[str, Any], detail: str) -> dict[str, Any]:
        try:
            return asyncio.run(self.bridge.request_control(action, fields))
        except (DaemonBridgeError, RuntimeError) as exc:
            raise ConnectionError(detail, 503) from exc


def local_grok_sessions(session_id: str | None = None) -> list[dict[str, Any]]:
    root = Path.home() / ".grok" / "sessions"
    if session_id is None:
        result = []
        for path in root.rglob("summary.json") if root.is_dir() else ():
            try:
                result.append(json.loads(path.read_text(encoding="utf-8")))
            except (OSError, ValueError):
                continue
        return result
    for path in root.rglob(f"{session_id}/updates.jsonl") if root.is_dir() else ():
        frames = []
        for line in path.read_text(encoding="utf-8", errors="replace").splitlines():
            try:
                frames.append(json.loads(line))
            except ValueError:
                continue
        return frames
    return []
