from __future__ import annotations

import base64
import hashlib
import json
import os
import logging
import queue
import re
import shlex
import shutil
import subprocess
import threading
from collections import deque
from collections.abc import Callable
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Any
from uuid import uuid4

from .connections import ConnectionError, _windows_hide_flags, _windows_hide_startupinfo, hermes_command_rejection

logger = logging.getLogger(__name__)


def _safe_process_line(value: str, maximum: int = 400) -> str:
    line = str(value or "").replace("\x00", " ").replace("\r", " ").replace("\n", " ").strip()
    line = re.sub(
        r"(?i)(password|passwd|token|secret|authorization|private[_ -]?key)(\s*[=:]\s*)[^\s,;]+",
        r"\1\2[REDACTED]",
        line,
    )
    return line[:maximum]


_REMOTE_INSTALL = r'''
import base64, hashlib, json, os, platform, re, shlex, shutil, subprocess, sys, tempfile
from pathlib import Path

NAME_RE = re.compile(r"^[A-Za-z_][A-Za-z0-9_.-]{0,63}$")
FILES = ("plugin.yaml", "__init__.py", "config.py", "transport.py")

def emit(payload, code=0):
    sys.stdout.write(json.dumps(payload, ensure_ascii=False, separators=(",", ":")) + "\n")
    sys.stdout.flush()
    raise SystemExit(code)

def clean(value, maximum=600):
    value = str(value or "").replace("\x00", " ").replace("\r", " ").replace("\n", " ").strip()
    value = re.sub(
        r"(?i)(password|passwd|token|secret|authorization|private[_ -]?key)(\s*[=:]\s*)[^\s,;]+",
        r"\1\2[REDACTED]",
        value,
    )
    return value[:maximum]

def launcher_python(path):
    try:
        lines = path.read_text(encoding="utf-8", errors="replace").splitlines()
    except OSError:
        return None
    for line in lines[:20]:
        try:
            parts = shlex.split(line.strip())
        except ValueError:
            continue
        if len(parts) > 1 and parts[0] == "exec" and "python" in Path(parts[1]).name.lower():
            candidate = Path(parts[1]).expanduser()
            if candidate.is_file():
                return candidate
    if not lines or not lines[0].startswith("#!"):
        return None
    parts = shlex.split(lines[0][2:].strip())
    if not parts:
        return None
    if Path(parts[0]).name == "env":
        parts = [part for part in parts[1:] if not part.startswith("-")]
    candidate = shutil.which(parts[0]) if parts and not Path(parts[0]).is_absolute() else (Path(parts[0]) if parts else None)
    return candidate if candidate and candidate.is_file() else None

def owned_plugin(plugin_dir):
    if plugin_dir.is_symlink() or not plugin_dir.is_dir():
        return False
    try:
        manifest = (plugin_dir / "plugin.yaml").read_text(encoding="utf-8", errors="replace")
        init = (plugin_dir / "__init__.py").read_text(encoding="utf-8", errors="replace")
        config = (plugin_dir / "config.py").read_text(encoding="utf-8", errors="replace")
        transport = (plugin_dir / "transport.py").read_text(encoding="utf-8", errors="replace")
    except OSError:
        return False
    if "managed_by: astrorder" in manifest:
        return True
    return (
        re.search(r"(?m)^name:\s*astrorder-hermes\s*$", manifest) is not None
        and "Astrorder" in manifest
        and "class HermesBridge" in init
        and "class HermesConnectorConfig" in config
        and "class HermesTransport" in transport
    )

def atomic_write(path, data):
    temp = path.with_name("." + path.name + ".astrorder-tmp")
    temp.write_bytes(data)
    os.replace(str(temp), str(path))

def main():
    try:
        payload = json.loads(sys.stdin.readline())
    except Exception:
        emit({"ok": False, "code": "invalid_bootstrap_payload", "detail": "remote bootstrap payload invalid"}, 2)
    profile = str(payload.get("profile_name") or "default")
    if not NAME_RE.fullmatch(profile):
        emit({"ok": False, "code": "invalid_profile", "detail": "remote profile name invalid"}, 2)
    hermes = str(payload.get("hermes_path") or "").strip()
    if hermes:
        hermes_path = Path(hermes).expanduser()
        if not hermes_path.is_absolute():
            emit({"ok": False, "code": "invalid_hermes_path", "detail": "remote Hermes path must be absolute"}, 2)
    else:
        found = shutil.which("hermes") or shutil.which("hermes.exe")
        if not found:
            for candidate in (Path.home() / ".local" / "bin" / "hermes", Path.home() / ".hermes" / "bin" / "hermes"):
                if candidate.is_file():
                    found = str(candidate)
                    break
        if not found:
            emit({"ok": False, "code": "runtime_missing", "detail": "remote Hermes executable was not found"}, 2)
        hermes_path = Path(found)
    if not hermes_path.is_file():
        emit({"ok": False, "code": "runtime_missing", "detail": "configured remote Hermes executable was not found"}, 2)
    home = Path(os.environ.get("HERMES_HOME") or (Path.home() / ".hermes"))
    if profile != "default":
        home = Path.home() / ".hermes" / "profiles" / profile
    plugin_dir = home / "plugins" / "astrorder-hermes"
    files = payload.get("plugin_files")
    if not isinstance(files, dict) or any(name not in files for name in FILES):
        emit({"ok": False, "code": "plugin_payload_invalid", "detail": "Astrorder plugin payload incomplete"}, 2)
    decoded = {}
    try:
        for name in FILES:
            decoded[name] = base64.b64decode(str(files[name]), validate=True)
    except Exception:
        emit({"ok": False, "code": "plugin_payload_invalid", "detail": "Astrorder plugin payload invalid"}, 2)
    if plugin_dir.exists() and not owned_plugin(plugin_dir):
        emit({"ok": False, "code": "plugin_conflict", "detail": "remote Astrorder plugin id conflicts with existing files"}, 2)
    if plugin_dir.exists():
        try:
            for name, data in decoded.items():
                atomic_write(plugin_dir / name, data)
        except OSError:
            emit({"ok": False, "code": "plugin_install_failed", "detail": "remote Astrorder plugin upgrade failed"}, 2)
    else:
        plugin_dir.parent.mkdir(parents=True, exist_ok=True)
        temp = Path(tempfile.mkdtemp(prefix="astrorder-plugin-", dir=str(plugin_dir.parent)))
        try:
            for name, data in decoded.items():
                target = temp / name
                target.write_bytes(data)
            os.replace(str(temp), str(plugin_dir))
        except Exception:
            shutil.rmtree(temp, ignore_errors=True)
            emit({"ok": False, "code": "plugin_install_failed", "detail": "remote Astrorder plugin install failed"}, 2)
    if any(
        not (plugin_dir / name).is_file() or (plugin_dir / name).read_bytes() != decoded[name]
        for name in FILES
    ):
        emit({"ok": False, "code": "plugin_conflict", "detail": "remote Astrorder plugin id conflicts with existing files"}, 2)
    env = dict(os.environ)
    env["HERMES_HOME"] = str(home)
    try:
        version = subprocess.run(
            [str(hermes_path), "--version"], env=env, stdin=subprocess.DEVNULL,
            capture_output=True, text=True, timeout=15, check=False
        )
    except Exception:
        emit({"ok": False, "code": "runtime_probe_failed", "detail": "remote Hermes version probe failed"}, 2)
    if version.returncode != 0:
        emit({"ok": False, "code": "runtime_probe_failed", "detail": "remote Hermes version probe failed"}, 2)
    try:
        activation = subprocess.run(
            [str(hermes_path), "plugins", "list", "--enabled", "--user", "--plain"], env=env,
            stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=30, check=False
        )
    except Exception:
        emit({"ok": False, "code": "plugin_activation_probe_failed", "detail": "remote Hermes plugin activation state could not be read"}, 2)
    enabled = any(
        len(parts) >= 2 and parts[0].lower() == "enabled" and parts[-1] == "astrorder-hermes"
        for parts in (line.strip().split() for line in (activation.stdout or "").splitlines())
    )
    if activation.returncode != 0:
        detail = clean(activation.stderr or activation.stdout or "remote Hermes plugin activation state could not be read")
        emit({"ok": False, "code": "plugin_activation_probe_failed", "detail": detail}, 2)
    if not enabled:
        emit({"ok": False, "code": "project_activation_required", "detail": "Astrorder plugin is not already enabled in the configured Hermes profile; no global enable was attempted"}, 2)
    candidates = [
        launcher_python(hermes_path),
        hermes_path.parent / ("python.exe" if os.name == "nt" else "python"),
        hermes_path.parent.parent / "Scripts" / "python.exe",
        hermes_path.parent.parent / "bin" / "python",
        Path(shutil.which("python3") or ""),
        Path(shutil.which("python") or ""),
        Path(sys.executable),
    ]
    python_path = next((path for path in candidates if path and path.is_file()), Path(sys.executable))
    emit({
        "ok": True, "code": "installed", "os": platform.system(),
        "hermes_version": clean(version.stdout or version.stderr, 160),
        "profile_home": str(home), "python_path": str(python_path), "python_command": [str(python_path)],
        "plugin_sha256": hashlib.sha256(decoded["__init__.py"]).hexdigest(),
    })

main()
'''

