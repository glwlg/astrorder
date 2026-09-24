"""Daemon-owned Grok Build ACP sessions."""
from __future__ import annotations

import asyncio
import threading
from collections.abc import Callable, Mapping
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from astrorder_codex_connector.app_server import CodexAppServer
from astrorder_codex_connector.config import CodexConnectorConfig

from astrorder.daemon.errors import DaemonProtocolError


@dataclass(frozen=True)
class GrokDaemonRuntimeConfig:
    executable: str
    workspace: Path
    allowed_workspaces: tuple[Path, ...]
    agent_id: str = "local-grok"
    agent_name: str = "Grok Build"

    def __post_init__(self) -> None:
        executable = Path(self.executable).expanduser().resolve()
        workspace = self.workspace.expanduser().resolve()
        roots = tuple(path.expanduser().resolve() for path in self.allowed_workspaces)
        if not executable.is_file() or not workspace.is_dir() or not roots:
            raise ValueError("Grok executable, workspace and allowed workspaces are required")
        object.__setattr__(self, "executable", str(executable))
        object.__setattr__(self, "workspace", workspace)
        object.__setattr__(self, "allowed_workspaces", roots)


@dataclass
class _OwnedGrokSession:
    client: Any
    workspace: Path
    status: str = "idle"
    prompt_task: asyncio.Task[None] | None = None
    models: list[dict[str, Any]] | None = None
    model: str | None = None
    effort: str | None = None
    commands: list[dict[str, Any]] | None = None
    error: str | None = None


