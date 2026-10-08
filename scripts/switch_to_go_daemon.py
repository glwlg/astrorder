#!/usr/bin/env python3
"""
平滑切流脚本：将当前运行的 Python 小内核切换至高性能 Go 小内核。
符合 AGENTS.md 约束：
1. 切流前严格校验权威状态（通过 daemon.status 检查 active/busy/waiting_approval）。
2. 若存在运行中或待审批会话，严禁直接重启/切流。
3. 支持预检查、优雅停止旧内核、启动 Go 新内核并验证就绪。
"""
from __future__ import annotations

import argparse
import asyncio
import json
import os
import subprocess
import sys
import time
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
BACKEND_ROOT = REPO_ROOT / "backend"
SCRIPTS_ROOT = REPO_ROOT / "scripts"

sys.path.insert(0, str(SCRIPTS_ROOT))
from service_lifecycle import current_listening_pids, command_line_for_pid, select_owned_daemon_pid
from daemon_service import graceful_shutdown

GO_BINARY_DEFAULT = REPO_ROOT / "session-daemon-go" / ".runtime" / "astrorder-sessiond.exe"

async def probe_daemon_authoritative_idle(port: int, secret: str) -> dict:
    import websockets

    async with websockets.connect(f"ws://127.0.0.1:{port}/") as ws:
        await ws.send(json.dumps({"action": "daemon.handshake", "secret": secret}))
        raw_resp = await asyncio.wait_for(ws.recv(), timeout=3.0)
        resp = json.loads(raw_resp)
        if resp.get("action") != "daemon.handshake.result" or resp.get("status") == "error":
            raise RuntimeError(f"握手失败: {resp}")
        await ws.send(json.dumps({"action": "daemon.status", "request_id": "status_probe"}))
        raw_status = await asyncio.wait_for(ws.recv(), timeout=3.0)
        status_result = json.loads(raw_status)

    if status_result.get("action") != "daemon.status.result":
        raise RuntimeError(f"旧内核状态响应异常: {status_result}")

    sessions = status_result.get("sessions")
    if not isinstance(sessions, dict):
        raise RuntimeError(f"旧内核状态响应缺少合法的 'sessions' 字典: {status_result}")

    active_count = 0
    waiting_approval_count = 0
    active_session_ids = []

    for sid, sdata in sessions.items():
        if not isinstance(sdata, dict):
            raise RuntimeError(f"旧内核会话 {sid!r} 状态格式无效")
        st = sdata.get("status")
        if st in ("running", "waiting_approval"):
            active_count += 1
            active_session_ids.append(sid)
            if st == "waiting_approval":
                waiting_approval_count += 1
        elif st != "idle":
            # 非 idle 状态（如 unknown, error, transitional）按保守安全原则视作潜在活动会话
            active_count += 1
            active_session_ids.append(sid)

    return {
        "sessions": sessions,
        "active_sessions": active_count,
        "waiting_approvals": waiting_approval_count,
        "active_session_ids": active_session_ids,
        "daemon_id": status_result.get("daemon_id"),
        "runtimes": status_result.get("runtimes", {}),
    }

def check_daemon_authoritative_idle(port: int, secret: str) -> dict:
    try:
        try:
            loop = asyncio.get_running_loop()
        except RuntimeError:
            loop = None
        if loop and loop.is_running():
            import concurrent.futures
            with concurrent.futures.ThreadPoolExecutor(max_workers=1) as pool:
                return pool.submit(asyncio.run, probe_daemon_authoritative_idle(port, secret)).result()
        else:
            return asyncio.run(probe_daemon_authoritative_idle(port, secret))
    except Exception as exc:
        raise RuntimeError(f"无法安全获取旧内核权威状态 (依据 AGENTS.md 约束，状态不明时强行终止): {exc}") from exc

