from __future__ import annotations

import logging
from typing import Any
from fastapi import APIRouter, HTTPException, Query, Request
from pydantic import BaseModel

from ..core.auth import require_browser
from ..core.ov_sync import list_targets
from ..core.skills_sync import (
    install_target_skill,
    list_target_skills,
    remove_target_skill,
)

logger = logging.getLogger("astrorder.skills")
router = APIRouter()


class SkillInstallPayload(BaseModel):
    target_ids: list[str]
    package_source: str
    skill_name: str | None = None


class SkillRemovePayload(BaseModel):
    target_id: str
    skill_name: str


@router.get("/api/v1/skills/search")
async def search_community_skills(
    request: Request,
    q: str = Query(default="agent", min_length=1, max_length=100),
) -> dict[str, Any]:
    """通过 skills find <q> 检索 skills.sh 全球社区开源技能市场。"""
    settings = request.app.state.settings
    require_browser(request, settings)

    import re
    import shutil
    import subprocess

    ansi_escape = re.compile(r"\x1B(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~])")
    npm_cmd = shutil.which("npm.cmd") or shutil.which("npm") or "npm"
    cmd = [npm_cmd, "exec", "--", "skills", "find", q.strip()]

    from ..connections import run_subprocess_hidden

    try:
        proc = run_subprocess_hidden(cmd, capture_output=True, text=True, timeout=12)
        clean = ansi_escape.sub("", proc.stdout)
        items = []
        current = {}
        for line in clean.splitlines():
            line = line.strip()
            if not line or line.startswith("Install with"):
                continue
            if "installs" in line:
                parts = line.split()
                pkg = parts[0]
                installs = parts[1] if len(parts) > 1 else ""
                current = {"pkg": pkg, "installs": installs}
            elif line.startswith("└") and current:
                current["url"] = line.replace("└", "").strip()
                # 提取 owner/repo 和 skill 名
                pkg_full = current.get("pkg", "")
                if "@" in pkg_full:
                    repo_part, skill_name = pkg_full.split("@", 1)
                else:
                    repo_part, skill_name = pkg_full, pkg_full
                current["name"] = skill_name
                current["repo"] = repo_part
                items.append(current)
                current = {}
        return {"query": q, "items": items}
    except Exception as e:
        logger.warning("skills find failed: %s", e)
        return {"query": q, "items": [], "error": str(e)}


@router.get("/api/v1/skills/targets")
def get_skills_targets(request: Request) -> dict[str, Any]:
    """获取所有支持技能分发的目标环境列表及其实时安装技能状态"""
    require_browser(request, request.app.state.settings)
    store = request.app.state.store
    targets = list_targets(store)
    
    results = []
    for t in targets:
        skills = list_target_skills(store, t["id"])
        results.append({
            **t,
            "skills": skills,
            "skills_count": len(skills),
        })
    return {"targets": results}


@router.get("/api/v1/skills/target/{target_id}")
def get_single_target_skills(target_id: str, request: Request) -> dict[str, Any]:
    """获取单个目标环境的技能清单"""
    require_browser(request, request.app.state.settings)
    store = request.app.state.store
    skills = list_target_skills(store, target_id)
    return {"target_id": target_id, "skills": skills, "count": len(skills)}


@router.post("/api/v1/skills/install")
def install_skill(payload: SkillInstallPayload, request: Request) -> dict[str, Any]:
    """向一个或多个目标批量安装开源 Skill"""
    require_browser(request, request.app.state.settings)
    store = request.app.state.store
    
    results = []
    for tid in payload.target_ids:
        try:
            res = install_target_skill(
                store,
                tid,
                package_source=payload.package_source,
                skill_name=payload.skill_name,
            )
            results.append(res)
        except Exception as exc:
            results.append({
                "target_id": tid,
                "success": False,
                "package": payload.package_source,
                "error": str(exc),
            })
            
    return {"results": results}


@router.post("/api/v1/skills/remove")
def remove_skill(payload: SkillRemovePayload, request: Request) -> dict[str, Any]:
    """从目标环境卸载指定 Skill"""
    require_browser(request, request.app.state.settings)
    store = request.app.state.store
    try:
        res = remove_target_skill(store, payload.target_id, payload.skill_name)
        return res
    except Exception as exc:
        raise HTTPException(status_code=500, detail=f"卸载技能失败: {exc}") from exc
