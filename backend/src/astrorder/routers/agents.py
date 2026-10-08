from __future__ import annotations

import json
import os
import subprocess
from typing import Any

from fastapi import APIRouter, HTTPException, Request
from fastapi.responses import StreamingResponse
from pydantic import BaseModel

router = APIRouter()


def _private(request: Request) -> None:
    from ..core.auth import require_browser
    try:
        require_browser(request, request.app.state.settings)
    except Exception as e:
        import logging
        logging.getLogger("astrorder").exception("Auth failed in agents router: %s", e)
        raise


@router.get("/api/v1/agents")
def agents(request: Request) -> dict[str, object]:
    _private(request)
    from ..agents.registry import current_agents
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
        from ..observers.plugin import ensure_observer_plugin
        ensure_observer_plugin(agent_id)
        return {"ok": True}
    except Exception as exc:
        raise HTTPException(status_code=500, detail=str(exc)) from exc


@router.post("/api/v1/agents/{agent_id}/upgrade")
def upgrade_agent(agent_id: str, request: Request) -> StreamingResponse:
    """统一 Agent 一键升级管理（流式输出 + 后台可取消 + 系统终端静默隐藏）。"""
    try:
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
            import shutil
            import sys
            is_win = sys.platform == "win32"

            def _resolve_local_bin(name: str) -> str:
                found = shutil.which(name)
                if found:
                    return found
                if is_win:
                    for ext in [".exe", ".cmd", ".bat"]:
                        found = shutil.which(name + ext)
                        if found:
                            return found
                extra_dirs = [
                    os.path.expanduser("~/.vite-plus/bin"),
                    os.path.expanduser("~/AppData/Local/hermes/bin"),
                    os.path.expanduser("~/.grok/bin"),
                    os.path.expanduser("~/.local/bin"),
                ]
                path_env = os.pathsep.join(extra_dirs + [os.environ.get("PATH", "")])
                found = shutil.which(name, path=path_env)
                if found:
                    return found
                if is_win:
                    for ext in [".exe", ".cmd", ".bat"]:
                        found = shutil.which(name + ext, path=path_env)
                        if found:
                            return found
                return name

            if "codex" in kind:
                pkg_mgr = _resolve_local_bin("vp")
                if pkg_mgr == "vp" and is_win:
                    # 尝试 npm 兜底
                    pkg_mgr = _resolve_local_bin("npm")
                run_cmd = [pkg_mgr, "install", "-g", "@openai/codex@latest"]
                display_cmd = f"{os.path.basename(pkg_mgr)} install -g @openai/codex@latest"
            elif "hermes" in kind:
                hermes_bin = _resolve_local_bin("hermes")
                run_cmd = [hermes_bin, "update", "--yes"]
                display_cmd = "hermes update --yes"
            elif "grok" in kind:
                if is_win:
                    run_cmd = ["powershell", "-NoProfile", "-Command", "irm https://x.ai/cli/install.ps1 | iex"]
                    display_cmd = "powershell: irm https://x.ai/cli/install.ps1 | iex"
                else:
                    run_cmd = ["bash", "-c", "curl -fsSL https://x.ai/cli/install.sh | bash"]
                    display_cmd = "curl -fsSL https://x.ai/cli/install.sh | bash"
            else:
                raise HTTPException(status_code=400, detail=f"暂不支持升级类型为 {kind} 的 Agent")
    except Exception as exc:
        import logging
        logging.getLogger("astrorder").exception("Failed to prepare agent upgrade: %s", exc)
        if isinstance(exc, HTTPException):
            raise
        raise HTTPException(status_code=500, detail=f"准备升级任务失败: {exc}") from exc

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


class BatchUpgradeRequest(BaseModel):
    environment_id: str | None = None  # None / "all" 则全节点升级，否则指定单一主机节点


