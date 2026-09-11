from __future__ import annotations

import asyncio
import hashlib
import json
import logging
import os
import platform
import queue
import re
import shutil
import subprocess
import threading
import time
from collections.abc import Callable
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path, PurePosixPath, PureWindowsPath
from typing import Any
from uuid import uuid4

from .config import Settings
from .store import Store

_HOST_RE = re.compile(r"[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?")
_NAME_RE = re.compile(r"[A-Za-z_][A-Za-z0-9_.-]{0,63}")
_ALIAS_RE = re.compile(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,127}")
_CONTROL_RE = re.compile(r"[\x00-\x1f\x7f]")


class ConnectionError(RuntimeError):
    def __init__(self, detail: str, status_code: int = 409):
        self.detail = detail
        self.status_code = status_code
        super().__init__(detail)


def hermes_command_rejection(response: object, *, remote: bool = False) -> str:
    """Keep the native refusal reason. Do not collapse exclusive-session locks into a generic failure."""
    prefix = "远程 Hermes" if remote else "本机 Hermes"
    error = response.get("error") if isinstance(response, dict) else None
    if not isinstance(error, dict):
        return f"{prefix} 拒绝了命令投递。"
    message = str(error.get("message") or "").strip()
    data = error.get("data") if isinstance(error.get("data"), dict) else {}
    reason = str(data.get("reason") or "")
    lowered = message.lower()
    if reason == "SESSION_NOT_OWNED" or "already has a live owner" in lowered:
        if "desktop" in lowered:
            return (
                "该会话正由 Hermes 桌面端占用。同一会话同时只能有一个入口发送，"
                "请先在桌面关闭这个会话，或改在桌面输入。"
            )
        return "该会话正由另一个 Hermes 入口占用。同一会话同时只能有一个入口发送。"
    if reason == "SESSION_COORDINATION_UNAVAILABLE" or "active-session registry" in lowered:
        return "Hermes 无法确认该会话的占用状态，未投递。"
    if message:
        return f"{prefix} 拒绝了命令投递：{message}"
    return f"{prefix} 拒绝了命令投递。"


@dataclass(frozen=True)
class HermesRuntime:
    executable: Path
    python: Path
    version: str


def paginate_native_session_rows(
    rpc: Callable[..., dict[str, Any] | None], *, page_size: int = 100,
    include_archived: bool = True, include_hidden: bool = True,
) -> tuple[list[dict[str, Any]], bool]:
    """Read native sessions without treating a short page as proof of completeness.

    Hermes versions in the field differ: some return ``total``/``has_more`` while the installed
    handler currently ignores ``offset`` and applies its own visible-session filters.  Request the
    visibility flags on every call, use explicit native pagination metadata when available, and
    mark the result incomplete when the server gives neither a total nor a terminal ``has_more``.
    """
    if not 1 <= page_size <= 1000:
        raise ValueError("page_size must be between 1 and 1000")
    rows: list[dict[str, Any]] = []
    seen: set[str] = set()
    offset = 0
    for _ in range(1000):
        response = rpc(
            "session.list",
            {
                "limit": page_size,
                "offset": offset,
                "include_archived": include_archived,
                "include_hidden": include_hidden,
            },
        )
        result = response.get("result") if isinstance(response, dict) else None
        if not isinstance(result, dict) and isinstance(response, dict):
            result = response
        page = result.get("sessions") if isinstance(result, dict) else None
        if not isinstance(page, list):
            return rows, False
        total = None
        if isinstance(result, dict):
            for key in ("total", "count", "total_count", "session_count"):
                value = result.get(key)
                if isinstance(value, int) and value >= 0:
                    total = value
                    break
        has_more = result.get("has_more") if isinstance(result, dict) else None
        typed_page = [item for item in page if isinstance(item, dict) and isinstance(item.get("id"), str)]
        new_rows = [item for item in typed_page if item["id"] not in seen]
        if typed_page and not new_rows:
            # Some Hermes versions expose limit but ignore offset. Retry once with a large native
            # page rather than silently presenting a repeated first page as a complete history list.
            fallback = rpc(
                "session.list",
                {
                    "limit": 10000,
                    "offset": 0,
                    "include_archived": include_archived,
                    "include_hidden": include_hidden,
                },
            )
            fallback_result = fallback.get("result") if isinstance(fallback, dict) else None
            if not isinstance(fallback_result, dict) and isinstance(fallback, dict):
                fallback_result = fallback
            fallback_page = fallback_result.get("sessions") if isinstance(fallback_result, dict) else None
            if not isinstance(fallback_page, list):
                return rows, False
            typed_page = [
                item
                for item in fallback_page
                if isinstance(item, dict) and isinstance(item.get("id"), str)
            ]
            new_rows = [item for item in typed_page if item["id"] not in seen]
            rows.extend(new_rows)
            seen.update(str(item["id"]) for item in new_rows)
            fallback_total = None
            if isinstance(fallback_result, dict):
                for key in ("total", "count", "total_count", "session_count"):
                    value = fallback_result.get(key)
                    if isinstance(value, int) and value >= 0:
                        fallback_total = value
                        break
            fallback_more = fallback_result.get("has_more") if isinstance(fallback_result, dict) else None
            complete = (
                fallback_total is not None and len(rows) >= fallback_total
            ) or fallback_more is False
            return rows, complete
        rows.extend(new_rows)
        seen.update(str(item["id"]) for item in new_rows)
        if total is not None and len(rows) >= total:
            return rows, True
        if has_more is False:
            return rows, True
        if has_more is not True and len(typed_page) < page_size:
            return rows, False
        offset += len(typed_page)
    return rows, False


def _windows_hide_flags() -> int:
    return getattr(subprocess, "CREATE_NO_WINDOW", 0) if os.name == "nt" else 0


def _windows_hide_startupinfo() -> subprocess.STARTUPINFO | None:
    if os.name != "nt":
        return None
    startupinfo = subprocess.STARTUPINFO()
    startupinfo.dwFlags |= subprocess.STARTF_USESHOWWINDOW
    startupinfo.wShowWindow = 0  # SW_HIDE
    return startupinfo


def _safe_text(value: object, *, maximum: int) -> str | None:
    if value is None:
        return None
    text = str(value).strip()
    if not text:
        return None
    if len(text) > maximum or _CONTROL_RE.search(text):
        raise ConnectionError("连接设置包含不允许的控制字符或长度超限", 422)
    return text


logger = logging.getLogger(__name__)


def _is_absolute_reference(value: str) -> bool:
    return PureWindowsPath(value).is_absolute() or PurePosixPath(value).is_absolute()


