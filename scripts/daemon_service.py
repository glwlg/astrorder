"""Independent, verified Session Daemon lifecycle helpers."""
from __future__ import annotations

import asyncio
import json
import os
import subprocess
import time
from collections.abc import Callable
from contextlib import contextmanager
from pathlib import Path
from typing import Any

from service_lifecycle import (
    INDEPENDENT_PROCESS_FLAGS,
    command_line_for_pid,
    current_listening_pids,
    select_owned_daemon_pid,
)

ROOT = Path(__file__).resolve().parent.parent
DEFAULT_PORT = 30009


def _shared_runtime_dir() -> Path:
    local = os.environ.get("LOCALAPPDATA")
    return Path(local) / "Astrorder" if local else ROOT / ".runtime"


@contextmanager
def _daemon_start_lock(timeout: float = 35):
    path = _shared_runtime_dir() / "daemon-start.lock"
    path.parent.mkdir(parents=True, exist_ok=True)
    handle = path.open("a+b")
    if handle.tell() == 0:
        handle.write(b"\0")
        handle.flush()
    deadline = time.monotonic() + timeout
    acquired = False
    try:
        while True:
            try:
                handle.seek(0)
                if os.name == "nt":
                    import msvcrt

                    msvcrt.locking(handle.fileno(), msvcrt.LK_NBLCK, 1)
                else:
                    import fcntl

                    fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
                acquired = True
                break
            except OSError:
                if time.monotonic() >= deadline:
                    raise RuntimeError("timed out waiting for Session Daemon lifecycle lock") from None
                time.sleep(0.1)
        yield
    finally:
        try:
            if acquired:
                handle.seek(0)
                if os.name == "nt":
                    import msvcrt

                    msvcrt.locking(handle.fileno(), msvcrt.LK_UNLCK, 1)
                else:
                    import fcntl

                    fcntl.flock(handle, fcntl.LOCK_UN)
        finally:
            handle.close()


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


def daemon_launch(*, port: int) -> tuple[list[str], dict[str, str]]:
    environment = os.environ.copy()
    executable = environment.get("ASTRORDER_SESSION_DAEMON_EXECUTABLE")
    if not executable:
        raise RuntimeError("ASTRORDER_SESSION_DAEMON_EXECUTABLE is required; only Go Session Daemon is supported")
    if not environment.get("ASTRORDER_SESSION_DAEMON_SECRET"):
        raise RuntimeError("ASTRORDER_SESSION_DAEMON_SECRET is required")
    binary = Path(executable)
    if not binary.is_absolute() or not binary.is_file():
        raise RuntimeError("Go daemon executable must be an existing absolute path")
    config = environment.get("ASTRORDER_SESSION_DAEMON_CONFIG", "")
    database = environment.get("ASTRORDER_SESSION_DAEMON_DB", "")
    if not config or not Path(config).is_file() or not Path(config).is_absolute():
        raise RuntimeError("Go daemon configuration is missing")
    if not database or not Path(database).is_absolute():
        raise RuntimeError("Go daemon database must be an absolute path")
    if type(port) is not int or not 1 <= port <= 65535:
        raise ValueError("invalid daemon port")
    environment["ASTRORDER_SESSION_DAEMON_PORT"] = str(port)
    environment["ASTRORDER_SESSION_DAEMON_CONNECTOR_SECRET"] = environment.get("ASTRORDER_CONNECTOR_SECRET", "")
    return [str(binary)], environment


def start_daemon(
    *,
    port: int = DEFAULT_PORT,
    metadata_path: Path | None = None,
) -> int:
    with _daemon_start_lock():
        existing = existing_verified_daemon(current_listening_pids(port), command_line_for_pid)
        if existing is not None:
            return existing
        argv, environment = daemon_launch(port=port)
        runtime = ROOT / ".runtime"
        runtime.mkdir(parents=True, exist_ok=True)
        log = (runtime / "session-daemon.log").open("ab", buffering=0)
        proc = subprocess.Popen(
            argv,
            cwd=str(ROOT / "backend"),
            env=environment,
            stdout=log,
            stderr=log,
            creationflags=INDEPENDENT_PROCESS_FLAGS,
        )
        try:
            deadline = time.monotonic() + 30
            while time.monotonic() < deadline:
                if proc.poll() is not None:
                    raise RuntimeError("Session Daemon exited during startup; inspect session-daemon.log")
                listener = existing_verified_daemon(
                    current_listening_pids(port), command_line_for_pid
                )
                if listener is not None:
                    if listener != proc.pid and _parent_pid(listener) != proc.pid:
                        _terminate_spawned(proc)
                    path = metadata_path or runtime / "session-daemon.json"
                    path.write_text(
                        json.dumps({"pid": listener, "port": port, "started_at": _timestamp()}),
                        encoding="utf-8",
                    )
                    return listener
                time.sleep(0.1)
            raise RuntimeError("Session Daemon did not become ready within 30 seconds")
        except Exception:
            _terminate_spawned(proc)
            raise


def _parent_pid(pid: int) -> int | None:
    if os.name != "nt":
        return None
    completed = subprocess.run(
        [
            "powershell",
            "-NoProfile",
            "-Command",
            f"(Get-CimInstance Win32_Process -Filter 'ProcessId = {pid}').ParentProcessId",
        ],
        capture_output=True,
        check=False,
        text=True,
        encoding="utf-8",
        errors="replace",
        creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0),
    )
    try:
        return int(completed.stdout.strip()) if completed.returncode == 0 else None
    except ValueError:
        return None


def _terminate_spawned(process: Any) -> None:
    if process.poll() is not None:
        return
    process.terminate()
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)


def graceful_shutdown(port: int, secret: str, *, confirm_active: bool = False) -> None:
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
            response = await call("daemon.shutdown", {"confirm_active": confirm_active})
            if response.get("action") == "error":
                raise RuntimeError(response.get("detail") or "daemon shutdown was refused")
            if response.get("result") != {"stopping": True}:
                raise RuntimeError("daemon graceful shutdown was not confirmed")

    asyncio.run(request())


def stop_daemon(
    *,
    port: int = DEFAULT_PORT,
    secret: str | None = None,
    metadata_path: Path | None = None,
    confirm_active: bool = False,
) -> int | None:
    pid = existing_verified_daemon(current_listening_pids(port), command_line_for_pid)
    if pid is None:
        return None
    graceful_shutdown(port, secret or "", confirm_active=confirm_active)
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