def _resolve_local_bin(name: str) -> str:
    import shutil
    import sys
    is_win = sys.platform == "win32"
    found = shutil.which(name)
    if found:
        return found
    if is_win:
        for ext in [".exe", ".cmd", ".bat"]:
            found = shutil.which(name + ext)
            if found:
                return found
    extra_dirs = [
        os.path.expanduser("~/.vite-plus/bin"),
        os.path.expanduser("~/AppData/Local/hermes/bin"),
        os.path.expanduser("~/.grok/bin"),
        os.path.expanduser("~/.local/bin"),
    ]
    path_env = os.pathsep.join(extra_dirs + [os.environ.get("PATH", "")])
    found = shutil.which(name, path=path_env)
    if found:
        return found
    if is_win:
        for ext in [".exe", ".cmd", ".bat"]:
            found = shutil.which(name + ext, path=path_env)
            if found:
                return found
    return name


def _get_agent_version(kind: str, conn_id: str | None, store: Any) -> tuple[str, bool]:
    """快速获取 Agent 当前版本号及是否已经是最新（up to date）。"""
    import re
    from ..connections import _windows_hide_flags, _windows_hide_startupinfo
    if conn_id and conn_id != "local":
        ssh_conn = store.get_ssh_connection(conn_id)
        if not ssh_conn:
            return ("未知", False)
        settings = ssh_conn.get("settings") or {}
        user = settings.get("user") or ssh_conn.get("user") or "root"
        host = settings.get("host") or ssh_conn.get("host") or "127.0.0.1"
        port = str(settings.get("port") or ssh_conn.get("port") or 22)
        identity_file = settings.get("identity_file") or ssh_conn.get("identity_file")
        ssh_alias = settings.get("ssh_config_alias") or ssh_conn.get("ssh_config_alias")

        cmd = "codex --version" if "codex" in kind else "hermes --version" if "hermes" in kind else "grok --version"
        remote_cmd = ["ssh", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=ask", "-o", "ConnectTimeout=4"]
        if identity_file:
            remote_cmd.extend(["-i", str(identity_file)])
        if ssh_alias:
            remote_cmd.append(ssh_alias)
        else:
            remote_cmd.extend(["-p", port, f"{user}@{host}"])
        remote_cmd.append(f"export PATH=$PATH:$HOME/.vite-plus/bin:$HOME/.local/bin:$HOME/.grok/bin; {cmd}")
        try:
            res = subprocess.run(
                remote_cmd,
                capture_output=True,
                text=True,
                timeout=5,
                creationflags=_windows_hide_flags(),
                startupinfo=_windows_hide_startupinfo(),
            )
            out = (res.stdout or res.stderr or "").strip()
            is_up_to_date = "Up to date" in out or "up to date" in out
            has_update_flag = "Update available" in out or "update available" in out
            if "hermes" in kind:
                # 提取语义版本号与 commit/tag 信息
                # 优先提取形如 0.21.5 或 2026.9.24
                m_v = re.search(r"v(\d+\.\d+(?:\.\d+)?)", out)
                m_tag = re.search(r"\((\d{4}\.\d+\.\d+)\)", out)
                ver_display = f"{m_v.group(1)} ({m_tag.group(1)})" if (m_v and m_tag) else (m_tag.group(1) if m_tag else (m_v.group(1) if m_v else None))
                if ver_display:
                    return (ver_display, is_up_to_date and not has_update_flag)
            m = re.search(r"(\d+\.\d+(?:\.\d+)?(?:[a-zA-Z0-9_\-\.]+)?|\d{4}\.\d+\.\d+)", out)
            ver = m.group(1) if m else (out.splitlines()[0] if out else "未知")
            return (ver, is_up_to_date and not has_update_flag)
        except Exception:
            return ("未知", False)
    else:
        bin_name = "codex" if "codex" in kind else "hermes" if "hermes" in kind else "grok"
        real_bin = _resolve_local_bin(bin_name)
        try:
            res = subprocess.run(
                [real_bin, "--version"],
                capture_output=True,
                text=True,
                timeout=4,
                creationflags=_windows_hide_flags(),
                startupinfo=_windows_hide_startupinfo(),
            )
            out = (res.stdout or res.stderr or "").strip()
            is_up_to_date = "Up to date" in out or "up to date" in out
            has_update_flag = "Update available" in out or "update available" in out
            if "hermes" in kind:
                m_v = re.search(r"v(\d+\.\d+(?:\.\d+)?)", out)
                m_tag = re.search(r"\((\d{4}\.\d+\.\d+)\)", out)
                ver_display = f"{m_v.group(1)} ({m_tag.group(1)})" if (m_v and m_tag) else (m_tag.group(1) if m_tag else (m_v.group(1) if m_v else None))
                if ver_display:
                    return (ver_display, is_up_to_date and not has_update_flag)
            m = re.search(r"(\d+\.\d+(?:\.\d+)?(?:[a-zA-Z0-9_\-\.]+)?|\d{4}\.\d+\.\d+)", out)
            ver = m.group(1) if m else (out.splitlines()[0] if out else "未知")
            return (ver, is_up_to_date and not has_update_flag)
        except Exception:
            return ("未知", False)


_LATEST_CACHE: dict[str, tuple[str, float]] = {}


def _get_latest_version(kind: str) -> str:
    """获取云端官方最新发布的版本号（带内存缓存避免重复请求）。"""
    import time
    import urllib.request
    now = time.monotonic()
    if kind in _LATEST_CACHE and now - _LATEST_CACHE[kind][1] < 300:
        return _LATEST_CACHE[kind][0]

    ver = "latest"
    try:
        if kind == "codex":
            req = urllib.request.Request("https://registry.npmjs.org/@openai/codex/latest", headers={"User-Agent": "Astrorder"})
            with urllib.request.urlopen(req, timeout=3) as resp:
                data = json.loads(resp.read().decode())
                ver = data.get("version") or "latest"
        elif kind == "hermes":
            req = urllib.request.Request("https://api.github.com/repos/NousResearch/Hermes-Agent/releases/latest", headers={"User-Agent": "Astrorder"})
            with urllib.request.urlopen(req, timeout=3) as resp:
                data = json.loads(resp.read().decode())
                tag = data.get("tag_name") or ""
                ver = tag.lstrip("v") if tag else "latest"
        elif kind == "grok":
            ver = "1.0.46"
    except Exception:
        ver = "latest"

    _LATEST_CACHE[kind] = (ver, now)
    return ver


@router.get("/api/v1/agents/upgrade/targets")
def get_upgrade_targets(request: Request) -> dict[str, Any]:
    """列出所有可升级的 Agent 目标，包含当前版本和云端最新版本号。"""
    _private(request)
    store = request.app.state.store
    agents = store.list_agents()
    targets = []
    supported_kinds = {"codex", "hermes", "grok"}
    for a in agents:
        kind = str(a.get("kind") or "").lower()
        if kind not in supported_kinds:
            continue
        conn_id = a.get("connection_id")
        current_ver, is_up_to_date = _get_agent_version(kind, conn_id, store)
        latest_ver = _get_latest_version(kind)
        # 如果 Agent 运行时自检已经报告 Up to date，或者当前版本匹配最新版本，则视为无更新
        if is_up_to_date:
            has_update = False
            # 若当前与云端标签一致或当前版本已知，将 latest_version 对齐展示为最新一致
            if current_ver != "未知":
                latest_ver = current_ver
        elif "hermes" in kind and not is_up_to_date and current_ver != "未知":
            # Hermes 处于可升级状态（例如还有 commits 未 pull）
            has_update = True
            latest_ver = "最新主线 (HEAD)"
        else:
            has_update = bool(current_ver != "未知" and latest_ver != "latest" and current_ver != latest_ver)
        targets.append({
            "id": a["id"],
            "name": a["name"],
            "kind": kind,
            "status": a.get("status"),
            "connection_id": conn_id or "local",
            "current_version": current_ver,
            "latest_version": latest_ver,
            "has_update": has_update,
        })
    return {"items": targets}
