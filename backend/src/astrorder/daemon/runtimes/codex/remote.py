"""Daemon-owned Codex app-server transport launched through an exact SSH binding."""
from __future__ import annotations

import asyncio
import json
import os
import posixpath
import subprocess
from collections.abc import Mapping
from pathlib import Path, PurePosixPath
from typing import Any

from astrorder_codex_connector.app_server import CodexAppServer, CodexRpcRejected

from astrorder.connections import (
    _windows_hide_flags,
    _windows_hide_startupinfo,
    validate_ssh_settings,
)
from astrorder.ssh_transport import SshNativeRuntime, build_remote_python_command
from .runtime import CodexDaemonRuntime
from astrorder.daemon.errors import DaemonProtocolError


class RemoteCodexDaemonRuntime(CodexDaemonRuntime):
    """Own remote Codex app-server SSH children without App Server process ownership."""

    def __init__(
        self,
        connection_id: str,
        raw_settings: Mapping[str, Any],
        *,
        emit,
        client_factory=CodexAppServer,
    ) -> None:
        if not isinstance(connection_id, str) or not connection_id or len(connection_id) > 128:
            raise ValueError("connection_id must be a non-empty string up to 128 characters")
        settings_payload = dict(raw_settings)
        executable = settings_payload.pop("codex_executable", None)
        if (
            not isinstance(executable, str)
            or not executable
            or "\x00" in executable
            or not PurePosixPath(executable).is_absolute()
        ):
            raise ValueError("remote Codex executable must be an absolute POSIX path")
        settings = validate_ssh_settings(settings_payload)
        display_name = str(settings.get("display_name") or connection_id)
        workspace = self._normalize_remote_workspace(settings.get("workspace") or "/")
        self.connection_id = connection_id
        self.ssh_settings = settings
        self.remote_executable = executable
        self._native_client_factory = client_factory
        self._transport = SshNativeRuntime(
            settings,
            connection_id,
            0,
            Path.cwd(),
            Path.cwd(),
            connector_secret=None,
        )
        config = _RemoteCodexConfig(
            executable=executable,
            workspace=workspace,
            allowed_workspaces=(PurePosixPath("/"),),
            agent_id=f"ssh-codex-{connection_id}",
            agent_name=f"{display_name} · Codex",
        )
        super().__init__(config, emit=emit, client_factory=self._client)

    def _client(self, config, on_notification, environment=None, **kwargs):
        child_environment = dict(os.environ if environment is None else environment)
        source = (
            "import json, os\n"
            "from pathlib import Path\n"
            "bootstrap=bytearray()\n"
            "while True:\n"
            " chunk=os.read(0, 1)\n"
            " if not chunk:\n"
            '  raise RuntimeError("Codex bootstrap stdin closed before payload")\n'
            ' if chunk == b"\\n":\n'
            "  break\n"
            " bootstrap.extend(chunk)\n"
            'payload=json.loads(bootstrap.decode("utf-8"))\n'
            'forwarded=payload.get("environment")\n'
            'remote_path=os.environ.get("PATH", "")\n'
            "if isinstance(forwarded, dict):\n"
            " for key,value in forwarded.items():\n"
            '  if not isinstance(key, str) or not key or "=" in key or "\\x00" in key:\n'
            "   continue\n"
            '  if key.casefold() == "path":\n'
            "   continue\n"
            "  if not os.environ.get(key):\n"
            "   os.environ[key]=str(value)\n"
            f"p={self.remote_executable!r}\n"
            'os.environ["PATH"]=str(Path(p).parent)+os.pathsep+remote_path\n'
            'os.execv(p,[p,"app-server","--listen","stdio://"])\n'
        )
        return self._native_client_factory(
            config,
            on_notification,
            **kwargs,
            launch_argv=self._ssh_argv() + [build_remote_python_command(source)],
            environment=child_environment,
            bootstrap_stdin={"environment": child_environment},
        )

    def _ssh_argv(self) -> list[str]:
        return [
            "-o" if part == "-o" else "StrictHostKeyChecking=yes" if part == "StrictHostKeyChecking=ask" else part
            for part in self._transport._base_ssh_argv()
        ] + [self._transport._target()]

    def _workspace(self, raw_workspace: Any) -> PurePosixPath:
        if raw_workspace is None:
            return self.config.workspace
        return self._normalize_remote_workspace(raw_workspace)

    async def _resume(self, client, session_id: str) -> Mapping[str, Any]:
        try:
            return await super()._resume(client, session_id)
        except CodexRpcRejected as exc:
            detail = str(exc).casefold()
            if "thread not found" not in detail:
                raise
            rollout_path = await asyncio.to_thread(self._rollout_path, session_id)
            if not rollout_path:
                raise
            return await asyncio.to_thread(
                client.request,
                "thread/resume",
                {
                    "threadId": session_id,
                    "path": rollout_path,
                    "excludeTurns": True,
                },
            )

    def _rollout_path(self, session_id: str) -> str | None:
        source = (
            "import json, sqlite3\n"
            "from pathlib import Path\n"
            f"sid={session_id!r}\n"
            "found=None\n"
            "home=Path('~/.codex').expanduser()\n"
            "dbs=sorted(home.glob('state_*.sqlite'))\n"
            "if (home/'state.db').is_file(): dbs.append(home/'state.db')\n"
            "for db_path in dbs:\n"
            " try:\n"
            "  with sqlite3.connect(db_path.resolve().as_uri()+'?mode=ro', uri=True) as db:\n"
            "   cols={row[1] for row in db.execute('PRAGMA table_info(threads)')}\n"
            "   if not {'id','rollout_path'}.issubset(cols): continue\n"
            "   row=db.execute('SELECT rollout_path FROM threads WHERE id=?',(sid,)).fetchone()\n"
            "   if row and isinstance(row[0],str) and row[0] and Path(row[0]).is_file():\n"
            "    found=row[0]; break\n"
            " except (OSError,sqlite3.Error):\n"
            "  continue\n"
            "print(json.dumps(found))\n"
        )
        try:
            value = self._remote_json(source)
        except (OSError, subprocess.TimeoutExpired, ValueError):
            return None
        if (
            not isinstance(value, str)
            or not value
            or len(value) > 4096
            or any(ord(char) < 32 for char in value)
        ):
            return None
        return value

    def _remote_json(self, source: str) -> Any:
        loader = 'import sys\nexec(compile(sys.stdin.read(), "<astrorder-remote>", "exec"))'
        result = subprocess.run(
            self._ssh_argv() + [build_remote_python_command(loader)],
            input=source,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            encoding="utf-8",
            timeout=30,
            check=False,
            creationflags=_windows_hide_flags(),
            startupinfo=_windows_hide_startupinfo(),
        )
        if result.returncode:
            return None
        return json.loads(result.stdout)

    @staticmethod
    def _normalize_remote_workspace(raw_workspace: Any) -> PurePosixPath:
        if (
            not isinstance(raw_workspace, str)
            or not raw_workspace
            or "\x00" in raw_workspace
            or any(ord(char) < 32 for char in raw_workspace)
        ):
            raise DaemonProtocolError("remote Codex workspace is invalid")
        normalized = posixpath.normpath(raw_workspace)
        if not normalized.startswith("/") or normalized == "//":
            raise DaemonProtocolError("remote Codex workspace must be an absolute POSIX path")
        return PurePosixPath(normalized)

    async def disconnect(self) -> None:
        await self.shutdown()


class _RemoteCodexConfig:
    def __init__(
        self,
        *,
        executable: str,
        workspace: PurePosixPath,
        allowed_workspaces: tuple[PurePosixPath, ...],
        agent_id: str,
        agent_name: str,
    ) -> None:
        self.executable = executable
        self.workspace = workspace
        self.allowed_workspaces = allowed_workspaces
        self.agent_id = agent_id
        self.agent_name = agent_name
        self.environment = None