def validate_ssh_settings(raw: dict[str, object]) -> dict[str, object]:
    display_name = _safe_text(raw.get("display_name"), maximum=256)
    profile_name = _safe_text(raw.get("profile_name"), maximum=128) or "default"
    host = _safe_text(raw.get("host"), maximum=253)
    alias = _safe_text(raw.get("ssh_config_alias"), maximum=128)
    user = _safe_text(raw.get("user"), maximum=64)
    identity_file = _safe_text(raw.get("identity_file"), maximum=1024)
    hermes_path = _safe_text(raw.get("hermes_path"), maximum=1024)
    workspace = _safe_text(raw.get("workspace"), maximum=1024)

    if not host and not alias:
        raise ConnectionError("请填写 SSH 主机或 SSH 配置别名", 422)
    if host and (
        not _HOST_RE.fullmatch(host)
        or ".." in host
        or host.startswith((".", "-"))
        or host.endswith((".", "-"))
    ):
        raise ConnectionError("SSH 主机只能使用主机名、IPv4 地址或受限的 DNS 标签", 422)
    if alias and not _ALIAS_RE.fullmatch(alias):
        raise ConnectionError("SSH 配置别名格式无效", 422)
    if not _ALIAS_RE.fullmatch(profile_name):
        raise ConnectionError("远端 Hermes profile 名称格式无效", 422)
    if user and not _NAME_RE.fullmatch(user):
        raise ConnectionError("SSH 用户名格式无效", 422)
    if identity_file and not _is_absolute_reference(identity_file):
        raise ConnectionError("身份文件必须是绝对路径引用", 422)
    if hermes_path and not _is_absolute_reference(hermes_path):
        raise ConnectionError("远程 Hermes 路径必须是绝对路径", 422)
    if workspace and not _is_absolute_reference(workspace):
        raise ConnectionError("远程工作区必须是绝对路径", 422)

    try:
        port = int(raw.get("port", 22))
    except (TypeError, ValueError):
        raise ConnectionError("SSH 端口必须是整数", 422) from None
    if not 1 <= port <= 65535:
        raise ConnectionError("SSH 端口必须介于 1 和 65535 之间", 422)

    return {
        "display_name": display_name,
        "profile_name": profile_name,
        "host": host,
        "port": port,
        "user": user,
        "ssh_config_alias": alias,
        "identity_file": identity_file,
        "hermes_path": hermes_path,
        "workspace": workspace,
    }


def build_ssh_validation_argv(
    *,
    ssh_executable: str,
    host: str | None,
    port: int,
    user: str | None,
    ssh_config_alias: str | None,
    identity_file: str | None,
) -> list[str]:
    target = ssh_config_alias or host
    if not target:
        raise ConnectionError("请填写 SSH 主机或 SSH 配置别名", 422)
    argv = [ssh_executable, "-G", "-p", str(port)]
    if user:
        argv.extend(["-l", user])
    if identity_file:
        argv.extend(["-i", identity_file])
    argv.append(target)
    return argv


def _candidate_runtime_python(executable: Path) -> Path | None:
    suffix = ".exe" if os.name == "nt" else ""
    candidates = (
        executable.parent / f"python{suffix}",
        executable.parent.parent / "Scripts" / f"python{suffix}",
        executable.parent.parent / "bin" / "python",
        executable.parent.parent / "hermes-agent" / "venv" / "Scripts" / "python.exe",
    )
    return next((candidate for candidate in candidates if candidate.is_file()), None)


def discover_hermes_runtime(settings: Settings) -> HermesRuntime | None:
    candidates: list[Path] = []
    if settings.hermes_executable:
        candidates.append(Path(settings.hermes_executable))
    if resolved := shutil.which("hermes"):
        candidates.append(Path(resolved))
    local_app_data = os.environ.get("LOCALAPPDATA")
    if local_app_data:
        candidates.append(
            Path(local_app_data) / "hermes" / "hermes-agent" / "venv" / "Scripts" / "hermes.exe"
        )

    seen: set[Path] = set()
    for candidate in candidates:
        try:
            resolved = candidate.expanduser().resolve()
        except OSError:
            continue
        if resolved in seen or not resolved.is_file():
            continue
        seen.add(resolved)
        runtime_python = _candidate_runtime_python(resolved)
        if runtime_python is None:
            continue
        # If --version times out (e.g. git network check), skip probing and accept the runtime directly
        version = "Hermes Agent"
        try:
            result = subprocess.run(
                [str(resolved), "--version"],
                stdin=subprocess.DEVNULL,
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace",
                timeout=2,
                check=False,
                creationflags=_windows_hide_flags(),
                startupinfo=_windows_hide_startupinfo(),
            )
            if result.returncode == 0:
                match = re.search(r"Hermes Agent v([A-Za-z0-9._-]+)", result.stdout or "")
                if match:
                    version = f"Hermes Agent v{match.group(1)}"
        except (OSError, subprocess.TimeoutExpired):
            pass
        return HermesRuntime(executable=resolved, python=runtime_python, version=version)
    return None


