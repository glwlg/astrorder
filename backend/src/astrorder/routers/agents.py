from __future__ import annotations

import json
import subprocess
from typing import Any

from fastapi import APIRouter, HTTPException, Request
from fastapi.responses import StreamingResponse

router = APIRouter()


def _private(request: Request) -> None:
    from ..auth import require_browser
    require_browser(request)


@router.get("/api/v1/agents")
def agents(request: Request) -> dict[str, object]:
    _private(request)
    from ..agent_registry import current_agents
    return {"items": [request.app.state.service.effective_agent(a) for a in current_agents(request.app.state.store)]}


@router.get("/api/v1/agents/{agent_id}/observations")
def observations(agent_id: str, request: Request, session_id: str | None = None):
    _private(request)
    if request.app.state.store.get_agent(agent_id) is None:
        raise HTTPException(status_code=404, detail="Agent not found")
    observer = request.app.state.observers
    return {"status": observer.status(agent_id), "items": observer.recent(agent_id, session_id)}


@router.post("/api/v1/agents/{agent_id}/observer")
def install_observer(agent_id: str, request: Request):
    _private(request)
    try:
        from ..observer_plugin import ensure_observer_plugin
        ensure_observer_plugin(agent_id)
        return {"ok": True}
    except Exception as exc:
        raise HTTPException(status_code=500, detail=str(exc)) from exc


@router.post("/api/v1/agents/{agent_id}/upgrade")
def upgrade_agent(agent_id: str, request: Request) -> StreamingResponse:
    """统一 Agent 一键升级管理（流式输出 + 后台可取消 + 系统终端静默隐藏）。"""
    _private(request)
    store = request.app.state.store
    agent = store.get_agent(agent_id)
    if not agent:
        raise HTTPException(status_code=404, detail="Agent 不存在")

    kind = str(agent.get("kind") or "").lower()
    conn_id = agent.get("connection_id")

    from ..connections import _windows_hide_flags, _windows_hide_startupinfo

    # 1. 远程 SSH 机器上的升级逻辑
    if conn_id and conn_id != "local":
        ssh_conn = store.get_ssh_connection(conn_id)
        if not ssh_conn:
            raise HTTPException(status_code=404, detail="关联的 SSH 环境连接不存在")
        settings = ssh_conn.get("settings") or {}
        user = settings.get("user") or ssh_conn.get("user") or "root"
        host = settings.get("host") or ssh_conn.get("host") or "127.0.0.1"
        port = str(settings.get("port") or ssh_conn.get("port") or 22)
        identity_file = settings.get("identity_file") or ssh_conn.get("identity_file")
        ssh_alias = settings.get("ssh_config_alias") or ssh_conn.get("ssh_config_alias")

        if "codex" in kind:
            cmd = "vp install -g @openai/codex@latest || npm install -g @openai/codex@latest"
        elif "hermes" in kind:
            cmd = "hermes update"
        elif "grok" in kind:
            cmd = "curl -fsSL https://x.ai/cli/install.sh | bash"
        else:
            raise HTTPException(status_code=400, detail=f"暂不支持升级类型为 {kind} 的 Agent")

        remote_cmd = ["ssh", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=ask", "-o", "ConnectTimeout=10"]
        if identity_file:
            remote_cmd.extend(["-i", str(identity_file)])
        if ssh_alias:
            target = ssh_alias
        else:
            remote_cmd.extend(["-p", port])
            target = f"{user}@{host}"
        remote_cmd.append(target)
        remote_cmd.append(f"export PATH=$PATH:$HOME/.vite-plus/bin:$HOME/.local/bin:$HOME/.grok/bin; {cmd}")
        run_cmd = remote_cmd
        display_cmd = cmd
    else:
        # 2. 本机 Windows / 环境升级逻辑
        import sys
        is_win = sys.platform == "win32"
        if "codex" in kind:
            run_cmd = ["vp.cmd", "install", "-g", "@openai/codex@latest"] if is_win else ["vp", "install", "-g", "@openai/codex@latest"]
            display_cmd = "vp install -g @openai/codex@latest"
        elif "hermes" in kind:
            run_cmd = ["hermes.cmd", "update"] if is_win else ["hermes", "update"]
            display_cmd = "hermes update"
        elif "grok" in kind:
            if is_win:
                run_cmd = ["powershell", "-NoProfile", "-Command", "irm https://x.ai/cli/install.ps1 | iex"]
                display_cmd = "powershell: irm https://x.ai/cli/install.ps1 | iex"
            else:
                run_cmd = ["bash", "-c", "curl -fsSL https://x.ai/cli/install.sh | bash"]
                display_cmd = "curl -fsSL https://x.ai/cli/install.sh | bash"
        else:
            raise HTTPException(status_code=400, detail=f"暂不支持升级类型为 {kind} 的 Agent")

    def stream_upgrade():
        yield json.dumps({"type": "init", "command": display_cmd}, ensure_ascii=False) + "\n"
        try:
            proc = subprocess.Popen(
                run_cmd,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
                encoding="utf-8",
                errors="replace",
                bufsize=1,
                creationflags=_windows_hide_flags(),
                startupinfo=_windows_hide_startupinfo(),
            )
            if proc.stdout is not None:
                for line in proc.stdout:
                    yield json.dumps({"type": "chunk", "data": line}, ensure_ascii=False) + "\n"
            proc.wait(timeout=300)
            yield json.dumps({"type": "done", "ok": proc.returncode == 0, "exit_code": proc.returncode}, ensure_ascii=False) + "\n"
        except Exception as exc:
            yield json.dumps({"type": "error", "error": str(exc)}, ensure_ascii=False) + "\n"

    return StreamingResponse(stream_upgrade(), media_type="application/x-ndjson")