_REMOTE_BRIDGE = r'''
import json, os, sys
from pathlib import Path

def emit(payload, code=0):
    sys.stdout.write(json.dumps(payload, ensure_ascii=False, separators=(",", ":")) + "\n")
    sys.stdout.flush()
    raise SystemExit(code)

def main():
    try:
        payload = json.loads(sys.stdin.readline())
    except Exception:
        emit({"ok": False, "code": "invalid_bridge_payload", "detail": "remote bridge payload invalid"}, 2)
    python_command = payload.get("python_command")
    if not isinstance(python_command, list) or not python_command or not all(isinstance(item, str) and item for item in python_command):
        python_command = [str(payload.get("python_path") or sys.executable)]
    profile_home = str(payload.get("profile_home") or "")
    if not profile_home or not Path(profile_home).is_absolute():
        emit({"ok": False, "code": "invalid_profile_home", "detail": "remote profile home invalid"}, 2)
    env = dict(os.environ)
    env["HERMES_HOME"] = profile_home
    for key in (
        "ASTRORDER_CONNECTOR_ENDPOINT", "ASTRORDER_CONNECTOR_SECRET",
        "ASTRORDER_HERMES_AGENT_ID", "ASTRORDER_HERMES_AGENT_NAME",
        "ASTRORDER_HERMES_SOURCE_ID", "ASTRORDER_HERMES_CONNECTION_ID",
        "ASTRORDER_HERMES_PROFILE_NAME", "ASTRORDER_HERMES_WORKSPACE",
        "ASTRORDER_HERMES_CONNECT_ON_REGISTER",
    ):
        if key in payload.get("env", {}):
            env[key] = str(payload["env"][key])
    cwd = str(payload.get("cwd") or "")
    if cwd:
        if not Path(cwd).is_absolute() or not Path(cwd).is_dir():
            emit({"ok": False, "code": "invalid_workspace", "detail": "remote workspace is not an existing directory"}, 2)
        os.chdir(cwd)
    os.execvpe(python_command[0], [*python_command, "-u", "-m", "tui_gateway.entry"], env)

main()
'''