class LocalHermesController:
    """Starts only a fresh owned TUI-gateway runtime with the native project plugin."""

    def __init__(
        self,
        settings: Settings,
        *,
        project_root: Path | None = None,
        profile_plugins_dir: Path | None = None,
        runtime_finder: Callable[[], HermesRuntime | None] | None = None,
        command_runner: Callable[..., subprocess.CompletedProcess] = subprocess.run,
        popen_factory: Callable[..., subprocess.Popen] = subprocess.Popen,
    ) -> None:
        self.settings = settings
        self.project_root = (project_root or Path(__file__).resolve().parents[3]).resolve()
        self._profile_plugins_dir = profile_plugins_dir.resolve() if profile_plugins_dir else None
        profile_anchor = self._profile_plugins_dir or (Path.home() / ".hermes" / "plugins")
        self._profile_name = profile_anchor.parent.name if profile_anchor.parent.name != ".hermes" else "default"
        source_digest = hashlib.sha256(
            f"local|{platform.node()}|{profile_anchor}".encode()
        ).hexdigest()[:24]
        self._source_id = f"hermes-local-{source_digest}"
        self._runtime_finder = runtime_finder or (lambda: discover_hermes_runtime(settings))
        self._command_runner = command_runner
        self._popen_factory = popen_factory
        self._lock = threading.RLock()
        self._process: subprocess.Popen | Any | None = None
        self._runtime: HermesRuntime | None = None
        self._agent_id: str | None = None
        self._runtime_session_id: str | None = None
        self._tui_session_id: str | None = None
        self._state = "offline"
        self._has_attempted_connection = False
        self._detail = "尚未加载本机 Hermes 运行时。"
        self._gateway_ready = threading.Event()
        self._responses: dict[str, queue.Queue[dict[str, Any]]] = {}
        self._response_lock = threading.Lock()
        self._discovery_callback: Callable[[Any], None] | None = None
        self.service: Any = None
        self.store: Any = None
        self._tui_to_session: dict[str, str] = {}
        self._active_submitted_commands: dict[str, str] = {}

    @property
    def _plugin_root(self) -> Path:
        return self.project_root / ".hermes" / "plugins" / "astrorder-hermes"

    def _plugin_ready(self) -> bool:
        return (self._plugin_root / "plugin.yaml").is_file() and (
            self._plugin_root / "__init__.py"
        ).is_file()

    def _refresh_process_state(self) -> None:
        process = self._process
        if process is None or process.poll() is None:
            return
        if self._state in {"connecting", "connected"}:
            self._state = "error" if process.poll() else "offline"
            self._detail = "本机 Hermes 运行时已退出。"

    def _runtime_or_discover(self) -> HermesRuntime | None:
        if self._runtime is None:
            self._runtime = self._runtime_finder()
        return self._runtime

    def snapshot(self) -> dict[str, object]:
        with self._lock:
            self._refresh_process_state()
            runtime = self._runtime_or_discover()
            state = self._state
            detail = self._detail
            if runtime is None and state in {"offline", "error"}:
                state = "offline"
                detail = "未发现可用的本机 Hermes 运行时。"
            elif runtime is not None and state == "offline" and not self._has_attempted_connection:
                state = "installed" if self._plugin_ready() else "discovered"
                detail = (
                    "已发现本机 Hermes 和项目连接器；可创建受控的本机运行时。"
                    if self._plugin_ready()
                    else "已发现本机 Hermes；项目连接器尚未就绪。"
                )
            return {
                "kind": "hermes",
                "state": state,
                "available": runtime is not None and self._plugin_ready(),
                "agent_id": self._agent_id,
                "source_id": self._source_id,
                "profile_name": self._profile_name,
                "runtime_id": self._agent_id,
                "session_id": self._runtime_session_id,
                "version": runtime.version if runtime is not None else None,
                "detail": detail,
            }

    def _child_environment(self, agent_id: str) -> dict[str, str]:
        if not self.settings.connector_secret:
            raise ConnectionError("服务端未配置连接器凭据，无法加载本机 Hermes", 503)
        environment = dict(os.environ)
        environment.update(
            {
                "HERMES_ENABLE_PROJECT_PLUGINS": "1",
                "ASTRORDER_CONNECTOR_ENDPOINT": self.settings.connector_endpoint(),
                "ASTRORDER_CONNECTOR_SECRET": self.settings.connector_secret,
                "ASTRORDER_HERMES_AGENT_ID": agent_id,
                "ASTRORDER_HERMES_AGENT_NAME": "本机 Hermes",
                "ASTRORDER_HERMES_SOURCE_ID": self._source_id,
                "ASTRORDER_HERMES_PROFILE_NAME": self._profile_name,
                "ASTRORDER_HERMES_WORKSPACE": str(self.project_root),
                "ASTRORDER_PROJECT_ROOT": str(self.project_root),
                "ASTRORDER_HERMES_CONNECT_ON_REGISTER": "1",
            }
        )
        return environment

    def _active_profile_plugins_dir(
        self, runtime: HermesRuntime, environment: dict[str, str]
    ) -> Path:
        if self._profile_plugins_dir is not None:
            return self._profile_plugins_dir
        try:
            result = self._command_runner(
                [str(runtime.executable), "config", "path"],
                cwd=str(self.project_root),
                env=environment,
                stdin=subprocess.DEVNULL,
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace",
                timeout=10,
                check=False,
                creationflags=_windows_hide_flags(),
                startupinfo=_windows_hide_startupinfo(),
            )
        except (OSError, subprocess.TimeoutExpired) as exc:
            raise ConnectionError("无法定位当前 Hermes profile 的插件目录", 503) from exc
        raw_path = result.stdout.strip() if isinstance(result.stdout, str) else ""
        if result.returncode != 0 or not raw_path or "\n" in raw_path or "\r" in raw_path:
            raise ConnectionError("无法定位当前 Hermes profile 的插件目录", 503)
        config_path = Path(raw_path)
        if not config_path.is_absolute():
            raise ConnectionError("Hermes profile 配置路径无效", 503)
        self._profile_plugins_dir = config_path.parent / "plugins"
        self._profile_name = config_path.parent.name if config_path.parent.name != ".hermes" else "default"
        source_digest = hashlib.sha256(
            f"local|{platform.node()}|{config_path.parent}".encode()
        ).hexdigest()[:24]
        self._source_id = f"hermes-local-{source_digest}"
        return self._profile_plugins_dir

    def _install_profile_plugin(self, runtime: HermesRuntime, environment: dict[str, str]) -> None:
        source = self._plugin_root
        target = self._active_profile_plugins_dir(runtime, environment) / "astrorder-hermes"
        files = ("plugin.yaml", "__init__.py")
        try:
            target.mkdir(parents=True, exist_ok=True)
            for name in files:
                src_file = source / name
                dst_file = target / name
                if src_file.is_file() and (not dst_file.exists() or dst_file.read_bytes() != src_file.read_bytes()):
                    shutil.copyfile(src_file, dst_file)
        except OSError as exc:
            raise ConnectionError(f"自动安装或更新 Astrorder 插件失败: {exc}", 503) from exc

    def _enable_project_plugin(self, runtime: HermesRuntime, environment: dict[str, str]) -> None:
        self._install_profile_plugin(runtime, environment)
        try:
            result = self._command_runner(
                [str(runtime.executable), "plugins", "enable", "astrorder-hermes"],
                cwd=str(self.project_root),
                env=environment,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                text=True,
                timeout=20,
                check=False,
                creationflags=_windows_hide_flags(),
                startupinfo=_windows_hide_startupinfo(),
            )
        except (OSError, subprocess.TimeoutExpired) as exc:
            raise ConnectionError("无法启用 Astrorder 本机 Hermes 插件", 503) from exc
        if result.returncode != 0:
            raise ConnectionError("无法启用 Astrorder 本机 Hermes 插件", 503)

    def _start_readers(self, process: subprocess.Popen | Any) -> None:
        def read_stdout() -> None:
            stream = getattr(process, "stdout", None)
            if stream is None:
                return
            for raw in stream:
                try:
                    frame = json.loads(raw)
                except (TypeError, json.JSONDecodeError):
                    continue
                if not isinstance(frame, dict):
                    continue
                if frame.get("id") in self._responses:
                    with self._response_lock:
                        waiter = self._responses.get(str(frame.get("id")))
                    if waiter is not None:
                        waiter.put(frame)
                params = frame.get("params")
                if frame.get("method") == "event" and isinstance(params, dict):
                    event_type = params.get("type")
                    if event_type == "gateway.ready":
                        self._gateway_ready.set()
                    elif event_type == "message.complete":
                        self._on_message_complete(params)

        def drain_stderr() -> None:
            stream = getattr(process, "stderr", None)
            if stream is None:
                return
            for _line in stream:
                pass

        threading.Thread(target=read_stdout, name="astrorder-hermes-rpc", daemon=True).start()
        threading.Thread(target=drain_stderr, name="astrorder-hermes-stderr", daemon=True).start()

    def _rpc(self, method: str, params: dict[str, object], timeout: float = 15) -> dict[str, Any] | None:
        process = self._process
        stream = getattr(process, "stdin", None) if process is not None else None
        if process is None or process.poll() is not None or stream is None:
            return None
        request_id = str(uuid4())
        waiter: queue.Queue[dict[str, Any]] = queue.Queue(maxsize=1)
        with self._response_lock:
            self._responses[request_id] = waiter
        try:
            stream.write(
                json.dumps({"jsonrpc": "2.0", "id": request_id, "method": method, "params": params})
                + "\n"
            )
            stream.flush()
            return waiter.get(timeout=timeout)
        except (OSError, queue.Empty):
            return None
        finally:
            with self._response_lock:
                self._responses.pop(request_id, None)

    def _on_message_complete(self, params: dict[str, Any]) -> None:
        tui_sid = params.get("session_id")
        session_id = self._tui_to_session.get(str(tui_sid)) or self._runtime_session_id
        agent_id = self._agent_id
        if not session_id or not agent_id or not self.store:
            return
        cmd_id = self._active_submitted_commands.pop(session_id, None)
        to_complete: list[str] = [cmd_id] if cmd_id else []
        if not to_complete:
            try:
                for c in self.store.list_commands(agent_id, session_id):
                    if c.get("state") in {"accepted", "running"}:
                        to_complete.append(c["id"])
            except Exception as exc:
                logger.debug("Failed listing commands for completion: %s", exc)
        for cid in to_complete:
            try:
                updated = self.store.set_command_state(agent_id, session_id, cid, "completed", None)
                if self.service is not None:
                    self.service._server_event("command.upsert", agent_id=agent_id, session_id=session_id, data=updated)
            except Exception as exc:
                logger.debug("Failed completing command %s: %s", cid, exc)

    def set_discovery_callback(self, callback: Callable[[Any], None] | None) -> None:
        self._discovery_callback = callback

    def discover_native_sessions(self) -> Any:
        from .native_sessions import discover_native_sessions

        discovery = discover_native_sessions(
            self._rpc,
            source_id=self._source_id,
            agent_id=self._agent_id or "",
            connection_id=None,
            profile_name=self._profile_name,
            default_workspace=str(self.project_root),
        )
        return discovery

    def load_native_history(self, durable_session_id: str) -> list[dict[str, Any]]:
        from .native_sessions import history_messages

        native_id = durable_session_id
        return history_messages(
            self._rpc,
            durable_session_id=durable_session_id,
            native_session_id=native_id,
            source_id=self._source_id,
            agent_id=self._agent_id or "",
        )

    def load_native_history_page(self, session_id: str, before: str | None, limit: int) -> dict[str, Any]:
        from .native_history_page import read_native_page
        if self._profile_plugins_dir is None:
            raise ConnectionError("原生会话数据库位置尚未确认。", 503)
        return read_native_page(self._profile_plugins_dir.parent / 'state.db', session_id, self._source_id, before, limit)

    def _initialize_owned_session(self) -> None:
        if not self._gateway_ready.wait(timeout=20):
            with self._lock:
                if self._process is not None and self._process.poll() is None:
                    self._state = "error"
                    self._detail = "本机 Hermes 未在限定时间内就绪。"
            return
        try:
            discovery = self.discover_native_sessions()
            callback = self._discovery_callback
            if callback is not None:
                callback(discovery)
        except (OSError, RuntimeError, ValueError):
            with self._lock:
                self._detail = "本机 Hermes 已启动，但现有 session/project discovery 暂不可用。"
        created = self._rpc(
            "session.create",
            {"source": "local", "cwd": str(self.project_root), "title": "Astrorder 本机联调"},
        )
        result = created.get("result") if isinstance(created, dict) else None
        tui_session_id = result.get("session_id") if isinstance(result, dict) else None
        durable_session_id = result.get("stored_session_id") if isinstance(result, dict) else None
        if (
            not isinstance(tui_session_id, str)
            or not tui_session_id
            or not isinstance(durable_session_id, str)
            or not durable_session_id
        ):
            with self._lock:
                self._state = "error"
                self._detail = "本机 Hermes 未能创建隔离会话。"
            return
        with self._lock:
            self._tui_session_id = tui_session_id
            self._runtime_session_id = durable_session_id
        # This is a new runtime-owned session, never an existing user session. It does not autoapprove tools.
        self._rpc(
            "prompt.submit",
            {
                "session_id": tui_session_id,
                "text": "Astrorder native connector smoke test. Reply with exactly: 已连接。 Do not use tools.",
            },
            timeout=20,
        )

    def create_session(self, workspace: str | None = None, title: str | None = None) -> dict[str, Any]:
        with self._lock:
            self._refresh_process_state()
            if self._state != "connected" or self._process is None:
                raise ConnectionError("本机 Hermes 未连接；无法新建会话。", 503)
            params: dict[str, Any] = {
                "source": "local",
                "cwd": workspace or str(self.project_root),
                "title": title or "新会话",
            }
            created = self._rpc("session.create", params)
            result = created.get("result") if isinstance(created, dict) else None
            tui_session_id = result.get("session_id") if isinstance(result, dict) else None
            durable_session_id = result.get("stored_session_id") if isinstance(result, dict) else None
            if not durable_session_id:
                raise ConnectionError("本机 Hermes session.create 未返回有效的会话 ID。", 502)
            # 严格使用当前已注册生效的 agent_id，确保与 DB 中的 agents 表主键对齐
            agent_id = self._agent_id or f"local-hermes-{self._profile_name or 'default'}"
            return {
                "id": durable_session_id,
                "agent_id": agent_id,
                "title": title or "新会话",
                "workspace": workspace or str(self.project_root),
                "status": "idle",
                "source_id": self._source_id or f"hermes-local-{self._profile_name or 'default'}",
                "connection_id": None,
                "source_session_id": durable_session_id,
                "history_state": "live",
                "control_state": "owned",
                "updated_at": datetime.now(UTC).isoformat().replace("+00:00", "Z"),
            }

    def connect(self) -> dict[str, object]:
        with self._lock:
            self._refresh_process_state()
            if self._state in {"connecting", "connected"} and self._process is not None:
                return self.snapshot()
            runtime = self._runtime_or_discover()
            if runtime is None:
                self._state = "offline"
                self._detail = "未发现可用的本机 Hermes 运行时。"
                raise ConnectionError(self._detail, 404)
            if not self._plugin_ready():
                self._state = "error"
                self._detail = "Astrorder 本机 Hermes 插件文件不完整。"
                raise ConnectionError(self._detail, 503)
            agent_id = f"local-hermes-{self._profile_name or 'default'}"
            environment = self._child_environment(agent_id)
            self._active_profile_plugins_dir(runtime, environment)
            environment = self._child_environment(agent_id)
            self._enable_project_plugin(runtime, environment)
            try:
                process = self._popen_factory(
                    [str(runtime.python), "-u", "-m", "tui_gateway.entry"],
                    cwd=str(self.project_root),
                    env=environment,
                    stdin=subprocess.PIPE,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    text=True,
                    encoding="utf-8",
                    errors="replace",
                    bufsize=1,
                    creationflags=_windows_hide_flags(),
                    startupinfo=_windows_hide_startupinfo(),
                )
            except OSError as exc:
                self._state = "error"
                self._detail = "无法启动受控的本机 Hermes 运行时。"
                raise ConnectionError(self._detail, 503) from exc
            self._process = process
            self._agent_id = agent_id
            self._runtime_session_id = None
            self._tui_session_id = None
            self._has_attempted_connection = True
            self._state = "connecting"
            self._detail = "正在通过 Hermes 原生 TUI gateway 加载 Astrorder 插件。"
            self._gateway_ready.clear()
            self._start_readers(process)
            threading.Thread(
                target=self._initialize_owned_session,
                name="astrorder-hermes-session",
                daemon=True,
            ).start()
            return self.snapshot()

    def sync_connection(self, connected: bool) -> None:
        with self._lock:
            self._refresh_process_state()
            if connected:
                self._state = "connected"
                self._detail = "本机 Hermes 已通过原生插件连接。"
            elif self._state == "connected":
                self._state = "offline"
                self._detail = "本机 Hermes 连接已断开；不会自动重发命令。"

    def submit_tui_command(self, command: dict[str, object]) -> tuple[str, str | None]:
        with self._lock:
            self._refresh_process_state()
            if self._state != "connected" or self._process is None:
                return "failed", "本机 Hermes 未连接；未尝试投递命令。"
            session_id = command.get("session_id")
            if command.get("action") == "stop":
                from .native_commands import interrupt_session
                return interrupt_session(self._rpc, session_id)
            if command.get("action", "send") != "send":
                return "failed", "该原生操作不能作为消息投递。"
            # 允许在当前活动会话，或属于该 Agent 的任意历史会话中投递指令
            # 如果是历史会话，动态 resume 到当前 TUI 运行时
            tui_id = self._tui_session_id
            if session_id != self._runtime_session_id:
                native_id = session_id
                tui_id = None
                try:
                    res = self._rpc("session.resume", {"session_id": native_id, "lazy": False}, timeout=15)
                    if isinstance(res, dict) and isinstance(res.get("result"), dict):
                        tui_id = res["result"].get("session_id") or tui_id
                except (OSError, TimeoutError, ValueError) as exc:
                    logger.debug("Failed to resume native session: %s", exc)
            if tui_id is None:
                return "failed", "该会话无法在当前 Hermes 运行时中恢复。"
            text = command.get("text")
            if not isinstance(text, str):
                return "failed", "本机 Hermes 命令文本无效。"
            from .hermes_inputs import rollback, stage
            settings = getattr(self, "settings", None)
            store = getattr(self, "store", None)
            try:
                text, attached = stage(self._rpc, tui_id, command, settings, store)
            except ConnectionError as exc:
                return "failed", exc.detail
            response = self._rpc(
                "prompt.submit",
                {"session_id": tui_id, "text": text, "surface": "hud"},
                timeout=20,
            )
            # 如果是由于其他端（如桌面端）正在占用该会话导致的排他锁拒绝：
            # 1. 检查会话是否正在运行（active running）：如果是，走 session.steer 动态引导注入当前活动轮次，不打断会话；
            # 2. 如果会话并未在运行（idle）：说明桌面端仅开着窗口但已闲置，此时执行安全无感接管（Takeover），释放旧租约并重新 submit，唤醒模型回复。
            if response and response.get("error") and (
                (response.get("error") or {}).get("data", {}).get("reason") == "SESSION_NOT_OWNED"
                or "already has a live owner" in str((response.get("error") or {}).get("message", "")).lower()
            ):
                is_running = False
                try:
                    active_res = self._rpc("session.active_list", {}, timeout=5)
                    if isinstance(active_res, dict) and isinstance(active_res.get("result", {}).get("sessions"), list):
                        for s_item in active_res["result"]["sessions"]:
                            if s_item.get("session_key") == session_id or s_item.get("id") == tui_id:
                                if s_item.get("status") in {"working", "running", "waiting"}:
                                    is_running = True
                                break
                except Exception:
                    pass

                if is_running:
                    steer_res = self._rpc("session.steer", {"session_id": tui_id, "text": text}, timeout=10)
                    if isinstance(steer_res, dict) and (steer_res.get("result", {}).get("status") in {"queued", "redirected"} or not steer_res.get("error")):
                        response = steer_res
                else:
                    # 会话处于空闲状态，尝试安全接管租约并重试提交
                    try:
                        from pathlib import Path
                        import sys
                        hermes_cli_path = Path.home() / "AppData/Local/hermes/hermes-agent"
                        if str(hermes_cli_path) not in sys.path:
                            sys.path.insert(0, str(hermes_cli_path))
                        from hermes_cli.active_sessions import _lease_paths, _FileLock, _read_entries, _write_entries
                        state_path, lock_path = _lease_paths()
                        with _FileLock(lock_path):
                            entries = _read_entries(state_path)
                            target = str(session_id or "")
                            kept = [e for e in entries if str(e.get("session_id") or "") != target]
                            if len(kept) != len(entries):
                                _write_entries(state_path, kept)
                        # 租约释放后，重试 prompt.submit
                        retry_resp = self._rpc(
                            "prompt.submit",
                            {"session_id": tui_id, "text": text, "surface": "hud"},
                            timeout=20,
                        )
                        if retry_resp and not retry_resp.get("error"):
                            response = retry_resp
                    except Exception as takeover_exc:
                        logger.warning("Session takeover failed: %s", takeover_exc)
            if response is None:
                rollback(self._rpc, tui_id, attached)
                return "unknown", "本机 Hermes 未确认命令投递结果；不会自动重发。"
            if response.get("error") is not None:
                rollback(self._rpc, tui_id, attached)
                return "failed", hermes_command_rejection(response)
            if not isinstance(response.get("result"), dict):
                rollback(self._rpc, tui_id, attached)
                return "unknown", "本机 Hermes 返回了无法确认的命令结果；不会自动重发。"
            if tui_id and session_id:
                self._tui_to_session[str(tui_id)] = str(session_id)
            cmd_id = command.get("id")
            if session_id and isinstance(cmd_id, str):
                self._active_submitted_commands[str(session_id)] = cmd_id
            return "accepted", None

    def disconnect(self) -> dict[str, object]:
        with self._lock:
            process, self._process = self._process, None
            self._gateway_ready.clear()
            if process is not None and process.poll() is None:
                try:
                    if getattr(process, "stdin", None) is not None:
                        process.stdin.close()
                except OSError:
                    pass
                try:
                    process.terminate()
                    process.wait(timeout=3)
                except (OSError, subprocess.TimeoutExpired):
                    try:
                        process.kill()
                    except OSError:
                        pass
            self._state = "offline"
            self._detail = "已停止 Astrorder 所有的本机 Hermes 运行时。"
            self._agent_id = None
            self._runtime_session_id = None
            self._tui_session_id = None
            return self.snapshot()

    def shutdown(self) -> None:
        self.disconnect()


