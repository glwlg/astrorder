"""Safe process identity helpers for Astrorder's independently managed services."""
from __future__ import annotations

import subprocess
from collections.abc import Callable


APP_MARKER = "scripts/run_production.py"
DAEMON_MODULE_MARKER = "astrorder.daemon.session_daemon"
DAEMON_FILE_MARKER = "astrorder/daemon/session_daemon.py"
INDEPENDENT_PROCESS_FLAGS = 0x00000008 | 0x08000000 | 0x01000000


def listening_pids(netstat_output: str, port: int) -> list[int]:
    """Return PIDs only for exact local TCP LISTENING endpoints on *port*."""
    if not isinstance(port, int) or not 1 <= port <= 65535:
        raise ValueError("port must be between 1 and 65535")
    result: set[int] = set()
    for raw in netstat_output.splitlines():
        fields = raw.split()
        if len(fields) < 5 or fields[-2].upper() != "LISTENING":
            continue
        local = fields[-4]
        try:
            local_port = int(local.rsplit(":", 1)[1])
            pid = int(fields[-1])
        except (IndexError, ValueError):
            continue
        if local_port == port and pid > 0:
            result.add(pid)
    return sorted(result)


def select_owned_app_pid(pids: list[int], command_line: Callable[[int], str | None]) -> int | None:
    """Return a sole verified Astrorder App Server PID, never a guess."""
    if len(pids) != 1:
        return None
    command = command_line(pids[0])
    if _contains_marker(command, APP_MARKER):
        return pids[0]
    return None


def select_owned_daemon_pid(pids: list[int], command_line: Callable[[int], str | None]) -> int | None:
    """Return a sole verified Session Daemon PID, never the App Server."""
    if len(pids) != 1:
        return None
    command = _normalized(command_line(pids[0]))
    if DAEMON_MODULE_MARKER in command or DAEMON_FILE_MARKER in command:
        return pids[0]
    return None


def current_listening_pids(port: int) -> list[int]:
    completed = subprocess.run(
        ["netstat", "-ano"],
        capture_output=True,
        check=False,
        text=True,
        encoding="utf-8",
        errors="replace",
    )
    return listening_pids(completed.stdout, port)


def command_line_for_pid(pid: int) -> str | None:
    """Read a Windows process command line without logging potentially secret args."""
    if not isinstance(pid, int) or pid <= 0:
        return None
    try:
        completed = subprocess.run(
            ["wmic", "process", "where", f"ProcessId={pid}", "get", "CommandLine", "/value"],
            capture_output=True,
            check=False,
            text=True,
            encoding="utf-8",
            errors="replace",
        )
    except OSError:
        return None
    if completed.returncode != 0:
        return None
    for line in completed.stdout.splitlines():
        if line.startswith("CommandLine="):
            value = line.partition("=")[2].strip()
            return value or None
    return None


def terminate_verified_pid(pid: int) -> None:
    """Terminate exactly one already-verified PID; never use /T process-tree kill."""
    subprocess.run(["taskkill", "/F", "/PID", str(pid)], check=True, capture_output=True)


def _contains_marker(command: str | None, marker: str) -> bool:
    return marker in _normalized(command)


def _normalized(command: str | None) -> str:
    return (command or "").replace("\\", "/").lower()