def _encoded_script(source: str) -> str:
    return base64.b64encode(source.encode("utf-8")).decode("ascii")


def build_remote_python_command(
    source: str, *, interpreter: str = "python3", fallback_interpreter: str | None = "python"
) -> str:
    """Build one fixed remote-shell command that decodes and executes *source*.

    OpenSSH sends the command portion to the target user's shell.  Passing the base64 text as
    ``python -c`` source therefore only asks Python to parse the encoded bytes.  The wrapper is
    intentionally generated from fixed bootstrap source and shell-quotes both the interpreter and
    the Python expression; user SSH fields never become shell source.
    """
    encoded = _encoded_script(source)
    runner = "import base64,sys;exec(compile(base64.b64decode(sys.argv[1]),sys.argv[2],sys.argv[3]))"

    def command_for(executable: str) -> str:
        parts = [executable, "-c", runner, encoded, "astrorder_ssh_bootstrap", "exec"]
        return "exec " + " ".join(shlex.quote(part) for part in parts)

    command = command_for(interpreter)
    if fallback_interpreter is None or fallback_interpreter == interpreter:
        return command
    primary = shlex.quote(interpreter)
    fallback = shlex.quote(fallback_interpreter)
    return (
        'PATH="$PATH:$HOME/.local/bin:$HOME/bin"; '
        f"if command -v {primary} >/dev/null 2>&1; then {command}; "
        f"elif command -v {fallback} >/dev/null 2>&1; then {command_for(fallback)}; "
        "else printf '%s\\n' '{\"ok\":false,\"code\":\"runtime_missing\",\"detail\":\"remote Python interpreter was not found\"}'; exit 127; fi"
    )


def build_remote_stdin_bootstrap_command(
    *, interpreter: str = "python3", fallback_interpreter: str | None = "python"
) -> str:
    """Build a short command whose first stdin line contains the encoded script."""
    runner = "import base64,sys;exec(compile(base64.b64decode(sys.stdin.buffer.readline().strip()),sys.argv[1],sys.argv[2]))"

    def command_for(executable: str) -> str:
        parts = [executable, "-c", runner, "astrorder_ssh_bootstrap", "exec"]
        return "exec " + " ".join(shlex.quote(part) for part in parts)

    command = command_for(interpreter)
    if fallback_interpreter is None or fallback_interpreter == interpreter:
        return command
    primary = shlex.quote(interpreter)
    fallback = shlex.quote(fallback_interpreter)
    return (
        'PATH="$PATH:$HOME/.local/bin:$HOME/bin"; '
        f"if command -v {primary} >/dev/null 2>&1; then {command}; "
        f"elif command -v {fallback} >/dev/null 2>&1; then {command_for(fallback)}; "
        "else printf '%s\\n' '{\"ok\":false,\"code\":\"runtime_missing\",\"detail\":\"remote Python interpreter was not found\"}'; exit 127; fi"
    )


