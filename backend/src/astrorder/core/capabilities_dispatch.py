from __future__ import annotations

import logging
import os
import shutil
import tarfile
import tempfile
import time
from typing import Any

from .ov_sync import resolve_target, _run_cmd_in_target
from ..connections import run_subprocess_hidden

logger = logging.getLogger("astrorder.capabilities_dispatch")


def dispatch_capability(
    store: Any,
    capability_type: str,  # "skill", "mcp", "plugin"
    source_target_id: str,
    source_agent: str,     # "codex", "hermes", "grok"
    item_id: str,          # skill name or mcp name or plugin name
    target_matrix: list[dict[str, Any]],  # [{"target_id": "...", "agent": "..."}]
    item_data: dict[str, Any] | None = None,
) -> dict[str, Any]:
    """
    真实跨端/跨 Agent 动态分发引擎：
    1. 找到源资产（文件目录或配置定义）
    2. 打包为 tar 归档或标准化配置包
    3. 遍历目标端矩阵（本机 / WSL / Debian），按目标 Agent 的规范注入资产
    4. 远程 SSH 注入并执行原子核验
    5. 返回包含每个目标注入状态的权威审计报告
    """
    audit_reports = []
    logger.info(f"Starting capability dispatch: {capability_type} '{item_id}' to {len(target_matrix)} targets")

    if capability_type == "skill":
        audit_reports = _dispatch_skill(store, source_target_id, source_agent, item_id, target_matrix)
    elif capability_type == "mcp":
        audit_reports = _dispatch_mcp(store, source_target_id, source_agent, item_id, target_matrix, item_data)
    elif capability_type == "plugin":
        audit_reports = _dispatch_plugin(store, source_target_id, source_agent, item_id, target_matrix, item_data)
    else:
        raise ValueError(f"Unsupported capability type: {capability_type}")

    success_count = sum(1 for r in audit_reports if r.get("ok"))
    return {
        "ok": success_count > 0,
        "capability_type": capability_type,
        "item_id": item_id,
        "total_targets": len(target_matrix),
        "success_count": success_count,
        "reports": audit_reports,
    }


# ==========================================
# 1. 技能跨端分发 (Skills Real Dispatch)
# ==========================================

def _find_local_skill_path(skill_name: str, agent: str) -> str | None:
    candidate_dirs = [
        os.path.expanduser("~/.hermes/skills"),
        os.path.expanduser("~/AppData/Local/hermes/skills"),
        os.path.expanduser("~/.codex/skills"),
    ]
    for base in candidate_dirs:
        if not os.path.isdir(base):
            continue
        for root, dirs, files in os.walk(base):
            if "SKILL.md" in files:
                folder_name = os.path.basename(root)
                if folder_name.lower() == skill_name.lower():
                    return root
    return None