def main():
    parser = argparse.ArgumentParser(description="Astrorder 小内核 Go 生产整版切流工具")
    parser.add_argument("--port", type=int, default=30009, help="小内核监听端口 (默认: 30009)")
    parser.add_argument("--secret", default=os.getenv("ASTRORDER_SESSION_DAEMON_SECRET"), help="小内核管理密钥")
    parser.add_argument("--go-bin", default=str(GO_BINARY_DEFAULT), help="Go 小内核二进制路径")
    parser.add_argument("--db-path", default=os.getenv("ASTRORDER_SESSION_DAEMON_DB"), help="小内核 SQLite 数据库路径")
    parser.add_argument("--config-path", default=os.getenv("ASTRORDER_SESSION_DAEMON_CONFIG"), help="小内核配置文件路径")
    parser.add_argument("--force", action="store_true", help="强制切流（忽略活动会话检查）")
    args = parser.parse_args()

    go_bin = Path(args.go_bin)
    if not go_bin.is_file():
        print(f"[ERROR] 未找到 Go 小内核可执行文件: {go_bin}")
        print("请先执行编译: go build -o .runtime/astrorder-sessiond.exe ./cmd/astrorder-sessiond")
        sys.exit(1)

    # Validate the replacement before touching the currently running daemon.
    if not args.secret:
        parser.error("ASTRORDER_SESSION_DAEMON_SECRET is required")
    if not args.db_path or not Path(args.db_path).is_absolute():
        parser.error("Go daemon database must be an absolute path")
    if not args.config_path or not Path(args.config_path).is_absolute() or not Path(args.config_path).is_file():
        parser.error("Go daemon configuration is missing")
    if not go_bin.is_absolute() or not 1 <= args.port <= 65535:
        parser.error("Go binary must be absolute and port must be between 1 and 65535")
    with Path(args.config_path).open(encoding="utf-8") as config_file:
        if not isinstance(json.load(config_file), dict):
            parser.error("Go daemon configuration must be a JSON object")

    print("=== Astrorder 小内核平滑切换至 Go 运行时 ===")
    print(f"目标端口: {args.port}")
    print(f"Go 二进制: {go_bin}")

    # 1. 探活当前运行的 Daemon PID
    pids = current_listening_pids(args.port)
    if pids:
        pid = select_owned_daemon_pid(pids, command_line_for_pid)
        if pid is None:
            parser.error("listener is not a verified Astrorder Session Daemon")
        print(f"检测到正在运行的 Session Daemon PID: {pids} (已验证归属: {pid})")

        # 2. 状态检查
        if not args.secret:
            print("[ABORT] 未提供 ASTRORDER_SESSION_DAEMON_SECRET，无法进行权威状态对账，严禁切流！")
            sys.exit(1)
        st = check_daemon_authoritative_idle(args.port, args.secret)
        active_sessions = st.get("active_sessions", 0)
        waiting_approvals = st.get("waiting_approvals", 0)
        active_ids = st.get("active_session_ids", [])
        print(f"当前小内核状态: active_sessions={active_sessions}, waiting_approvals={waiting_approvals}")
        if (active_sessions > 0 or waiting_approvals > 0) and not args.force:
            print(f"[ABORT] 检测到当前存在运行中或等待审批的会话 (活动会话列表: {active_ids})，依据 AGENTS.md 约束，严禁重启/切换！")
            print("如需强制切换，请取得用户明确同意后附加 --force 参数。")
            sys.exit(1)

        # 3. 优雅停止旧 Daemon
        print("正在停止旧版本 Session Daemon...")
        if args.secret:
            try:
                graceful_shutdown(args.port, args.secret, confirm_active=args.force)
                time.sleep(1.0)
            except Exception as e:
                print(f"[ERROR] 优雅停止失败: {e}")
                print("依据 AGENTS.md 约束，旧内核未能完全停稳前，禁止启动新内核！")
                sys.exit(1)

    # 4. 准备启动 Go 小内核
    env = os.environ.copy()
    if args.secret:
        env["ASTRORDER_SESSION_DAEMON_SECRET"] = args.secret
    if args.db_path:
        env["ASTRORDER_SESSION_DAEMON_DB"] = args.db_path
    if args.config_path:
        env["ASTRORDER_SESSION_DAEMON_CONFIG"] = args.config_path
    env["ASTRORDER_SESSION_DAEMON_PORT"] = str(args.port)

    # 启动进程 (Windows CREATE_NO_WINDOW | CREATE_BREAKAWAY_FROM_JOB)
    flags = 0x08000000 | 0x01000000 | 0x00000200 if sys.platform == "win32" else 0
    print(f"启动 Go 小内核: {go_bin} ...")
    proc = subprocess.Popen(
        [str(go_bin)],
        env=env,
        creationflags=flags,
        close_fds=True,
    )
    print(f"Go 小内核已启动，PID: {proc.pid}")

    # 5. 校验新内核就绪
    print("正在验证 Go 小内核就绪状态...")
    deadline = time.monotonic() + 5.0
    new_ready = False
    new_status = None
    while time.monotonic() < deadline:
        if proc.poll() is not None:
            print(f"[ERROR] Go 小内核意外退出，退出码: {proc.returncode}")
            sys.exit(1)
        try:
            new_status = check_daemon_authoritative_idle(args.port, args.secret)
            new_ready = True
            break
        except Exception:
            time.sleep(0.2)

    if new_ready and new_status:
        print(f"[SUCCESS] Go 小内核已成功接管端口 {args.port}，epoch/daemon_id: {new_status.get('daemon_id')}，状态对账通过！")
    else:
        new_pids = current_listening_pids(args.port)
        print(f"[ERROR] Go 小内核握手探活超时，当前端口监听 PID: {new_pids}")
        sys.exit(1)

if __name__ == "__main__":
    main()
