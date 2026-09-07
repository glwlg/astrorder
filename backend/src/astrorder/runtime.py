from __future__ import annotations

import asyncio
import shutil
import subprocess
from pathlib import Path
from typing import ClassVar

from .config import Settings


class RuntimeUnavailable(RuntimeError):
    pass


class ProcessSupervisor:
    """Owns only processes started by this object with fixed command construction."""

    _fixed_args: ClassVar[dict[str, tuple[str, ...]]] = {
        # Hermes has no verified headless control transport in the installed public plugin API.
        "hermes": (),
        # This is process supervision only; the Codex connector separately speaks app-server JSON-RPC.
        "codex": ("app-server", "--listen", "stdio://"),
    }

    def __init__(self, settings: Settings):
        self.settings = settings
        self._owned: dict[str, subprocess.Popen] = {}

    def _executable(self, kind: str) -> str | None:
        configured = self.settings.hermes_executable if kind == "hermes" else self.settings.codex_executable
        if configured:
            candidate = Path(configured)
            return str(candidate) if candidate.is_file() else None
        return shutil.which(kind)

    def _workspace(self, value: str) -> Path:
        candidate = Path(value).expanduser().resolve()
        if not candidate.is_dir():
            raise RuntimeUnavailable("Workspace is not an existing directory")
        for root in self.settings.allowed_workspaces:
            resolved_root = root.expanduser().resolve()
            try:
                candidate.relative_to(resolved_root)
                return candidate
            except ValueError:
                continue
        raise RuntimeUnavailable("Workspace is not allowlisted")

    def items(self) -> list[dict[str, object]]:
        result: list[dict[str, object]] = []
        for kind in ("hermes", "codex"):
            if not self.settings.launch_enabled:
                result.append(
                    {
                        "kind": kind,
                        "available": False,
                        "capabilities": [],
                        "reason": "Managed launch is disabled",
                    }
                )
            elif kind == "hermes":
                result.append(
                    {
                        "kind": kind,
                        "available": False,
                        "capabilities": [],
                        "reason": "No verified headless Hermes control transport is available",
                    }
                )
            else:
                result.append(
                    {
                        "kind": kind,
                        "available": False,
                        "capabilities": [],
                        "reason": (
                            "No verified managed Codex connector launch is available; "
                            "a detached app-server is not an Agent connection"
                        ),
                    }
                )
        return result

    def launch(self, kind: str, workspace: str) -> dict[str, str]:
        if kind not in self._fixed_args:
            raise RuntimeUnavailable("Runtime kind is not supported")
        if not self.settings.launch_enabled:
            raise RuntimeUnavailable("Managed launch is disabled")
        if kind == "hermes":
            raise RuntimeUnavailable("No verified headless Hermes control transport is available")
        raise RuntimeUnavailable(
            "No verified managed Codex connector launch is available; a detached app-server is not an Agent connection"
        )

    async def shutdown(self) -> None:
        processes = list(self._owned.items())
        self._owned.clear()
        for _launch_id, process in processes:
            if process.poll() is None:
                process.terminate()
        if processes:
            await asyncio.to_thread(self._wait_for_processes, processes)

    @staticmethod
    def _wait_for_processes(processes: list[tuple[str, subprocess.Popen]]) -> None:
        for _launch_id, process in processes:
            try:
                process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=3)
