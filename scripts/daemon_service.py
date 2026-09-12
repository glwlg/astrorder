"""Independent, verified Session Daemon lifecycle helpers."""
from __future__ import annotations

import asyncio
import json
import subprocess
import time
from collections.abc import Callable, Sequence
from pathlib import Path
from typing import Any

from service_lifecycle import (
    command_line_for_pid,
    current_listening_pids,
    select_owned_daemon_pid,
)

ROOT = Path(__file__).resolve().parent.parent
DEFAULT_PORT = 30009
_SECRET_FLAGS = frozenset({"--secret", "--daemon-secret", "--session-daemon-secret"})


def daemon_argv(
    *,
    python_executable: str,
    port: int = DEFAULT_PORT,
    runtime_args: Sequence[str] = (),
) -> list[str]:
    if not isinstance(python_executable, str) or not python_executable:
        raise ValueError("python_executable is required")
    if not isinstance(port, int) or not 1 <= port <= 65535:
        raise ValueError("port must be between 1 and 65535")
    if any(not isinstance(arg, str) or not arg for arg in runtime_args):
        raise ValueError("runtime args must be non-empty strings")
    if any(arg.casefold() in _SECRET_FLAGS for arg in runtime_args):
        raise ValueError("daemon secrets must be supplied only through the environment")
    return [
        python_executable,
        "-m",
        "astrorder.daemon.session_daemon",
        "--port",
        str(port),
        *runtime_args,
    ]


def existing_verified_daemon(
    pids: list[int], command_line: Callable[[int], str | None]
) -> int | None:
    if not pids:
        return None
    pid = select_owned_daemon_pid(pids, command_line)
    if pid is None:
        raise RuntimeError("port listener is not a verified Astrorder Session Daemon")
    return pid


def status_payload(
    *,
    port: int,
    pids: list[int],
    command_line: Callable[[int], str | None],
    metadata_path: Path,
) -> dict[str, Any]:
    verified_pid = select_owned_daemon_pid(pids, command_line)
    payload: dict[str, Any] = {
        "port": port,
        "listening": bool(pids),
        "pid": verified_pid,
        "verified": verified_pid is not None,
    }
    if verified_pid is not None and metadata_path.is_file():
        try:
            saved = json.loads(metadata_path.read_text(encoding="utf-8"))
        except (OSError, ValueError):
            saved = None
        if isinstance(saved, dict) and saved.get("pid") == verified_pid:
            started_at = saved.get("started_at")
            if isinstance(started_at, str):
                payload["started_at"] = started_at
    return payload


def start_daemon(
    *,
    port: int = DEFAULT_PORT,
    runtime_args: Sequence[str] = (),
    metadata_path: Path | None = None,
) -> int:
    existing = existing_verified_daemon(current_listening_pids(port), command_line_for_pid)
    if existing is not None:
        return existing
    python_executable = str(ROOT / "backend/.venv/Scripts/python.exe")
    argv = daemon_argv(
        python_executable=python_executable,
        port=port,
        runtime_args=runtime_args,
    )
    runtime = ROOT / ".runtime"
    runtime.mkdir(parents=True, exist_ok=True)
    log = (runtime / "session-daemon.log").open("ab", buffering=0)
    proc = subprocess.Popen(
        argv,
        cwd=str(ROOT / "backend"),
        stdout=log,
        stderr=log,
        creationflags=0x00000008 | 0x08000000,
    )
    path = metadata_path or runtime / "session-daemon.json"
    path.write_text(
        json.dumps({"pid": proc.pid, "port": port, "started_at": _timestamp()}),
        encoding="utf-8",
    )
    return proc.pid


def graceful_shutdown(port: int, secret: str) -> None:
    if not isinstance(secret, str) or not secret:
        raise RuntimeError("ASTRORDER_SESSION_DAEMON_SECRET is required for graceful daemon shutdown")

    async def request() -> None:
        import websockets

        async with websockets.connect(
            f"ws://127.0.0.1:{port}", open_timeout=3, close_timeout=3, max_size=2_000_000
        ) as socket:
            async def call(action: str, fields: dict[str, str]) -> dict[str, Any]:
                request_id = f"daemon-service-{action}"
                await socket.send(
                    json.dumps({"action": action, "request_id": request_id, **fields})
                )
                raw = await asyncio.wait_for(socket.recv(), timeout=3)
                response = json.loads(raw)
                if (
                    not isinstance(response, dict)
                    or response.get("request_id") != request_id
                    or response.get("action") != f"{action}.result"
                ):
                    raise RuntimeError("daemon graceful shutdown was not confirmed")
                return response

            await call("daemon.handshake", {"secret": secret})
            response = await call("daemon.shutdown", {})
            if response.get("result") != {"stopping": True}:
                raise RuntimeError("daemon graceful shutdown was not confirmed")

    asyncio.run(request())


def stop_daemon(
    *, port: int = DEFAULT_PORT, secret: str | None = None, metadata_path: Path | None = None
) -> int | None:
    pid = existing_verified_daemon(current_listening_pids(port), command_line_for_pid)
    if pid is None:
        return None
    graceful_shutdown(port, secret or "")
    deadline = time.monotonic() + 10
    while current_listening_pids(port):
        if time.monotonic() >= deadline:
            raise RuntimeError("verified Session Daemon did not release its listener after shutdown")
        time.sleep(0.1)
    path = metadata_path or ROOT / ".runtime" / "session-daemon.json"
    try:
        path.unlink()
    except FileNotFoundError:
        pass
    return pid


def _timestamp() -> str:
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
