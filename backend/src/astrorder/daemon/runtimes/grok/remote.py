"""Daemon-owned Grok ACP transport launched through SSH."""
from __future__ import annotations

import os
import posixpath
from collections.abc import Mapping
from pathlib import Path, PurePosixPath
from typing import Any

from astrorder_codex_connector.app_server import CodexAppServer
from astrorder_codex_connector.config import CodexConnectorConfig

from astrorder.connections import validate_ssh_settings
from ..ssh_transport import SshNativeRuntime, build_remote_python_command
from astrorder.daemon.errors import DaemonProtocolError
from .runtime import GrokDaemonRuntime


class RemoteGrokDaemonRuntime(GrokDaemonRuntime):
    def __init__(self, connection_id: str, raw_settings: Mapping[str, Any], *, emit) -> None:
        settings_payload = dict(raw_settings)
        executable = settings_payload.pop("grok_executable", None)
        if (
            not isinstance(connection_id, str)
            or not connection_id
            or not isinstance(executable, str)
            or not PurePosixPath(executable).is_absolute()
        ):
            raise ValueError("remote Grok binding is invalid")
        settings = validate_ssh_settings(settings_payload)
        self.connection_id = connection_id
        self.ssh_settings = settings
        self.remote_executable = executable
        self._transport = SshNativeRuntime(
            settings, connection_id, 0, Path.cwd(), Path.cwd(), connector_secret=None
        )
        self.config = _RemoteGrokConfig(
            executable=executable,
            workspace=self._normalize_workspace(settings.get("workspace") or "/"),
            allowed_workspaces=(PurePosixPath("/"),),
            agent_id=f"ssh-grok-{connection_id}",
            agent_name=f"{settings.get('display_name') or connection_id} · Grok Build",
        )
        self.emit = emit
        self.client_factory = CodexAppServer
        self._sessions = {}
        import threading

        self._lock = threading.RLock()

    def _client(self, workspace: PurePosixPath, notification) -> Any:
        config = CodexConnectorConfig(
            endpoint="",
            secret="",
            agent_id=self.config.agent_id,
            agent_name=self.config.agent_name,
            executable=self.remote_executable,
            workspace=Path.cwd(),
            allowed_workspaces=(Path.cwd(),),
        )
        source = (
            "import os\n"
            "from pathlib import Path\n"
            f"p={self.remote_executable!r}\n"
            f"os.chdir({str(workspace)!r})\n"
            'os.environ["PATH"]=str(Path(p).parent)+os.pathsep+os.environ.get("PATH","")\n'
            'os.execv(p,[p,"agent","stdio"])\n'
        )
        return self.client_factory(
            config,
            notification,
            launch_argv=self._ssh_argv() + [build_remote_python_command(source)],
            environment=dict(os.environ),
            request_name="Grok",
        )

    def _ssh_argv(self) -> list[str]:
        return [
            "-o" if part == "-o" else "StrictHostKeyChecking=yes"
            if part == "StrictHostKeyChecking=ask"
            else part
            for part in self._transport._base_ssh_argv()
        ] + [self._transport._target()]

    def _workspace(self, raw: Any) -> PurePosixPath:
        return self.config.workspace if raw is None else self._normalize_workspace(raw)

    @staticmethod
    def _normalize_workspace(raw: Any) -> PurePosixPath:
        if not isinstance(raw, str) or not raw or "\x00" in raw:
            raise DaemonProtocolError("remote Grok workspace is invalid")
        value = posixpath.normpath(raw)
        if not value.startswith("/"):
            raise DaemonProtocolError("remote Grok workspace must be absolute")
        return PurePosixPath(value)

    async def disconnect(self) -> None:
        await self.shutdown()


class _RemoteGrokConfig:
    def __init__(self, **values: Any) -> None:
        vars(self).update(values)
