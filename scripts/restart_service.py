"""Restart only a verified Astrorder App Server; never touch Session Daemon."""
from __future__ import annotations

import subprocess
import time
from pathlib import Path

from service_lifecycle import (
    command_line_for_pid,
    current_listening_pids,
    select_owned_app_pid,
    terminate_verified_pid,
)

ROOT = Path(__file__).resolve().parent.parent
APP_PORT = 30001


def restart() -> int:
    pids = current_listening_pids(APP_PORT)
    if pids:
        pid = select_owned_app_pid(pids, command_line_for_pid)
        if pid is None:
            raise RuntimeError(
                "Refusing restart: port 30001 is not owned by exactly one verified Astrorder App Server."
            )
        terminate_verified_pid(pid)
        deadline = time.monotonic() + 10
        while current_listening_pids(APP_PORT):
            if time.monotonic() >= deadline:
                raise RuntimeError("Verified Astrorder App Server did not release port 30001.")
            time.sleep(0.1)

    log = (ROOT / ".runtime" / "production.log").open("ab", buffering=0)
    python_exe = str(ROOT / "backend/.venv/Scripts/python.exe")
    detached = 0x00000008
    no_window = 0x08000000
    proc = subprocess.Popen(
        [python_exe, "scripts/run_production.py"],
        cwd=str(ROOT),
        stdout=log,
        stderr=log,
        creationflags=detached | no_window,
    )
    return proc.pid


if __name__ == "__main__":
    print(f"Started verified Astrorder production process PID: {restart()}")