def build_bootstrap_stdin(source: str, payload: dict[str, Any]) -> str:
    """Frame the encoded script before the JSON payload on the SSH stdin stream."""
    return _encoded_script(source) + "\n" + json.dumps(payload, ensure_ascii=False, separators=(",", ":")) + "\n"


@dataclass(frozen=True)
class SshRuntimeMetadata:
    remote_os: str
    profile_home: str
    python_path: str
    python_command: tuple[str, ...]
    hermes_version: str


def _resolve_best_ssh_executable(custom: str | None = None) -> str | None:
    if custom:
        return custom
    if os.name == "nt":
        win_ssh = os.path.join(os.environ.get("SystemRoot", r"C:\Windows"), "System32", "OpenSSH", "ssh.exe")
        if os.path.isfile(win_ssh):
            return win_ssh
    return shutil.which("ssh")


class SshNativeRuntime:
    """Owns one SSH stdio/reverse-tunnel native Hermes runtime."""

    def __init__(
        self,
        settings: dict[str, Any],
        connection_id: str,
        local_port: int,
        project_root: Path,
        plugin_root: Path,
        *,
        connector_secret: str | None,
        ssh_executable: str | None = None,
        popen_factory: Callable[..., subprocess.Popen] = subprocess.Popen,
        command_runner: Callable[..., subprocess.CompletedProcess] = subprocess.run,
    ) -> None:
        self.settings = settings
        self.connection_id = connection_id
        self.local_port = local_port
        self.project_root = project_root
        self.plugin_root = plugin_root
        self.connector_secret = connector_secret
        self.ssh_executable = _resolve_best_ssh_executable(ssh_executable)
        self._popen_factory = popen_factory
        self._command_runner = command_runner
        self._process: subprocess.Popen | Any | None = None
        self._gateway_ready = threading.Event()
        self._responses: dict[str, queue.Queue[dict[str, Any]]] = {}
        self._response_lock = threading.Lock()
        self._lock = threading.RLock()
        self._stderr_tail: deque[str] = deque(maxlen=8)
        self._metadata: SshRuntimeMetadata | None = None
        self._remote_port = 0
        self._agent_id = f"ssh-hermes-{self.connection_id}"
        self._runtime_id = self._agent_id
        self._source_id = "hermes-ssh-" + hashlib.sha256(
            f"{connection_id}|{settings.get('profile_name') or 'default'}".encode()
        ).hexdigest()[:24]
        self._tui_session_id: str | None = None
        self._runtime_session_id: str | None = None
        self.service: Any = None
        self.store: Any = None
        self._tui_to_session: dict[str, str] = {}
        self._active_submitted_commands: dict[str, str] = {}

    @property
    def agent_id(self) -> str:
        return self._agent_id

    @property
    def source_id(self) -> str:
        return self._source_id

    @property
    def runtime_id(self) -> str:
        return self._runtime_id

    @property
    def metadata(self) -> SshRuntimeMetadata | None:
        return self._metadata

    def snapshot(self) -> dict[str, Any]:
        process = self._process
        return {
            "id": self.connection_id,
            "agent_id": self._agent_id,
            "runtime_id": self._runtime_id,
            "source_id": self._source_id,
            "profile_name": self.settings.get("profile_name") or "default",
            "alive": bool(process is not None and process.poll() is None),
            "remote_os": self._metadata.remote_os if self._metadata else None,
        }

    def _target(self) -> str:
        target = self.settings.get("ssh_config_alias") or self.settings.get("host")
        if not isinstance(target, str) or not target:
            raise ConnectionError("请填写 SSH 主机或 SSH 配置别名", 422)
        return target

    def _base_ssh_argv(self) -> list[str]:
        if not self.ssh_executable:
            raise ConnectionError("系统 OpenSSH 客户端不可用。", 503)
        argv = [
            self.ssh_executable,
            "-T",
            "-o",
            "BatchMode=yes",
            "-o",
            "StrictHostKeyChecking=ask",
            "-o",
            "ConnectTimeout=10",
            "-o",
            "ExitOnForwardFailure=yes",
            "-p",
            str(self.settings.get("port", 22)),
        ]
        user = self.settings.get("user")
        identity = self.settings.get("identity_file")
        if user:
            argv.extend(["-l", str(user)])
        if identity:
            argv.extend(["-i", str(identity)])
        return argv

    @staticmethod
    def _classify_ssh_error(stderr: str, fallback: str) -> str:
        lower = stderr.lower()
        if "host key verification failed" in lower or "remote host identification has changed" in lower:
            return "SSH 主机密钥未通过校验；请在系统 known_hosts 中审核该主机，Astrorder 不会自动接受新密钥或 changed key。"
        if "permission denied" in lower or "authentication failed" in lower:
            return "SSH 身份验证失败；请检查 SSH agent/identity reference 和目标用户。"
        if "could not resolve hostname" in lower:
            return "SSH 无法解析目标主机或别名；请检查保存的主机和 SSH config alias。"
        if "connection timed out" in lower or "operation timed out" in lower:
            return "SSH 连接超时；请检查目标端口和网络。"
        if "connection refused" in lower:
            return "SSH 目标拒绝连接；请检查 sshd 和保存的端口。"
        return fallback

    @staticmethod
    def _bootstrap_response(output: str) -> dict[str, Any] | None:
        for line in reversed((output or "")[-65536:].splitlines()):
            try:
                value = json.loads(line)
            except json.JSONDecodeError:
                continue
            if isinstance(value, dict) and (isinstance(value.get("code"), str) or isinstance(value.get("ok"), bool)):
                return value
        return None

    @staticmethod
    def _safe_bootstrap_detail(response: dict[str, Any]) -> str:
        code = re.sub(r"[^A-Za-z0-9_.-]", "_", str(response.get("code") or "remote_failure"))[:64]
        detail = str(response.get("detail") or "remote Astrorder bootstrap failed")
        detail = detail.replace("\x00", " ").replace("\r", " ").replace("\n", " ").strip()
        detail = re.sub(
            r"(?i)(password|passwd|token|secret|authorization|private[_ -]?key)(\s*[=:]\s*)[^\s,;]+",
            r"\1\2[REDACTED]",
            detail,
        )
        return f"远端 Astrorder bootstrap [{code}]：{detail[:600]}"

    def _plugin_files(self) -> dict[str, str]:
        source_dir = self.plugin_root
        connector_dir = self.project_root / "connectors" / "hermes" / "astrorder_hermes_plugin"
        files = {
            "plugin.yaml": source_dir / "plugin.yaml",
            "__init__.py": connector_dir / "__init__.py",
            "config.py": connector_dir / "config.py",
            "transport.py": connector_dir / "transport.py",
        }
        result: dict[str, str] = {}
        for name, path in files.items():
            if not path.is_file():
                raise ConnectionError("Astrorder SSH 插件文件不完整。", 503)
            result[name] = base64.b64encode(path.read_bytes()).decode("ascii")
        return result

    def _install(self) -> SshRuntimeMetadata:
        payload = {
            "profile_name": self.settings.get("profile_name") or "default",
            "hermes_path": self.settings.get("hermes_path"),
            "plugin_files": self._plugin_files(),
        }
        argv = [
            *self._base_ssh_argv(),
            self._target(),
            build_remote_stdin_bootstrap_command(),
        ]
        try:
            result = self._command_runner(
                argv,
                input=build_bootstrap_stdin(_REMOTE_INSTALL, payload),
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace",
                timeout=60,
                check=False,
                creationflags=_windows_hide_flags(),
                startupinfo=_windows_hide_startupinfo(),
            )
        except subprocess.TimeoutExpired as exc:
            raise ConnectionError("远端 Astrorder 插件部署超时。", 504) from exc
        except OSError as exc:
            raise ConnectionError("无法启动系统 OpenSSH 客户端。", 503) from exc
        response = self._bootstrap_response(result.stdout or "")
        if result.returncode != 0:
            if response is not None and not response.get("ok"):
                raise ConnectionError(self._safe_bootstrap_detail(response), 409)
            detail = self._classify_ssh_error(result.stderr or "", "远端 Astrorder 插件部署失败。")
            raise ConnectionError(detail, 409)
        if response is None:
            raise ConnectionError("远端插件部署返回了无效结果。", 502) from None
        if not isinstance(response, dict) or not response.get("ok"):
            detail = self._safe_bootstrap_detail(response)
            raise ConnectionError(detail, 409)
        try:
            python_path = str(response["python_path"])
            raw_python_command = response.get("python_command")
            python_command = (
                tuple(item for item in raw_python_command if isinstance(item, str) and item)
                if isinstance(raw_python_command, list)
                else (python_path,)
            )
            if not python_command:
                python_command = (python_path,)
            metadata = SshRuntimeMetadata(
                remote_os=str(response["os"]),
                profile_home=str(response["profile_home"]),
                python_path=python_path,
                python_command=python_command,
                hermes_version=str(response.get("hermes_version") or "Hermes Agent"),
            )
        except (KeyError, TypeError):
            raise ConnectionError("远端插件部署缺少运行时元数据。", 502) from None
        self._metadata = metadata
        return metadata

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
                frame_id = frame.get("id")
                if frame_id is not None:
                    with self._response_lock:
                        waiter = self._responses.get(str(frame_id))
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
            if stream is not None:
                for line in stream:
                    safe_line = _safe_process_line(line)
                    if safe_line:
                        self._stderr_tail.append(safe_line)

        threading.Thread(target=read_stdout, name=f"astrorder-ssh-rpc-{self.connection_id}", daemon=True).start()
        threading.Thread(target=drain_stderr, name=f"astrorder-ssh-stderr-{self.connection_id}", daemon=True).start()

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
                logger.debug("Failed listing commands for SSH completion: %s", exc)
        for cid in to_complete:
            try:
                updated = self.store.set_command_state(agent_id, session_id, cid, "completed", None)
                if self.service is not None:
                    self.service._server_event("command.upsert", agent_id=agent_id, session_id=session_id, data=updated)
            except Exception as exc:
                logger.debug("Failed completing SSH command %s: %s", cid, exc)

    def start(self) -> None:
        with self._lock:
            if self._process is not None and self._process.poll() is None:
                return
            metadata = self._metadata or self._install()
            self._remote_port = 23000 + (int(hashlib.sha256(self.connection_id.encode()).hexdigest()[:6], 16) % 20000)
            bridge_payload = {
                "python_path": metadata.python_path,
                "python_command": list(metadata.python_command),
                "profile_home": metadata.profile_home,
                "cwd": self.settings.get("workspace") or "",
                "env": {
                    "ASTRORDER_CONNECTOR_ENDPOINT": f"ws://127.0.0.1:{self._remote_port}/ws/v1/connector",
                    "ASTRORDER_CONNECTOR_SECRET": self.connector_secret or "",
                    "ASTRORDER_HERMES_AGENT_ID": self._agent_id,
                    "ASTRORDER_HERMES_AGENT_NAME": str(self.settings.get("display_name") or "远程 Hermes"),
                    "ASTRORDER_HERMES_SOURCE_ID": self._source_id,
                    "ASTRORDER_HERMES_CONNECTION_ID": self.connection_id,
                    "ASTRORDER_HERMES_PROFILE_NAME": str(self.settings.get("profile_name") or "default"),
                    "ASTRORDER_HERMES_WORKSPACE": str(self.settings.get("workspace") or ""),
                    "ASTRORDER_HERMES_CONNECT_ON_REGISTER": "1",
                },
            }
            if not bridge_payload["env"]["ASTRORDER_CONNECTOR_SECRET"]:
                raise ConnectionError("服务端未配置连接器凭据，无法连接远程 Hermes", 503)
            argv = [
                *self._base_ssh_argv(),
                "-R",
                f"{self._remote_port}:127.0.0.1:{self.local_port}",
                self._target(),
                build_remote_stdin_bootstrap_command(
                    interpreter=metadata.python_path,
                    fallback_interpreter=None,
                ),
            ]
            try:
                process = self._popen_factory(
                    argv,
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
                raise ConnectionError("无法启动远程 Hermes SSH bridge。", 503) from exc
            self._process = process
            try:
                stream = getattr(process, "stdin", None)
                if stream is None:
                    raise OSError("missing ssh stdin")
                stream.write(build_bootstrap_stdin(_REMOTE_BRIDGE, bridge_payload))
                stream.flush()
            except OSError as exc:
                self.stop()
                raise ConnectionError("无法初始化远程 Hermes SSH bridge。", 503) from exc
            self._gateway_ready.clear()
            self._start_readers(process)

    def wait_gateway(self, timeout: float = 20) -> bool:
        return self._gateway_ready.wait(timeout=timeout)

    def bridge_failure_detail(self) -> str:
        if self._stderr_tail:
            return f"远端 bridge 诊断：{self._stderr_tail[-1]}"
        process = self._process
        if process is not None and process.poll() is not None:
            return f"远端 bridge 已退出（exit={process.returncode}），未产生 native handshake。"
        return "远端 bridge 未返回 native handshake 诊断。"

    def rpc(self, method: str, params: dict[str, Any], timeout: float = 15) -> dict[str, Any] | None:
        process = self._process
        stream = getattr(process, "stdin", None) if process is not None else None
        if process is None or process.poll() is not None or stream is None:
            return None
        request_id = str(uuid4())
        waiter: queue.Queue[dict[str, Any]] = queue.Queue(maxsize=1)
        with self._response_lock:
            self._responses[request_id] = waiter
        try:
            stream.write(json.dumps({"jsonrpc": "2.0", "id": request_id, "method": method, "params": params}) + "\n")
            stream.flush()
            return waiter.get(timeout=timeout)
        except (OSError, queue.Empty):
            return None
        finally:
            with self._response_lock:
                self._responses.pop(request_id, None)

    def create_session(self, workspace: str | None = None, title: str | None = None) -> dict[str, Any]:
        with self._lock:
            if self._process is None or self._process.poll() is not None:
                raise ConnectionError("远程 Hermes 未连接；无法新建会话。", 503)
            params: dict[str, Any] = {
                "source": "ssh",
                "cwd": workspace or str(self.settings.get("workspace") or ""),
                "title": title or "新会话",
            }
            response = self.rpc("session.create", params)
            result = response.get("result") if isinstance(response, dict) else None
            tui_id = result.get("session_id") if isinstance(result, dict) else None
            stored_id = result.get("stored_session_id") if isinstance(result, dict) else None
            if not stored_id:
                raise ConnectionError("远程 Hermes session.create 未返回有效的会话 ID。", 502)
            return {
                "id": stored_id,
                "agent_id": self._agent_id,
                "title": title or "新会话",
                "workspace": workspace or str(self.settings.get("workspace") or ""),
                "status": "idle",
                "source_id": self._source_id,
                "connection_id": self.connection_id,
                "source_session_id": stored_id,
                "history_state": "live",
                "control_state": "owned",
                "updated_at": datetime.now(UTC).isoformat().replace("+00:00", "Z"),
            }

    def create_owned_session(self) -> tuple[str, str] | None:
        params: dict[str, Any] = {"source": "ssh", "title": "Astrorder 远程会话"}
        if self.settings.get("workspace"):
            params["cwd"] = str(self.settings["workspace"])
        response = self.rpc(
            "session.create",
            params,
        )
        result = response.get("result") if isinstance(response, dict) else None
        tui_id = result.get("session_id") if isinstance(result, dict) else None
        stored_id = result.get("stored_session_id") if isinstance(result, dict) else None
        if not isinstance(tui_id, str) or not isinstance(stored_id, str) or not tui_id or not stored_id:
            return None
        self._tui_session_id = tui_id
        self._runtime_session_id = stored_id
        return tui_id, stored_id

    def discover_native_sessions(self) -> Any:
        from .native_sessions import discover_native_sessions

        discovery = discover_native_sessions(
            self.rpc,
            source_id=self._source_id,
            agent_id=self._agent_id,
            connection_id=self.connection_id,
            profile_name=str(self.settings.get("profile_name") or "default"),
            default_workspace=str(self.settings["workspace"]) if self.settings.get("workspace") else None,
        )
        return discovery

    def load_native_history(self, durable_session_id: str) -> list[dict[str, Any]]:
        from .native_sessions import history_messages

        native_id = durable_session_id
        return history_messages(
            self.rpc,
            durable_session_id=durable_session_id,
            native_session_id=native_id,
            source_id=self._source_id,
            agent_id=self._agent_id,
        )

    def read_user_activity(self, session_ids):
        from . import native_user_activity
        if self._metadata is None:
            return []
        payload = {'database': self._metadata.profile_home.rstrip('/\\') + '/state.db', 'session_ids': session_ids}
        command = build_remote_stdin_bootstrap_command(interpreter=self._metadata.python_path, fallback_interpreter=None)
        response = self._command_runner(
            [*self._base_ssh_argv(), self._target(), command],
            input=build_bootstrap_stdin(Path(native_user_activity.__file__).read_text(encoding='utf-8'), payload),
            capture_output=True, text=True, encoding='utf-8', errors='replace', timeout=15,
            check=False, creationflags=_windows_hide_flags(),
            startupinfo=_windows_hide_startupinfo(),
        )
        if response.returncode:
            return []
        return json.loads(response.stdout).get('items', [])

    def read_presence(self, session_ids):
        from . import native_user_activity
        if self._metadata is None:
            return {'items': [], 'open_ids': [], 'live_ids': []}
        payload = {'database': self._metadata.profile_home.rstrip('/\\') + '/state.db', 'session_ids': session_ids}
        command = build_remote_stdin_bootstrap_command(interpreter=self._metadata.python_path, fallback_interpreter=None)
        response = self._command_runner(
            [*self._base_ssh_argv(), self._target(), command],
            input=build_bootstrap_stdin(Path(native_user_activity.__file__).read_text(encoding='utf-8'), payload),
            capture_output=True, text=True, encoding='utf-8', errors='replace', timeout=15,
            check=False, creationflags=_windows_hide_flags(),
            startupinfo=_windows_hide_startupinfo(),
        )
        if response.returncode:
            return {'items': [], 'open_ids': [], 'live_ids': []}
        try:
            data = json.loads(response.stdout)
        except ValueError:
            return {'items': [], 'open_ids': [], 'live_ids': []}
        if isinstance(data, dict) and 'items' in data:
            return {'items': data.get('items') or [], 'open_ids': data.get('open_ids') or [], 'live_ids': data.get('live_ids') or []}
        return {'items': [], 'open_ids': [], 'live_ids': []}

    def load_native_history_page(self, session_id: str, before: str | None, limit: int) -> dict[str, Any]:
        from . import native_history_page
        if self._metadata is None:
            raise ConnectionError("远端原生会话数据库位置尚未确认。", 503)
        payload = {'database': self._metadata.profile_home.rstrip('/\\') + '/state.db', 'session_id': session_id, 'source_id': self._source_id, 'before': before, 'limit': limit}
        command = build_remote_stdin_bootstrap_command(interpreter=self._metadata.python_path, fallback_interpreter=None)
        response = self._command_runner(
            [*self._base_ssh_argv(), self._target(), command],
            input=build_bootstrap_stdin(Path(native_history_page.__file__).read_text(encoding='utf-8'), payload),
            capture_output=True, text=True, encoding='utf-8', errors='replace', timeout=15,
            check=False, creationflags=_windows_hide_flags(),
            startupinfo=_windows_hide_startupinfo(),
        )
        try:
            page = json.loads(response.stdout)
        except (ValueError, TypeError):
            raise ConnectionError("远端原生消息分页未返回有效数据。", 502) from None
        if response.returncode != 0 or not page.get('ok'):
            raise ConnectionError("远端原生消息分页读取失败。", 502)
        return {'items': page['items'], 'next_cursor': page['next_cursor']}

    def submit(self, command: dict[str, Any]) -> tuple[str, str | None]:
        with self._lock:
            if self._process is None or self._process.poll() is not None:
                return "failed", "远程 Hermes 未连接；未尝试投递命令。"
            session_id = command.get("session_id")
            if command.get("action") == "stop":
                from .native_commands import interrupt_session
                return interrupt_session(self.rpc, session_id)
            if command.get("action", "send") != "send":
                return "failed", "该原生操作不能作为消息投递。"
            tui_id = self._tui_session_id
            # 允许在属于该远程主机的任意历史会话中发消息，后台自动动态 resume
            if session_id != self._runtime_session_id:
                native_id = session_id
                tui_id = None
                try:
                    res = self.rpc("session.resume", {"session_id": native_id, "lazy": False}, timeout=15)
                    if isinstance(res, dict) and isinstance(res.get("result"), dict):
                        tui_id = res["result"].get("session_id") or tui_id
                except Exception as exc:
                    logger.debug("Failed to resume remote SSH session: %s", exc)
            if not tui_id:
                return "failed", "该会话无法在当前远程 Hermes 运行时中恢复。"
            text_value = command.get("text")
            if not isinstance(text_value, str):
                return "failed", "远程 Hermes 命令文本无效。"
            from .hermes_inputs import rollback, stage
            settings = getattr(self, "app_settings", None)
            store = getattr(self, "store", None)
            try:
                text_value, attached = stage(self.rpc, tui_id, command, settings, store)
            except ConnectionError as exc:
                return "failed", exc.detail
            response = self.rpc("prompt.submit", {"session_id": tui_id, "text": text_value, "surface": "hud"}, timeout=20)
            # 如果是由于其他端正在占用该会话导致的排他锁拒绝，尝试以无感引导（session.steer）注入当前活动轮次，不打断会话
            if response and response.get("error") and (
                (response.get("error") or {}).get("data", {}).get("reason") == "SESSION_NOT_OWNED"
                or "already has a live owner" in str((response.get("error") or {}).get("message", "")).lower()
            ):
                steer_res = self.rpc("session.steer", {"session_id": tui_id, "text": text_value}, timeout=10)
                if isinstance(steer_res, dict) and (steer_res.get("result", {}).get("status") in {"queued", "redirected"} or not steer_res.get("error")):
                    response = steer_res
            if response is None:
                rollback(self.rpc, tui_id, attached)
                return "unknown", "远程 Hermes 未确认命令投递结果；不会自动重发。"
            if response.get("error") is not None:
                rollback(self.rpc, tui_id, attached)
                return "failed", hermes_command_rejection(response, remote=True)
            if not isinstance(response.get("result"), dict):
                rollback(self.rpc, tui_id, attached)
                return "unknown", "远程 Hermes 返回了无法确认的命令结果；不会自动重发。"
            if tui_id and session_id:
                self._tui_to_session[str(tui_id)] = str(session_id)
            cmd_id = command.get("id")
            if session_id and isinstance(cmd_id, str):
                self._active_submitted_commands[str(session_id)] = cmd_id
            return "accepted", None

    def stop(self) -> None:
        with self._lock:
            process, self._process = self._process, None
            self._gateway_ready.clear()
            if process is None:
                return
            try:
                stream = getattr(process, "stdin", None)
                if stream is not None:
                    stream.close()
            except OSError:
                pass
            try:
                if process.poll() is None:
                    process.terminate()
                    process.wait(timeout=5)
            except (OSError, subprocess.TimeoutExpired):
                try:
                    process.kill()
                except OSError:
                    pass
