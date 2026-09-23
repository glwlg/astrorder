"""JSON service controls used by the Electron main process."""
from __future__ import annotations

import argparse
import asyncio
import json
import os
import subprocess
import sys
import time
from typing import Any
from urllib.request import Request, urlopen

from daemon_service import (
    command_line_for_pid,
    current_listening_pids,
    stop_daemon,
)
from daemon_service import (
    start_daemon as start_independent_daemon,
)
from production_daemon import production_daemon_spec
from run_production import ROOT, load_production_environment
from service_lifecycle import (
    INDEPENDENT_PROCESS_FLAGS,
    select_owned_app_pid,
    select_owned_daemon_pid,
    terminate_verified_pid,
)

STARTUP_TASK_NAME = "AstrorderSessionDaemon"


def _health(port: int) -> bool:
    try:
        with urlopen(f"http://127.0.0.1:{port}/health", timeout=2) as response:
            body = json.load(response)
        return response.status == 200 and body == {
            "status": "ok",
            "service": "astrorder",
            "protocol_version": 1,
        }
    except (OSError, ValueError):
        return False


def _state(port: int, selector) -> dict[str, Any]:
    pids = current_listening_pids(port)
    if not pids:
        return {"state": "stopped", "port": port}
    pid = selector(pids, command_line_for_pid)
    if pid is None:
        return {"state": "conflict", "port": port}
    return {"state": "running", "port": port, "pid": pid}


def app_status(port: int) -> dict[str, Any]:
    result = _state(port, select_owned_app_pid)
    if result["state"] == "running" and not _health(port):
        result["state"] = "unhealthy"
    return result


async def _daemon_ipc_status(port: int, secret: str) -> dict[str, Any]:
    import websockets

    async with websockets.connect(
        f"ws://127.0.0.1:{port}", open_timeout=2, close_timeout=2
    ) as socket:
        await socket.send(json.dumps({
            "action": "daemon.handshake",
            "request_id": "desktop-handshake",
            "secret": secret,
        }))
        handshake = json.loads(await asyncio.wait_for(socket.recv(), timeout=2))
        if handshake.get("action") != "daemon.handshake.result":
            raise RuntimeError("Session Daemon authentication failed")
        await socket.send(json.dumps({
            "action": "daemon.status",
            "request_id": "desktop-status",
        }))
        status = json.loads(await asyncio.wait_for(socket.recv(), timeout=2))
        if status.get("action") != "daemon.status.result":
            raise RuntimeError("Session Daemon status is unavailable")
        return status


def daemon_status(port: int, secret: str) -> dict[str, Any]:
    result = _state(port, select_owned_daemon_pid)
    result["startup_task"] = startup_task_state()
    if result["state"] != "running":
        return result
    try:
        status = asyncio.run(_daemon_ipc_status(port, secret))
    except Exception:  # noqa: BLE001 - every IPC failure maps to an unhealthy daemon.
        result["state"] = "unhealthy"
        return result
    sessions = status.get("sessions", {})
    active = {
        session_id: data.get("status")
        for session_id, data in sessions.items()
        if isinstance(data, dict)
        and data.get("status") in {"running", "waiting_approval"}
    }
    result.update(
        daemon_id=status.get("daemon_id"),
        session_count=len(sessions),
        active_sessions=active,
    )
    return result


def _task_scheduler():
    import win32com.client

    scheduler = win32com.client.Dispatch("Schedule.Service")
    scheduler.Connect()
    return scheduler


def startup_task_state() -> str:
    try:
        _task_scheduler().GetFolder("\\").GetTask(STARTUP_TASK_NAME)
    except Exception as exc:  # noqa: BLE001 - COM exposes HRESULTs through dynamic errors.
        details = exc.args[2] if len(exc.args) > 2 and isinstance(exc.args[2], tuple) else ()
        hresult = details[5] if len(details) > 5 else getattr(exc, "hresult", 0)
        if hresult & 0xFFFFFFFF == 0x80070002:
            return "not_installed"
        return "unknown"
    return "installed"


def install_startup_task() -> None:
    domain = os.environ.get("USERDOMAIN")
    username = os.environ.get("USERNAME")
    if not domain or not username:
        raise RuntimeError("无法识别当前 Windows 用户")
    user = f"{domain}\\{username}"
    scheduler = _task_scheduler()
    folder = scheduler.GetFolder("\\")
    task = scheduler.NewTask(0)
    task.RegistrationInfo.Description = "登录 Windows 后启动星序小内核。"
    task.Principal.UserId = user
    task.Principal.LogonType = 3  # TASK_LOGON_INTERACTIVE_TOKEN
    task.Principal.RunLevel = 0  # TASK_RUNLEVEL_LUA
    task.Settings.Enabled = True
    task.Settings.StartWhenAvailable = True
    task.Settings.DisallowStartIfOnBatteries = False
    task.Settings.StopIfGoingOnBatteries = False
    task.Settings.ExecutionTimeLimit = "PT0S"
    task.Settings.MultipleInstances = 2  # TASK_INSTANCES_IGNORE_NEW
    trigger = task.Triggers.Create(9)  # TASK_TRIGGER_LOGON
    trigger.UserId = user
    action = task.Actions.Create(0)  # TASK_ACTION_EXEC
    action.Path = str(ROOT / "backend/.venv/Scripts/pythonw.exe")
    action.Arguments = f'"{ROOT / "scripts/start_desktop_daemon.py"}"'
    action.WorkingDirectory = str(ROOT)
    folder.RegisterTaskDefinition(STARTUP_TASK_NAME, task, 6, user, None, 3)