class GrokDaemonRuntime:
    def __init__(
        self,
        config: GrokDaemonRuntimeConfig,
        *,
        emit: Callable[..., Any],
        client_factory=CodexAppServer,
    ) -> None:
        self.config = config
        self.emit = emit
        self.client_factory = client_factory
        self._sessions: dict[str, _OwnedGrokSession] = {}
        self._lock = threading.RLock()

    def set_emitter(self, emit: Callable[..., Any]) -> None:
        self.emit = emit

    def status(self) -> Mapping[str, Any]:
        return {"available": True, "executable": self.config.executable}

    async def query(self, request: Mapping[str, Any]) -> Mapping[str, Any]:
        if request.get("method") != "initialize":
            raise DaemonProtocolError("Grok runtime request is unsupported")
        client = self._client(self.config.workspace, lambda _frame: None)
        try:
            await asyncio.to_thread(client.start)
            return await asyncio.to_thread(client.request, "initialize", self._initialize_params())
        finally:
            await asyncio.to_thread(client.stop)

    async def spawn(self, request: Mapping[str, Any]) -> Mapping[str, Any]:
        session_id = self._session_id(request)
        workspace = self._workspace(request.get("cwd"))
        return await self._open(session_id, workspace, load=True)

    async def create(self, request: Mapping[str, Any]) -> Mapping[str, Any]:
        workspace = self._workspace(request.get("cwd"))
        client = self._client(workspace, lambda _frame: None)
        try:
            await asyncio.to_thread(client.start)
            initialized = await asyncio.to_thread(
                client.request, "initialize", self._initialize_params()
            )
            result = await asyncio.to_thread(
                client.request, "session/new", {"cwd": str(workspace), "mcpServers": []}
            )
            session_id = result.get("sessionId")
            if not isinstance(session_id, str) or not session_id:
                raise DaemonProtocolError("Grok did not confirm a session ID")
            await asyncio.to_thread(client.stop)
            created = await self._open(session_id, workspace, load=True)
            return {
                **created,
                "session_id": session_id,
                "model": self._current_model(initialized),
            }
        except Exception:
            await asyncio.to_thread(client.stop)
            raise

    async def command(self, action: str, request: Mapping[str, Any]) -> Mapping[str, Any]:
        session_id = self._session_id(request)
        owned = self._owned(session_id)
        if action == "session.send":
            prompt = request.get("prompt")
            attachments = request.get("attachments")
            if not isinstance(prompt, str) and not attachments:
                raise DaemonProtocolError("Grok prompt must be non-empty")
            prompt_str = prompt if isinstance(prompt, str) else ""
            if owned.prompt_task and not owned.prompt_task.done():
                raise DaemonProtocolError("Grok session already has an active turn")
            command_id = request.get("command_id")
            if not isinstance(command_id, str) or not command_id:
                raise DaemonProtocolError("Grok command ID is required")
            owned.status = "running"
            owned.error = None
            owned.prompt_task = asyncio.create_task(
                self._prompt(session_id, owned, command_id, prompt_str, attachments=attachments)
            )
            return {"status": "running", "accepted": True}
        if action == "session.interrupt":
            await asyncio.to_thread(
                owned.client.send,
                {"jsonrpc": "2.0", "method": "session/cancel", "params": {"sessionId": session_id}},
            )
            return {"status": "idle", "interrupted": True}
        if action == "session.models":
            return {"items": list(owned.models or [])}
        if action == "session.commands":
            return {"items": list(owned.commands or [])}
        if action == "session.model.read":
            return {"provider": "grok", "model": owned.model, "effort": owned.effort}
        if action == "session.model.set":
            model = request.get("model")
            if not isinstance(model, str) or model not in {
                row.get("model") for row in (owned.models or [])
            }:
                raise DaemonProtocolError("Grok model is unavailable")
            await asyncio.to_thread(
                owned.client.request,
                "session/set_model",
                {"sessionId": session_id, "modelId": model},
            )
            owned.model = model
            return {"provider": "grok", "model": model}
        if action == "session.reasoning.set":
            effort = request.get("effort")
            choices = next(
                (
                    row.get("efforts", [])
                    for row in (owned.models or [])
                    if row.get("model") == owned.model
                ),
                [],
            )
            if not isinstance(effort, str) or effort not in choices:
                raise DaemonProtocolError("Grok reasoning effort is unavailable")
            await asyncio.to_thread(
                owned.client.request,
                "session/set_config_option",
                {"sessionId": session_id, "configId": "reasoning_effort", "value": effort},
            )
            owned.effort = effort
            return {"effort": effort}
        raise DaemonProtocolError("Grok runtime action is unsupported")

    async def shutdown(self) -> None:
        with self._lock:
            owned = list(self._sessions.values())
            self._sessions.clear()
        for item in owned:
            if item.prompt_task:
                item.prompt_task.cancel()
        await asyncio.gather(
            *(asyncio.to_thread(item.client.stop) for item in owned),
            return_exceptions=True,
        )

    async def reload_config(self) -> None:
        """Grok reads config when opening each session and has no shared catalog client."""

    async def _open(self, session_id: str, workspace: Path, *, load: bool) -> dict[str, Any]:
        with self._lock:
            current = self._sessions.get(session_id)
        if current is not None:
            return {"status": current.status, "model": None}
        loop = asyncio.get_running_loop()
        commands: list[dict[str, Any]] = []

        def notification(frame: Mapping[str, Any]) -> None:
            update = (frame.get("params") or {}).get("update")
            if isinstance(update, Mapping) and update.get("sessionUpdate") == "available_commands_update":
                commands[:] = self._commands(update)
            if (
                isinstance(update, Mapping)
                and update.get("sessionUpdate") == "retry_state"
                and update.get("type") == "failed"
                and isinstance(update.get("message"), str)
            ):
                with self._lock:
                    current = self._sessions.get(session_id)
                    if current is not None:
                        current.error = update["message"]
            asyncio.run_coroutine_threadsafe(
                self.emit(
                    session_id,
                    "grok.notification",
                    {"agent_id": self.config.agent_id, "frame": frame},
                    status=None,
                ),
                loop,
            )

        client = self._client(
            workspace,
            notification,
        )
        try:
            await asyncio.to_thread(client.start)
            initialized = await asyncio.to_thread(
                client.request, "initialize", self._initialize_params()
            )
            loaded: Mapping[str, Any] = {}
            if load:
                loaded = await asyncio.to_thread(
                    client.request,
                    "session/load",
                    {"sessionId": session_id, "cwd": str(workspace), "mcpServers": []},
                )
                if loaded.get("sessionId", session_id) != session_id:
                    raise DaemonProtocolError("Grok loaded a different session")
        except Exception:
            await asyncio.to_thread(client.stop)
            raise
        with self._lock:
            models = self._models(initialized)
            model, effort = self._loaded_settings(loaded, self._current_model(initialized))
            model = next(
                (
                    row["model"]
                    for row in models
                    if model in {row.get("model"), row.get("label")}
                ),
                model,
            )
            self._sessions[session_id] = _OwnedGrokSession(
                client, workspace, models=models, model=model, effort=effort, commands=commands
            )
        return {"status": "idle", "model": model, "effort": effort}

    async def _prompt(
        self,
        session_id: str,
        owned: _OwnedGrokSession,
        command_id: str,
        prompt: str,
        attachments: list[dict[str, Any]] | None = None,
    ) -> None:
        status = "idle"
        payload: dict[str, Any]
        prompt_blocks: list[dict[str, Any]] = []
        if prompt:
            prompt_blocks.append({"type": "text", "text": prompt})
        for att in attachments or []:
            media_type = str(att.get("media_type") or "")
            if media_type.startswith("image/") and att.get("content_base64"):
                prompt_blocks.append({
                    "type": "image",
                    "data": att["content_base64"],
                    "mimeType": media_type,
                })
            elif att.get("name") or att.get("text"):
                name = att.get("name", "attachment")
                text_content = att.get("text") or ""
                prompt_blocks.append({
                    "type": "text",
                    "text": f"\n\n[Attachment: {name}]\n{text_content}".strip(),
                })
        if not prompt_blocks:
            prompt_blocks.append({"type": "text", "text": ""})
        try:
            result = await asyncio.to_thread(
                owned.client.request,
                "session/prompt",
                {"sessionId": session_id, "prompt": prompt_blocks},
                3600,
            )
            payload = {"command_id": command_id, "result": result}
        except asyncio.CancelledError:
            status, payload = "idle", {"command_id": command_id, "cancelled": True}
        except Exception as exc:  # noqa: BLE001 - background turn failures must reach the WAL
            status, payload = "error", {
                "command_id": command_id,
                "error": (owned.error or str(exc))[:1000],
            }
        owned.status = status
        await self.emit(
            session_id,
            "grok.completed",
            {"agent_id": self.config.agent_id, **payload},
            status=status,
        )

    def _client(self, workspace: Path, notification) -> Any:
        config = CodexConnectorConfig(
            endpoint="",
            secret="",
            agent_id=self.config.agent_id,
            agent_name=self.config.agent_name,
            executable=self.config.executable,
            workspace=workspace,
            allowed_workspaces=self.config.allowed_workspaces,
        )
        return self.client_factory(
            config,
            notification,
            launch_argv=[self.config.executable, "agent", "stdio"],
            request_name="Grok",
        )

    @staticmethod
    def _initialize_params() -> dict[str, Any]:
        return {"protocolVersion": 1, "clientCapabilities": {}}

    @staticmethod
    def _current_model(initialized: Mapping[str, Any]) -> str | None:
        meta = initialized.get("_meta")
        state = meta.get("modelState") if isinstance(meta, Mapping) else None
        model = state.get("currentModelId") if isinstance(state, Mapping) else None
        return model if isinstance(model, str) else None

    @staticmethod
    def _models(initialized: Mapping[str, Any]) -> list[dict[str, Any]]:
        meta = initialized.get("_meta")
        state = meta.get("modelState") if isinstance(meta, Mapping) else None
        rows = state.get("availableModels") if isinstance(state, Mapping) else None
        return [
            {
                "provider": "grok",
                "model": row["modelId"],
                "label": row.get("name") or row["modelId"],
                "efforts": [
                    effort["value"]
                    for effort in ((row.get("_meta") or {}).get("reasoningEfforts") or [])
                    if isinstance(effort, Mapping) and isinstance(effort.get("value"), str)
                ],
            }
            for row in (rows or [])
            if isinstance(row, Mapping) and isinstance(row.get("modelId"), str)
        ]

    @staticmethod
    def _loaded_settings(
        loaded: Mapping[str, Any], default_model: str | None
    ) -> tuple[str | None, str | None]:
        options = loaded.get("configOptions")
        values = {
            row.get("id"): row.get("currentValue")
            for row in (options or [])
            if isinstance(row, Mapping)
        }
        models = loaded.get("models")
        loaded_model = models.get("currentModelId") if isinstance(models, Mapping) else None
        model = values.get("model") or loaded_model or default_model
        effort = values.get("reasoning_effort")
        return (
            model if isinstance(model, str) else None,
            effort if isinstance(effort, str) else None,
        )

    @staticmethod
    def _commands(update: Mapping[str, Any]) -> list[dict[str, Any]]:
        rows = update.get("availableCommands") or update.get("available_commands") or []
        result = []
        for row in rows:
            if not isinstance(row, Mapping) or not isinstance(row.get("name"), str):
                continue
            input_spec = row.get("input")
            hint = input_spec.get("hint") if isinstance(input_spec, Mapping) else None
            result.append({
                "name": row["name"].lstrip("/"),
                "description": str(row.get("description") or ""),
                "input_hint": hint if isinstance(hint, str) and hint else None,
            })
        return result

    def _workspace(self, raw: Any) -> Path:
        path = Path(raw or self.config.workspace).expanduser().resolve()
        if not path.is_dir() or not any(
            path == root or root in path.parents for root in self.config.allowed_workspaces
        ):
            raise DaemonProtocolError("Grok workspace is not allowlisted")
        return path

    @staticmethod
    def _session_id(request: Mapping[str, Any]) -> str:
        value = request.get("session_id")
        if not isinstance(value, str) or not value:
            raise DaemonProtocolError("Grok session ID is required")
        return value

    def _owned(self, session_id: str) -> _OwnedGrokSession:
        with self._lock:
            owned = self._sessions.get(session_id)
        if owned is None:
            raise DaemonProtocolError("Grok session is not daemon-owned")
        return owned