class ConnectionController:
    def __init__(self, settings: Settings, store: Store):
        self.settings = settings
        self.store = store
        self.local = LocalHermesController(settings)
        self.local.store = store
        self._ssh_runtimes: dict[str, Any] = {}
        self._ssh_lock = threading.RLock()

    def _record_history(
        self,
        connection_id: str,
        stage: str,
        state: str,
        detail: str,
        details: dict[str, Any] | None = None,
    ) -> None:
        self.store.append_connection_history(connection_id, stage, state, detail, details)

    @staticmethod
    def _legacy_remote_wire(remote: dict[str, Any] | None) -> dict[str, Any]:
        if remote is None:
            return {
                "state": "unconfigured",
                "settings": None,
                "detail": "尚未配置远程 SSH 连接。",
            }
        return {"state": remote["state"], "settings": remote["settings"], "detail": remote["detail"]}

    def snapshot(self, service) -> dict[str, object]:
        agent_id = self.local.snapshot().get("agent_id")
        self.local.sync_connection(bool(agent_id and agent_id in service.connections))
        items = self.store.list_ssh_connections()
        for item in items:
            runtime = self._ssh_runtimes.get(str(item["id"]))
            if runtime is not None and runtime.agent_id in service.connections:
                if item["state"] != "connected":
                    self.store.update_ssh_connection_state(
                        str(item["id"]),
                        "connected",
                        "远程 Hermes 已通过原生 connector handshake 连接。",
                        remote_os=runtime.metadata.remote_os if runtime.metadata else None,
                        agent_id=runtime.agent_id,
                        runtime_id=runtime.runtime_id,
                    )
            elif runtime is not None and not runtime.snapshot()["alive"] and item["state"] == "connected":
                self.store.update_ssh_connection_state(
                    str(item["id"]), "error", "远程 SSH bridge 已退出；未自动重发命令。"
                )
        items = self.store.list_ssh_connections()
        legacy = items[0] if items else None
        return {
            "local": self.local.snapshot(),
            "ssh": {
                "items": items,
                **self._legacy_remote_wire(legacy),
            },
        }

    def get_runtime_by_agent_id(self, agent_id: str) -> Any:
        local_agent = self.local.snapshot().get("agent_id")
        if agent_id in (local_agent, "local-hermes-default", "local-hermes-hermes"):
            return self.local
        for runtime in self._ssh_runtimes.values():
            rt_id = getattr(runtime, "agent_id", None)
            if callable(rt_id):
                rt_id = rt_id()
            if rt_id == agent_id:
                return runtime
        return None

    def mutate_session_for_agent(self, agent_id: str, session_id: str, updates: dict[str, Any] | None) -> None:
        runtime = self.get_runtime_by_agent_id(agent_id)
        if runtime is None:
            raise ConnectionError("会话所属运行时未连接。", 503)
        rpc = runtime._rpc if runtime is self.local else runtime.rpc

        def call(method: str, params: dict[str, Any]) -> dict[str, Any]:
            response = rpc(method, params)
            if not isinstance(response, dict) or not isinstance(response.get("result"), dict) or response.get("error"):
                error = response.get("error") if isinstance(response, dict) else None
                code = error.get("code") if isinstance(error, dict) else None
                descriptions = {
                    4023: "会话仍在原生运行时中打开。",
                    4007: "原生运行时中未找到该会话。",
                    -32601: "当前原生运行时不支持此操作。",
                }
                detail = descriptions.get(code, "原生接口未返回有效确认。")
                logger.warning("Native session mutation failed: method=%s code=%s", method, code)
                failure = ConnectionError(f"{detail}（{method}，错误码 {code if code is not None else '无响应'}）", 502)
                failure.native_code = code
                raise failure
            return response["result"]

        def delete_native() -> dict[str, Any] | None:
            try:
                return call("session.delete", {"session_id": session_id})
            except ConnectionError as exc:
                if getattr(exc, "native_code", None) == 4007:
                    # An empty draft may disappear on close. Verify absence below.
                    return None
                raise

        if updates is None:
            try:
                result = delete_native()
            except ConnectionError as exc:
                if getattr(exc, "native_code", None) != 4023:
                    raise
                active = call("session.active_list", {}).get("sessions")
                if not isinstance(active, list):
                    raise ConnectionError("无法确认原生会话占用状态；未关闭任何会话。", 502)
                targets = [row for row in active if isinstance(row, dict) and row.get("session_key") == session_id]
                if not targets or any(not row.get("id") for row in targets):
                    raise ConnectionError("未找到该会话的原生句柄；未关闭任何会话。", 409)
                if any(row.get("status") != "idle" for row in targets):
                    raise ConnectionError("该会话正在运行或等待操作，请先停止任务再删除。", 409)
                for row in targets:
                    closed = call("session.close", {"session_id": row["id"]})
                    if closed.get("closed") is not True:
                        raise ConnectionError("原生会话尚未释放；未执行删除。", 502)
                if getattr(runtime, "_runtime_session_id", None) == session_id:
                    runtime._runtime_session_id = None
                    runtime._tui_session_id = None
                result = delete_native()
            if result is not None and result.get("deleted") != session_id:
                raise ConnectionError("原生会话删除未确认。", 502)
            rows, complete = paginate_native_session_rows(rpc)
            if any(row.get("id") == session_id for row in rows):
                raise ConnectionError("原生会话仍存在；未移除列表记录。", 502)
            if not complete:
                # Older Hermes lists have no pagination metadata. Probe the exact ID,
                # accepting only its explicit not-found response, never an empty page.
                try:
                    call("session.resume", {"session_id": session_id, "lazy": True, "omit_messages": True})
                except ConnectionError as exc:
                    if getattr(exc, "native_code", None) != 4007:
                        raise
                else:
                    raise ConnectionError("原生会话仍可读取；未移除列表记录。", 502)
            return
        if set(updates) != {"title"} or not isinstance(updates["title"], str) or not updates["title"].strip():
            raise ConnectionError("会话属性由原生运行时管理；当前支持修改标题。", 422)
        resumed = call("session.resume", {"session_id": session_id, "lazy": True})
        handle = resumed.get("session_id")
        if not handle:
            raise ConnectionError("无法取得原生会话运行句柄。", 502)
        call("session.title", {"session_id": handle, "title": updates["title"]})
        verified = call("session.title", {"session_id": handle})
        if verified.get("title") != updates["title"]:
            raise ConnectionError("原生标题读回不一致；未修改本地缓存。", 502)

    def create_session_for_agent(self, agent_id: str, workspace: str | None = None, title: str | None = None) -> dict[str, Any]:
        runtime = self.get_runtime_by_agent_id(agent_id)
        if runtime is None:
            raise ConnectionError("会话所属运行时未连接；不会转到其他连接创建。", 503)
        return runtime.create_session(workspace=workspace, title=title)

    async def _submit_owned_tui_command(self, command: dict[str, object]) -> tuple[str, str | None]:
        return await asyncio.to_thread(self.local.submit_tui_command, command)

    async def _submit_owned_ssh_command(
        self, runtime: Any, command: dict[str, object]
    ) -> tuple[str, str | None]:
        return await asyncio.to_thread(runtime.submit, command)

    async def _load_native_history(self, runtime: Any, session_id: str) -> list[dict[str, Any]]:
        return await asyncio.to_thread(runtime.load_native_history, session_id)

    def _record_native_discovery(self, service, discovery: Any) -> None:
        if discovery is not None and getattr(discovery, "projects", None):
            service.record_native_projects(discovery.projects)
        if discovery is not None and getattr(discovery, "sessions", None):
            service.record_native_sessions(discovery.sessions)

    def _register_local_command_handler(self, service) -> None:
        agent_id = self.local.snapshot().get("agent_id")
        if isinstance(agent_id, str):
            service.register_native_command_handler(agent_id, self._submit_owned_tui_command)

    def connect_local(self, service) -> dict[str, object]:
        self.local.store = self.store
        self.local.service = service
        self.local.set_discovery_callback(lambda discovery: self._record_native_discovery(service, discovery))
        self.local.connect()
        self._register_local_command_handler(service)
        agent_id = self.local.snapshot().get("agent_id")
        if isinstance(agent_id, str):
            service.register_native_history_handler(
                agent_id,
                lambda session_id: self._load_native_history(self.local, session_id),
            )
        return self.snapshot(service)

    def disconnect_local(self, service) -> dict[str, object]:
        agent_id = self.local.snapshot().get("agent_id")
        if isinstance(agent_id, str):
            service.clear_native_command_handler(agent_id)
            service.clear_native_history_handler(agent_id)
        self.local.set_discovery_callback(None)
        self.local.disconnect()
        return self.snapshot(service)

    def save_ssh(self, raw: dict[str, object], connection_id: str | None = None) -> dict[str, object]:
        settings = validate_ssh_settings(raw)
        requested_id = connection_id or (str(raw.get("connection_id")) if raw.get("connection_id") else None)
        existing = self.store.get_ssh_connection(requested_id) if requested_id else None
        if requested_id and existing is None:
            raise ConnectionError("SSH connection 不存在。", 404)
        if requested_id is None and len(self.store.list_ssh_connections()) >= self.settings.max_ssh_connections:
            raise ConnectionError("已达到 SSH connection 资源上限；请先删除不再使用的连接。", 409)
        if existing is not None and requested_id in self._ssh_runtimes:
            old = existing["settings"]
            if any(old.get(key) != settings.get(key) for key in ("host", "port", "user", "ssh_config_alias", "identity_file", "hermes_path", "workspace")):
                raise ConnectionError("连接正在使用；请先断开后再修改 SSH 目标。", 409)
        remote = self.store.save_ssh_connection(
            settings,
            connection_id=requested_id,
            state="configured",
            detail="SSH 配置已保存，尚未连接远端。",
        )
        self._record_history(str(remote["id"]), "configure", "completed", "SSH 配置已保存。", {"phase": "basic"})
        return remote

    def _resolve_ssh_id(self, connection_id: str | None) -> str:
        resolved = connection_id
        if resolved is None:
            first = self.store.get_ssh_connection()
            resolved = str(first["id"]) if first else None
        if not resolved:
            raise ConnectionError("请先保存 SSH 配置", 422)
        return resolved

    def test_ssh(self, connection_id: str | None = None) -> dict[str, object]:
        resolved_id = self._resolve_ssh_id(connection_id)
        remote = self.store.get_ssh_connection(resolved_id)
        if remote is None:
            raise ConnectionError("SSH connection 不存在。", 404)
        settings = remote["settings"]
        self._record_history(resolved_id, "validate", "running", "正在校验 OpenSSH 配置。", {"phase": "identity"})
        ssh = shutil.which("ssh")
        if not ssh:
            updated = self.store.update_ssh_connection_state(resolved_id, "error", "系统 OpenSSH 客户端不可用。")
            self._record_history(resolved_id, "validate", "failed", "系统 OpenSSH 客户端不可用。", {"code": "ssh_unavailable"})
            raise ConnectionError(updated["detail"] if updated else "系统 OpenSSH 客户端不可用。", 503)
        argv = build_ssh_validation_argv(
            ssh_executable=ssh,
            host=settings["host"],
            port=settings["port"],
            user=settings["user"],
            ssh_config_alias=settings["ssh_config_alias"],
            identity_file=settings["identity_file"],
        )
        try:
            result = subprocess.run(
                argv,
                stdin=subprocess.DEVNULL,
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace",
                timeout=10,
                check=False,
                creationflags=_windows_hide_flags(),
                startupinfo=_windows_hide_startupinfo(),
            )
        except (OSError, subprocess.TimeoutExpired) as exc:
            self.store.update_ssh_connection_state(resolved_id, "error", "SSH 配置校验未完成。")
            self._record_history(resolved_id, "validate", "failed", "SSH 配置校验未完成。", {"code": "validation_unavailable"})
            raise ConnectionError("SSH 配置校验未完成。", 503) from exc
        if result.returncode != 0:
            detail = "SSH 配置校验失败；未建立远程连接。"
            updated = self.store.update_ssh_connection_state(resolved_id, "error", detail)
            self._record_history(resolved_id, "validate", "failed", detail, {"code": "validation_failed"})
            raise ConnectionError(updated["detail"] if updated else detail, 422)
        updated = self.store.update_ssh_connection_state(
            resolved_id,
            "validated",
            "OpenSSH 配置已校验；尚未连接或启动远端 Hermes。",
        )
        if updated is None:
            raise ConnectionError("SSH connection 不存在。", 404)
        self._record_history(resolved_id, "validate", "completed", "OpenSSH 配置已校验。", {"phase": "identity"})
        return updated

    def connect_ssh(self, service, connection_id: str | None = None) -> dict[str, object]:
        from .ssh_transport import SshNativeRuntime

        resolved_id = self._resolve_ssh_id(connection_id)
        remote = self.store.get_ssh_connection(resolved_id)
        if remote is None:
            raise ConnectionError("SSH connection 不存在。", 404)
        with self._ssh_lock:
            existing_runtime = self._ssh_runtimes.get(resolved_id)
            if existing_runtime is not None and existing_runtime.snapshot()["alive"]:
                return self.snapshot(service)
            self.store.update_ssh_connection_state(
                resolved_id, "connecting", "正在通过 SSH 部署 Astrorder 插件并启动原生 bridge。"
            )
            stage = "deploy"
            self._record_history(resolved_id, "deploy", "running", "正在通过 SSH 部署 Astrorder 插件。", {"phase": "deploy"})
            runtime = SshNativeRuntime(
                {**remote["settings"], "display_name": remote["display_name"], "profile_name": remote["profile_name"]},
                resolved_id,
                self.settings.port,
                Path(__file__).resolve().parents[3],
                Path(__file__).resolve().parents[3] / ".hermes" / "plugins" / "astrorder-hermes",
                connector_secret=self.settings.connector_secret,
            )
            runtime.store = self.store
            runtime.app_settings = self.settings
            runtime.service = service
            try:
                runtime.start()
                self._record_history(resolved_id, "deploy", "completed", "远程插件部署和 bridge 启动命令已返回。", {"phase": "deploy"})
                stage = "handshake"
                self._record_history(resolved_id, "handshake", "running", "等待 native connector handshake。", {"phase": "handshake"})
                deadline = time.monotonic() + 20
                while runtime.agent_id not in service.connections and runtime.snapshot()["alive"]:
                    if time.monotonic() >= deadline:
                        break
                    threading.Event().wait(0.1)
                if runtime.agent_id not in service.connections:
                    raise ConnectionError(
                        f"SSH 已建立但远程 Hermes native connector handshake 未完成；{runtime.bridge_failure_detail()}；bridge 已清理。",
                        504,
                    )
                if not runtime.wait_gateway(20):
                    raise ConnectionError(
                        f"SSH 已建立但远程 Hermes native connector handshake 未完成；{runtime.bridge_failure_detail()}；bridge 已清理。",
                        504,
                    )
                self._ssh_runtimes[resolved_id] = runtime
                service.register_native_command_handler(
                    runtime.agent_id,
                    lambda command, current=runtime: self._submit_owned_ssh_command(current, command),
                )
                service.register_native_history_handler(
                    runtime.agent_id,
                    lambda session_id, current=runtime: self._load_native_history(current, session_id),
                )
                try:
                    discovery = runtime.discover_native_sessions()
                    self._record_native_discovery(service, discovery)
                    self._record_history(resolved_id, "discovery", "completed", "native session/project discovery 已返回。", {"phase": "catalog"})
                except (OSError, RuntimeError, ValueError):
                    discovery = None
                    self._record_history(resolved_id, "discovery", "failed", "native session/project discovery 未完成；连接仍保持。", {"phase": "catalog", "code": "discovery_failed"})
                owned_session = runtime.create_owned_session()
                if owned_session is not None:
                    _tui_session_id, stored_session_id = owned_session
                    service.record_native_sessions(
                        [
                            {
                                "id": stored_session_id,
                                "agent_id": runtime.agent_id,
                                "title": "Astrorder 远程会话",
                                "workspace": remote["settings"].get("workspace"),
                                "status": "idle",
                                "source_id": runtime.source_id,
                                "connection_id": resolved_id,
                                "source_session_id": stored_session_id,
                                "history_state": "pending",
                                "control_state": "owned",
                                "updated_at": datetime.now(UTC).isoformat().replace("+00:00", "Z"),
                            }
                        ]
                    )
                updated = self.store.update_ssh_connection_state(
                    resolved_id,
                    "connected",
                    "远程 Hermes 已通过 native connector handshake 连接；历史会话保持只读。"
                    if discovery is not None
                    else "远程 Hermes 已连接，但 native session/project discovery 暂不可用。",
                    remote_os=runtime.metadata.remote_os if runtime.metadata else None,
                    agent_id=runtime.agent_id,
                    runtime_id=runtime.runtime_id,
                )
                if updated is None:
                    raise ConnectionError("SSH connection 状态无法保存。", 500)
                self._record_history(resolved_id, "handshake", "connected", "远程 Hermes 已通过 native connector handshake 连接。", {"phase": "handshake"})
                return self.snapshot(service)
            except Exception as exc:
                runtime.stop()
                self._ssh_runtimes.pop(resolved_id, None)
                if isinstance(exc, ConnectionError):
                    detail, status = exc.detail, exc.status_code
                else:
                    detail, status = "远程 Hermes native bridge 启动失败；已清理 SSH 进程。", 502
                self.store.update_ssh_connection_state(resolved_id, "error", detail)
                self._record_history(resolved_id, stage, "failed", detail, {"phase": stage, "status_code": status})
                raise ConnectionError(detail, status) from exc

    def disconnect_ssh(self, service, connection_id: str | None = None) -> dict[str, object]:
        resolved_id = self._resolve_ssh_id(connection_id)
        runtime = self._ssh_runtimes.pop(resolved_id, None)
        if runtime is not None:
            service.clear_native_command_handler(runtime.agent_id)
            service.clear_native_history_handler(runtime.agent_id)
            runtime.stop()
        updated = self.store.update_ssh_connection_state(
            resolved_id,
            "disconnected",
            "SSH bridge 已停止；不会自动重发未确认命令。",
        )
        if updated is None:
            raise ConnectionError("SSH connection 不存在。", 404)
        self._record_history(resolved_id, "disconnect", "completed", "SSH bridge 已停止。", {"phase": "disconnect"})
        return self.snapshot(service)

    def delete_ssh(self, service, connection_id: str) -> dict[str, object]:
        if connection_id in self._ssh_runtimes:
            raise ConnectionError("请先断开 SSH connection，再删除它。", 409)
        if not self.store.delete_ssh_connection(connection_id):
            raise ConnectionError("SSH connection 不存在。", 404)
        return self.snapshot(service)

    def shutdown(self, service) -> None:
        self.disconnect_local(service)
        for connection_id in tuple(self._ssh_runtimes):
            try:
                self.disconnect_ssh(service, connection_id)
            except ConnectionError:
                pass
