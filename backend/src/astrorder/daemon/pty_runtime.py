"""Daemon-owned terminal runtime; App Server only projects its buffered output."""
from __future__ import annotations

import asyncio
import threading
from collections.abc import Awaitable, Callable, Mapping
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Protocol

from ..core.terminal_service import TerminalSession
from .errors import DaemonProtocolError


class TerminalLike(Protocol):
    async def start(self) -> None: ...

    def read_sync(self) -> str: ...

    def write_sync(self, value: str) -> None: ...

    def resize(self, cols: int, rows: int) -> None: ...

    def close(self) -> None: ...


TerminalFactory = Callable[..., TerminalLike]
FrameEmitter = Callable[..., Awaitable[dict[str, Any]]]


@dataclass(frozen=True)
class PtyDaemonRuntimeConfig:
    allowed_workspaces: tuple[Path, ...]

    def __post_init__(self) -> None:
        roots = tuple(Path(root).expanduser().resolve() for root in self.allowed_workspaces)
        if not roots or any(not root.is_dir() for root in roots):
            raise ValueError("PTY allowed_workspaces must contain existing directories")
        object.__setattr__(self, "allowed_workspaces", roots)


@dataclass
class _OwnedTerminal:
    terminal: TerminalLike
    reader: asyncio.Task[None]
    status: str = "running"


class PtyDaemonRuntime:
    """Own local PTYs created only after an explicit daemon `session.spawn`."""

    def __init__(
        self,
        config: PtyDaemonRuntimeConfig,
        *,
        emit: FrameEmitter,
        terminal_factory: TerminalFactory = TerminalSession,
    ) -> None:
        self.config = config
        self.emit = emit
        self.terminal_factory = terminal_factory
        self._sessions: dict[str, _OwnedTerminal] = {}
        self._lock = threading.RLock()

    async def spawn(self, request: Mapping[str, Any]) -> Mapping[str, Any]:
        session_id = _session_id(request)
        workspace = self._workspace(request.get("cwd"))
        with self._lock:
            if session_id in self._sessions:
                raise DaemonProtocolError("PTY session is already daemon-owned")
        terminal = self.terminal_factory(workspace=str(workspace))
        await terminal.start()
        reader = asyncio.create_task(self._read_output(session_id, terminal))
        with self._lock:
            self._sessions[session_id] = _OwnedTerminal(terminal=terminal, reader=reader)
        return {"status": "running"}

    async def command(self, action: str, request: Mapping[str, Any]) -> Mapping[str, Any]:
        session_id = _session_id(request)
        owned = self._owned(session_id)
        if action == "session.send":
            value = request.get("input")
            if not isinstance(value, str) or not value or len(value) > 65_536:
                raise DaemonProtocolError("PTY input is invalid")
            await asyncio.to_thread(owned.terminal.write_sync, value)
        elif action == "session.resize":
            cols, rows = request.get("cols"), request.get("rows")
            if not isinstance(cols, int) or not isinstance(rows, int) or not 20 <= cols <= 400 or not 10 <= rows <= 200:
                raise DaemonProtocolError("PTY dimensions are invalid")
            await asyncio.to_thread(owned.terminal.resize, cols, rows)
        elif action == "session.interrupt":
            await asyncio.to_thread(owned.terminal.write_sync, "\x03")
        elif action == "session.close":
            with self._lock:
                self._sessions.pop(session_id, None)
            owned.reader.cancel()
            await asyncio.to_thread(owned.terminal.close)
            await asyncio.gather(owned.reader, return_exceptions=True)
            return {"status": "idle", "closed": True}
        else:
            raise DaemonProtocolError("PTY runtime action is unsupported")
        with self._lock:
            return {"status": owned.status, "accepted": True}

    async def shutdown(self) -> None:
        with self._lock:
            owned = list(self._sessions.values())
            self._sessions.clear()
        for item in owned:
            item.reader.cancel()
        await asyncio.gather(
            *(asyncio.to_thread(item.terminal.close) for item in owned),
            return_exceptions=True,
        )
        await asyncio.gather(*(item.reader for item in owned), return_exceptions=True)

    async def _read_output(self, session_id: str, terminal: TerminalLike) -> None:
        try:
            while True:
                chunk = await asyncio.to_thread(terminal.read_sync)
                if not chunk:
                    break
                await self.emit(session_id, "pty.output", {"data": chunk}, status="running")
        finally:
            with self._lock:
                owned = self._sessions.get(session_id)
                if owned is not None and owned.terminal is terminal:
                    owned.status = "idle"
            await self.emit(session_id, "pty.closed", {}, status="idle")

    def _owned(self, session_id: str) -> _OwnedTerminal:
        with self._lock:
            owned = self._sessions.get(session_id)
        if owned is None:
            raise DaemonProtocolError("PTY session is not daemon-owned")
        return owned

    def _workspace(self, raw: Any) -> Path:
        if not isinstance(raw, str) or not raw or "\x00" in raw:
            raise DaemonProtocolError("PTY workspace is invalid")
        workspace = Path(raw).expanduser().resolve()
        if not workspace.is_dir() or not any(_inside(workspace, root) for root in self.config.allowed_workspaces):
            raise DaemonProtocolError("PTY workspace is outside daemon allowlist")
        return workspace


def _session_id(request: Mapping[str, Any]) -> str:
    session_id = request.get("session_id")
    if not isinstance(session_id, str) or not session_id or len(session_id) > 256:
        raise DaemonProtocolError("PTY session_id is invalid")
    return session_id


def _inside(path: Path, root: Path) -> bool:
    try:
        path.relative_to(root)
        return True
    except ValueError:
        return False
