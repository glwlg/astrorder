from __future__ import annotations

import json
import logging
import os
import shutil
import subprocess
from typing import Any

from .ov_sync import list_targets, resolve_target, _run_cmd_in_target
from ..connections import run_subprocess_hidden

logger = logging.getLogger("astrorder.skills")


def _run_skills_cli_local(args: list[str], timeout: int = 30) -> tuple[int, str]:
    """在本机运行 skills CLI (通过 npm exec 或原生可执行)，使用 run_subprocess_hidden 防黑框/终端弹窗。"""
    is_win = os.name == "nt"
    npm_bin = shutil.which("npm.cmd") if is_win else shutil.which("npm")
    if not npm_bin:
        npm_bin = "npm"

    cmd = [npm_bin, "exec", "--", "skills", *args]
    try:
        proc = run_subprocess_hidden(
            cmd,
            capture_output=True,
            text=True,
            timeout=timeout,
        )
        return proc.returncode, proc.stdout or proc.stderr
    except Exception as exc:
        logger.error(f"Failed to run skills CLI locally: {exc}")
        return 1, str(exc)


def _run_skills_cli_target(target: dict[str, Any], args: list[str], timeout: int = 40) -> tuple[int, str]:
    """在目标环境（Local / SSH）中执行 skills CLI"""
    kind = target.get("kind")
    if kind == "local":
        return _run_skills_cli_local(args, timeout=timeout)

    # 远程 SSH 环境执行
    arg_str = " ".join(f"'{a}'" if " " in a else a for a in args)
    cmd = f"PATH=/home/luwei/.vite-plus/bin:/usr/local/bin:/usr/bin:/bin npx -y skills {arg_str}"
    return _run_cmd_in_target(target, cmd, timeout=timeout)


def _discover_native_agent_skills_local() -> list[dict[str, Any]]:
    """扫描本机 Agent 原生已安装的 SKILL.md（包括 Hermes、Codex 等），无须依赖 skills CLI 即可发现"""
    bases = [
        os.path.expanduser("~/.hermes/skills"),
        os.path.expanduser("~/AppData/Local/hermes/skills"),
        os.path.expanduser("~/.codex/skills"),
    ]
    seen_names: set[str] = set()
    results: list[dict[str, Any]] = []

    for base in bases:
        if not os.path.isdir(base):
            continue
        agent = "hermes" if "hermes" in base.lower() else "codex"
        for root, dirs, files in os.walk(base):
            # 过滤归档目录
            if ".archive" in root or ".curator_backups" in root:
                continue
            if "SKILL.md" in files:
                fpath = os.path.join(root, "SKILL.md")
                name = os.path.basename(root)
                desc = ""
                try:
                    with open(fpath, "r", encoding="utf-8", errors="ignore") as f:
                        for line in f:
                            if line.startswith("name:"):
                                name = line.split(":", 1)[1].strip().strip("\"'")
                            elif line.startswith("description:"):
                                desc = line.split(":", 1)[1].strip().strip("\"'")
                except Exception:
                    pass
                if name not in seen_names:
                    seen_names.add(name)
                    results.append({
                        "name": name,
                        "path": fpath,
                        "source": f"{agent}-native",
                        "agents": [agent],
                        "scope": "global",
                        "description": desc,
                    })
    return results


def _discover_native_agent_skills_target(target: dict[str, Any]) -> list[dict[str, Any]]:
    """在远程目标环境（WSL / SSH）中扫描原生 SKILL.md，单行推导规避 Shell 字符嵌套"""
    cmd = (
        "python3 -c 'import os, json; res = []; seen = set(); "
        "bases = [os.path.expanduser(\"~/.hermes/skills\"), os.path.expanduser(\"~/.codex/skills\")]; "
        "[res.append({\"name\": os.path.basename(r), \"path\": os.path.join(r, \"SKILL.md\"), "
        "\"source\": (\"hermes-native\" if \"hermes\" in b else \"codex-native\"), "
        "\"agents\": [\"hermes\" if \"hermes\" in b else \"codex\"], \"scope\": \"global\"}) "
        "for b in bases if os.path.isdir(b) for r, d, fs in os.walk(b) "
        "if \"SKILL.md\" in fs and \".archive\" not in r and \".curator\" not in r "
        "and not (os.path.basename(r) in seen or seen.add(os.path.basename(r)))]; "
        "print(\"###SKILLS_JSON###\" + json.dumps(res))'"
    )
    code, output = _run_cmd_in_target(target, cmd, timeout=20)
    if code == 0 and "###SKILLS_JSON###" in output:
        try:
            raw = output.split("###SKILLS_JSON###")[1].strip()
            data = json.loads(raw)
            return data if isinstance(data, list) else []
        except Exception as exc:
            logger.warning(f"Failed to parse target native skills JSON: {exc}")
    return []


def list_target_skills(store: Any, target_id: str) -> list[dict[str, Any]]:
    """查询指定目标上已安装的技能列表（深度融合 skills CLI 安装的技能与 Agent 原生技能，如柳如烟等）"""
    target = resolve_target(store, target_id)
    kind = target.get("kind")
    skills_map: dict[str, dict[str, Any]] = {}

    # 1. 查询 skills CLI (skills.sh / npx skills) 管理的技能
    code, output = _run_skills_cli_target(target, ["list", "-g", "--json"], timeout=20)
    if code == 0:
        try:
            clean_out = output.strip()
            if clean_out.find("[") != -1:
                clean_out = clean_out[clean_out.find("[") :]
            if clean_out.rfind("]") != -1:
                clean_out = clean_out[: clean_out.rfind("]") + 1]
            cli_data = json.loads(clean_out)
            if isinstance(cli_data, list):
                for item in cli_data:
                    name = item.get("name")
                    if name:
                        skills_map[name] = item
        except Exception:
            pass

    # 2. 深度扫描原生 Agent 目录（如 Hermes / Codex 的 skills 目录，支持识别用户专属私有技能）
    if kind == "local":
        native_skills = _discover_native_agent_skills_local()
    else:
        native_skills = _discover_native_agent_skills_target(target)

    for ns in native_skills:
        name = ns["name"]
        if name not in skills_map:
            skills_map[name] = ns
        else:
            # 合并 agent 列表与描述
            existing = skills_map[name]
            existing_agents = set(existing.get("agents") or [])
            existing_agents.update(ns.get("agents") or [])
            existing["agents"] = list(existing_agents)
            if not existing.get("description") and ns.get("description"):
                existing["description"] = ns["description"]

    return list(skills_map.values())


def install_target_skill(store: Any, target_id: str, package_source: str, skill_name: str | None = None) -> dict[str, Any]:
    """在指定目标上安装开源技能包（默认全局 -g，非交互式 -y，面向所有 Agent）"""
    target = resolve_target(store, target_id)
    args = ["add", package_source, "-g", "-y", "--agent", "*"]
    if skill_name:
        args.extend(["--skill", skill_name])

    code, output = _run_skills_cli_target(target, args, timeout=120)
    success = code == 0
    return {
        "target_id": target_id,
        "success": success,
        "package": package_source,
        "skill": skill_name,
        "output": output.strip(),
    }


def remove_target_skill(store: Any, target_id: str, skill_name: str) -> dict[str, Any]:
    """在指定目标上移除全局技能"""
    target = resolve_target(store, target_id)
    args = ["remove", skill_name, "-g", "-y"]
    code, output = _run_skills_cli_target(target, args, timeout=40)
    success = code == 0
    return {
        "target_id": target_id,
        "success": success,
        "skill": skill_name,
        "output": output.strip(),
    }
