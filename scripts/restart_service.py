"""Restart only a verified Astrorder App Server; never touch Session Daemon."""
from __future__ import annotations

import subprocess
import time
from pathlib import Path

from service_lifecycle import (
    INDEPENDENT_PROCESS_FLAGS,
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
    import os
    child_env = dict(os.environ)
    child_env.pop("ASTRORDER_DESKTOP_PID", None)
    subprocess.Popen(
        [python_exe, "scripts/run_production.py"],
        cwd=str(ROOT),
        stdout=log,
        stderr=log,
        env=child_env,
        creationflags=INDEPENDENT_PROCESS_FLAGS,
    )
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        pids = current_listening_pids(APP_PORT)
        if pids:
            pid = select_owned_app_pid(pids, command_line_for_pid)
            if pid is None:
                raise RuntimeError("Started process did not become the verified Astrorder App Server.")
            return pid
        time.sleep(0.1)
    raise RuntimeError("Astrorder App Server did not become ready within 30 seconds; inspect .runtime/production.log.")


if __name__ == "__main__":
    print(f"Started verified Astrorder production process PID: {restart()}")