def _dispatch_skill(
    store: Any,
    source_target_id: str,
    source_agent: str,
    skill_name: str,
    target_matrix: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    reports = []
    
    # 获取本地源目录
    source_path = _find_local_skill_path(skill_name, source_agent)
    if not source_path or not os.path.isdir(source_path):
        # 尝试在全局已解析技能查找
        return [{
            "target_id": "all",
            "agent": "all",
            "ok": False,
            "message": f"Source skill directory for '{skill_name}' not found locally.",
        }]

    # 创建临时的 tar 打包文件
    with tempfile.NamedTemporaryFile(suffix=".tar.gz", delete=False) as tmp_tar:
        tar_path = tmp_tar.name

    try:
        with tarfile.open(tar_path, "w:gz") as tar:
            tar.add(source_path, arcname=skill_name)
        
        tar_size = os.path.getsize(tar_path)
        logger.info(f"Packaged skill '{skill_name}' into tar ({tar_size} bytes)")

        # 遍历目标矩阵
        for t_spec in target_matrix:
            dest_target_id = t_spec.get("target_id")
            dest_agent = t_spec.get("agent")
            dest_target = resolve_target(store, dest_target_id)
            dest_kind = dest_target.get("kind")
            dest_name = dest_target.get("name", dest_target_id)

            try:
                if dest_kind == "local":
                    # 本机目标注入
                    dest_base = os.path.expanduser("~/.hermes/skills" if dest_agent == "hermes" else "~/.codex/skills")
                    os.makedirs(dest_base, exist_ok=True)
                    dest_dir = os.path.join(dest_base, skill_name)
                    if os.path.exists(dest_dir):
                        shutil.rmtree(dest_dir)
                    shutil.copytree(source_path, dest_dir)
                    # 核验注入
                    verified = os.path.isfile(os.path.join(dest_dir, "SKILL.md"))
                    reports.append({
                        "target_id": dest_target_id,
                        "target_name": dest_name,
                        "agent": dest_agent,
                        "ok": verified,
                        "installed_path": dest_dir,
                        "message": "Local skill copied and verified OK" if verified else "Verification failed",
                    })
                else:
                    # 远程目标注入 (WSL / Debian SSH)
                    dest_base_remote = f"~/.{dest_agent}/skills"
                    # 1. 确保远程目录存在
                    _run_cmd_in_target(dest_target, f"mkdir -p {dest_base_remote}", timeout=10)
                    
                    # 2. 传输 tar 包并远程解压
                    # 使用 scp 或 base64 安全管道传输
                    import base64
                    with open(tar_path, "rb") as f:
                        b64_content = base64.b64encode(f.read()).decode("ascii")
                    
                    remote_unpack = (
                        f"python3 -c \\\""
                        f"import base64, tarfile, io, os; "
                        f"data = base64.b64decode('{b64_content}'); "
                        f"dest = os.path.expanduser('{dest_base_remote}'); "
                        f"tar = tarfile.open(fileobj=io.BytesIO(data), mode='r:gz'); "
                        f"tar.extractall(dest); "
                        f"v_path = os.path.join(dest, '{skill_name}', 'SKILL.md'); "
                        f"print('###SKILL_VERIFIED###' if os.path.isfile(v_path) else '###FAILED###')\\\""
                    )
                    code, out = _run_cmd_in_target(dest_target, remote_unpack, timeout=6)
                    verified = (code == 0 and "###SKILL_VERIFIED###" in out)
                    reports.append({
                        "target_id": dest_target_id,
                        "target_name": dest_name,
                        "agent": dest_agent,
                        "ok": verified,
                        "installed_path": f"{dest_base_remote}/{skill_name}",
                        "message": "Remote SSH unpack and verified OK" if verified else f"Remote failed: {out}",
                    })
            except Exception as exc:
                logger.error(f"Failed to dispatch skill to {dest_target_id}: {exc}")
                reports.append({
                    "target_id": dest_target_id,
                    "target_name": dest_name,
                    "agent": dest_agent,
                    "ok": False,
                    "message": str(exc),
                })
    finally:
        if os.path.exists(tar_path):
            try:
                os.remove(tar_path)
            except Exception:
                pass

    return reports


# ==========================================
# 2. MCP 跨端分发 (MCP Real Dispatch)
# ==========================================

def _dispatch_mcp(
    store: Any,
    source_target_id: str,
    source_agent: str,
    server_name: str,
    target_matrix: list[dict[str, Any]],
    item_data: dict[str, Any] | None,
) -> list[dict[str, Any]]:
    from .capabilities_sync import update_mcp_server

    reports = []
    data_to_dispatch = item_data or {"name": server_name}

    for t_spec in target_matrix:
        dest_target_id = t_spec.get("target_id")
        dest_agent = t_spec.get("agent")
        dest_target = resolve_target(store, dest_target_id)
        dest_name = dest_target.get("name", dest_target_id)

        try:
            res = update_mcp_server(store, dest_target_id, dest_agent, "save", data_to_dispatch)
            reports.append({
                "target_id": dest_target_id,
                "target_name": dest_name,
                "agent": dest_agent,
                "ok": res.get("ok", False),
                "message": f"MCP configuration injected into {dest_agent} config",
            })
        except Exception as exc:
            reports.append({
                "target_id": dest_target_id,
                "target_name": dest_name,
                "agent": dest_agent,
                "ok": False,
                "message": str(exc),
            })

    return reports


# ==========================================
# 3. 插件跨端分发 (Plugin Real Dispatch)
# ==========================================

def _dispatch_plugin(
    store: Any,
    source_target_id: str,
    source_agent: str,
    plugin_name: str,
    target_matrix: list[dict[str, Any]],
    item_data: dict[str, Any] | None,
) -> list[dict[str, Any]]:
    from .capabilities_sync import update_plugin_state

    reports = []
    for t_spec in target_matrix:
        dest_target_id = t_spec.get("target_id")
        dest_agent = t_spec.get("agent")
        dest_target = resolve_target(store, dest_target_id)
        dest_name = dest_target.get("name", dest_target_id)

        try:
            res = update_plugin_state(store, dest_target_id, dest_agent, "toggle", plugin_name, enabled=True)
            reports.append({
                "target_id": dest_target_id,
                "target_name": dest_name,
                "agent": dest_agent,
                "ok": res.get("ok", False),
                "message": f"Plugin activated and recorded into {dest_agent} config",
            })
        except Exception as exc:
            reports.append({
                "target_id": dest_target_id,
                "target_name": dest_name,
                "agent": dest_agent,
                "ok": False,
                "message": str(exc),
            })

    return reports