def start_daemon(environment: dict[str, str], port: int, secret: str) -> dict[str, Any]:
    current = daemon_status(port, secret)
    if current["state"] == "running":
        return current
    _, runtime_args = production_daemon_spec(environment)
    start_independent_daemon(port=port, runtime_args=runtime_args)
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        status = daemon_status(port, secret)
        if status["state"] == "running":
            return status
        if status["state"] == "conflict":
            raise RuntimeError("Session Daemon port is occupied by another process")
        time.sleep(0.1)
    raise RuntimeError("Session Daemon did not become ready within 30 seconds")


def start_app(port: int) -> dict[str, Any]:
    status = app_status(port)
    if status["state"] == "running":
        return status
    if status["state"] != "stopped":
        raise RuntimeError("App Server port is occupied or unhealthy")
    runtime = ROOT / ".runtime"
    runtime.mkdir(exist_ok=True)
    log = (runtime / "production.log").open("ab", buffering=0)
    process = subprocess.Popen(
        [str(ROOT / "backend/.venv/Scripts/pythonw.exe"), "scripts/run_production.py"],
        cwd=ROOT,
        stdout=log,
        stderr=log,
        creationflags=INDEPENDENT_PROCESS_FLAGS,
    )
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError("App Server exited during startup; inspect production.log")
        status = app_status(port)
        if status["state"] == "running":
            return status
        time.sleep(0.1)
    raise RuntimeError("App Server did not become ready within 30 seconds")


def stop_app(port: int, browser_secret: str) -> dict[str, Any]:
    status = app_status(port)
    if status["state"] == "stopped":
        return status
    pid = status.get("pid")
    if not isinstance(pid, int):
        raise TypeError("Refusing to stop an unverified App Server")
    if select_owned_app_pid(current_listening_pids(port), command_line_for_pid) != pid:
        raise RuntimeError("App Server identity changed before stop")
    request = Request(
        f"http://127.0.0.1:{port}/_desktop/shutdown",
        method="POST",
        headers={"Authorization": f"Bearer {browser_secret}"},
    )
    try:
        with urlopen(request, timeout=3) as response:
            confirmed = json.load(response)
    except OSError as exc:
        raise RuntimeError("App Server did not accept graceful shutdown") from exc
    if response.status != 200 or confirmed != {"stopping": True}:
        raise RuntimeError("App Server did not confirm graceful shutdown")
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        status = app_status(port)
        if status["state"] == "stopped":
            for _ in range(20):
                if command_line_for_pid(pid) is None:
                    return status
                time.sleep(0.1)
            if select_owned_app_pid([pid], command_line_for_pid) != pid:
                raise RuntimeError("App Server identity changed during shutdown")
            terminate_verified_pid(pid)
            return status
        time.sleep(0.1)
    raise RuntimeError("App Server did not stop within 10 seconds")


def execute(target: str, action: str, *, confirm_active: bool = False) -> dict[str, Any]:
    environment = load_production_environment()
    app_port = int(environment.get("ASTRORDER_PORT", "30001"))
    daemon_port, _ = production_daemon_spec(environment)
    daemon_secret = os.environ["ASTRORDER_SESSION_DAEMON_SECRET"]
    browser_secret = os.environ["ASTRORDER_BROWSER_SECRET"]
    if target == "app":
        if action == "status":
            result = app_status(app_port)
        elif action == "start":
            daemon = daemon_status(daemon_port, daemon_secret)
            if daemon["state"] == "stopped":
                start_daemon(environment, daemon_port, daemon_secret)
            elif daemon["state"] != "running":
                raise RuntimeError("Session Daemon is not ready")
            result = start_app(app_port)
        elif action == "stop":
            result = stop_app(app_port, browser_secret)
        else:
            daemon = daemon_status(daemon_port, daemon_secret)
            if daemon["state"] != "running":
                raise RuntimeError("Session Daemon is not ready")
            stop_app(app_port, browser_secret)
            result = start_app(app_port)
    else:
        if action == "install":
            install_startup_task()
            result = start_daemon(environment, daemon_port, daemon_secret)
        elif action == "status":
            result = daemon_status(daemon_port, daemon_secret)
        elif action == "start":
            result = start_daemon(environment, daemon_port, daemon_secret)
        elif action == "stop":
            stop_daemon(
                port=daemon_port,
                secret=daemon_secret,
                confirm_active=confirm_active,
            )
            result = daemon_status(daemon_port, daemon_secret)
        else:
            stop_daemon(
                port=daemon_port,
                secret=daemon_secret,
                confirm_active=confirm_active,
            )
            result = start_daemon(environment, daemon_port, daemon_secret)
    return {"ok": True, "target": target, "action": action, **result}


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("target", choices=("app", "daemon"))
    parser.add_argument("action", choices=("status", "start", "stop", "restart", "install"))
    parser.add_argument("--confirm-active", action="store_true")
    args = parser.parse_args(argv)
    try:
        result = execute(args.target, args.action, confirm_active=args.confirm_active)
    except Exception as exc:  # noqa: BLE001 - CLI boundary returns stable JSON to Electron.
        result = {
            "ok": False,
            "target": args.target,
            "action": args.action,
            "state": "unknown",
            "message": str(exc),
        }
    print(json.dumps(result, ensure_ascii=False, separators=(",", ":")))
    return 0 if result["ok"] else 1


if __name__ == "__main__":
    sys.exit(main())
