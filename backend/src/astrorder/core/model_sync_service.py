from __future__ import annotations

import asyncio
import difflib
import json
import os
import re
import subprocess
from datetime import UTC, datetime
from typing import Any

import httpx
from sqlalchemy.dialects.sqlite import insert

from .gateway_config import get_gateway_config
from .model_sync import (
    normalize_catalog,
    render_codex_catalog,
    render_codex_config,
    render_grok_config,
    sha256_text,
)
from astrorder.models import WorkspacePreferenceRow

STATUS_KEY = "model_sync:jobs"


def _now() -> str:
    return datetime.now(UTC).isoformat().replace("+00:00", "Z")


def list_targets(store: Any) -> list[dict[str, Any]]:
    targets = [{"id": "local", "kind": "local", "name": "本机", "agents": ["codex", "grok", "hermes"]}]
    ssh_rows = store.list_ssh_connections()
    has_wsl_ssh = any(
        str(row.get("settings", {}).get("host") or "").strip().lower() in {"127.0.0.1", "localhost"}
        or "wsl" in str(row.get("display_name") or row.get("profile_name") or "").lower()
        for row in ssh_rows
    )
    if os.name == "nt" and not has_wsl_ssh:
        try:
            result = subprocess.run(
                ["wsl.exe", "-l", "-q"], capture_output=True, timeout=5,
                creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0),
                check=False,
            )
            text = result.stdout.decode("utf-16le", errors="ignore")
            for distro in (line.replace("\x00", "").strip() for line in text.splitlines()):
                if distro:
                    targets.append({"id": f"wsl:{distro}", "kind": "wsl", "distro": distro, "name": f"WSL · {distro}", "agents": ["codex", "grok", "hermes"]})
        except (OSError, subprocess.SubprocessError):
            pass
    for row in ssh_rows:
        targets.append({
            "id": f"ssh:{row['id']}", "kind": "ssh", "connection_id": row["id"],
            "name": row.get("display_name") or row.get("profile_name") or row["id"],
            "state": row.get("state"), "agents": ["codex", "grok", "hermes"],
        })
    return targets


def resolve_target(store: Any, target_id: str) -> dict[str, Any]:
    target = next((item for item in list_targets(store) if item["id"] == target_id), None)
    if target is None:
        raise ValueError("模型同步目标不存在")
    if target["kind"] == "ssh":
        row = store.get_ssh_connection(target["connection_id"])
        if row is None:
            raise ValueError("SSH 连接不存在")
        return {"kind": "ssh", "settings": row["settings"]}
    return {key: target[key] for key in ("kind", "distro") if key in target}


def validate_selections(store: Any, value: Any) -> list[dict[str, Any]]:
    if not isinstance(value, list) or not value or len(value) > 64:
        raise ValueError("请选择同步目标")
    result = []
    seen = set()
    for item in value:
        if not isinstance(item, dict):
            raise TypeError("同步目标格式无效")
        target_id = item.get("target_id")
        agents = item.get("agents")
        if not isinstance(target_id, str) or target_id in seen:
            raise ValueError("同步目标重复或无效")
        if not isinstance(agents, list) or not agents or any(not isinstance(agent, str) for agent in agents) or not set(agents) <= {"codex", "grok", "hermes"}:
            raise ValueError("Agent 选择无效")
        resolve_target(store, target_id)
        seen.add(target_id)
        result.append({"target_id": target_id, "agents": list(dict.fromkeys(agents))})
    return result


async def fetch_catalog(config: dict[str, Any]) -> dict[str, Any]:
    headers = {"Authorization": f"Bearer {config['api_key']}"} if config["api_key"] else {}
    async with httpx.AsyncClient(follow_redirects=True, timeout=20) as client:
        response = await client.get(f"{config['management_url']}/api/models", headers=headers)
        response.raise_for_status()
    return normalize_catalog(response.json())


def _diff(before: str, after: str, name: str) -> str:
    if before == after:
        return ""
    lines = difflib.unified_diff(before.splitlines(), after.splitlines(), f"当前/{name}", f"同步后/{name}", lineterm="", n=2)
    value = "\n".join(list(lines)[:400])
    return re.sub(r'(?mi)^(\s*[+-]?\s*api_key\s*=\s*).+$', r'\1"[已配置]"', value)


def _active_for_target(store: Any, target_id: str, agents: list[str]) -> bool:
    if target_id != "local":
        return False
    active = {"running", "waiting_approval"}
    relevant = {agent["id"] for agent in store.list_agents() if agent.get("kind") in agents}
    for session in store.list_sessions():
        if session.get("status") not in active:
            continue
        connection_id = session.get("connection_id")
        if not connection_id and session.get("agent_id") in relevant:
            return True
    return False


async def validate_hermes_catalog(app: Any, target_id: str, catalog: dict[str, Any]) -> dict[str, Any]:
    if target_id.startswith("wsl:"):
        return {"dynamic": True, "status": "未连接"}
    agents = [agent for agent in app.state.store.list_agents() if agent.get("kind") == "hermes"]
    if target_id.startswith("ssh:"):
        agents = [agent for agent in agents if agent.get("connection_id") == target_id[4:]]
    else:
        agents = [agent for agent in agents if not agent.get("connection_id")]
    sessions = app.state.store.list_sessions()
    expected = {model["slug"] for model in catalog["models"]}
    for agent in agents:
        session = next((item for item in sessions if item["agent_id"] == agent["id"]), None)
        if session is None:
            continue
        resolver = getattr(app.state.connections, "get_runtime_by_agent_id", None)
        runtime = resolver(agent["id"]) if callable(resolver) else None
        models = getattr(runtime, "models", None)
        if not callable(models):
            continue
        try:
            rows = await asyncio.to_thread(models, session["id"])
            available = {
                str(row.get("model") or "") for row in rows if isinstance(row, dict) and row.get("model")
            }
            available |= {
                f"{row.get('provider')}/{row.get('model')}" for row in rows
                if isinstance(row, dict) and row.get("provider") and row.get("model")
            }
            return {
                "dynamic": True, "status": "已验证", "model_count": len(rows),
                "matched_count": len(expected & available),
            }
        except (OSError, RuntimeError, TypeError, ValueError) as exc:
            return {"dynamic": True, "status": "验证失败", "error": str(exc)[:300]}
    return {"dynamic": True, "status": "未连接"}


async def build_target_plan(store: Any, bridge: Any, catalog: dict[str, Any], target_id: str, agents: list[str], config: dict[str, Any]) -> dict[str, Any]:
    if not set(agents) <= {"codex", "grok", "hermes"} or not agents:
        raise ValueError("Agent 选择无效")
    target = resolve_target(store, target_id)
    response = await bridge.request_control("model_config.plan", {"target": target})
    current = response["result"]
    home = str(current.get("_home") or "~").replace("\\", "/")
    inference_url = config["target_overrides"].get(target_id, config["inference_url"])
    files: dict[str, str] = {}
    old_catalog: list[Any] = []
    if "codex" in agents:
        try:
            parsed = json.loads(current["codex_catalog"]["content"] or "[]")
            if isinstance(parsed, dict):
                old_catalog = parsed.get("models", [])
            elif isinstance(parsed, list):
                old_catalog = parsed
        except json.JSONDecodeError:
            old_catalog = []
        files["codex_catalog"] = render_codex_catalog(catalog, old_catalog)
        files["codex_config"] = render_codex_config(current["codex_config"]["content"], inference_url, f"{home}/.codex/opencodex-catalog.json")
    if "grok" in agents:
        files["grok_config"] = render_grok_config(current["grok_config"]["content"], catalog, inference_url, config["api_key"])

    new_slugs = {m["slug"] for m in catalog.get("models", [])}
    old_slugs = {str(m.get("slug")) for m in (old_catalog if isinstance(old_catalog, list) else []) if isinstance(m, dict) and m.get("slug")}
    model_diff = {
        "added": sorted(new_slugs - old_slugs),
        "removed": sorted(old_slugs - new_slugs),
        "kept_count": len(new_slugs & old_slugs),
    }
    changes = [
        {"file": name, "changed": sha256_text(text) != current[name]["sha256"], "current_sha256": current[name]["sha256"], "expected_sha256": sha256_text(text), "diff": _diff(current[name]["content"], text, name)}
        for name, text in files.items()
    ]
    return {
        "target_id": target_id, "agents": agents, "target": target, "files": files, "model_diff": model_diff,
        "changes": changes, "catalog_fingerprint": catalog["fingerprint"],
        "reload_pending": _active_for_target(store, target_id, agents),
        "hermes": {"dynamic": True, "status": "待验证" if "hermes" in agents else "未选择"},
    }


def _read_jobs(store: Any) -> list[dict[str, Any]]:
    with store.session() as db:
        row = db.get(WorkspacePreferenceRow, STATUS_KEY)
        return list(row.value) if row and isinstance(row.value, list) else []


def save_job(store: Any, job: dict[str, Any]) -> None:
    jobs = [job, *(item for item in _read_jobs(store) if item.get("id") != job["id"])][:20]
    with store.session() as db:
        statement = insert(WorkspacePreferenceRow).values(key=STATUS_KEY, value=jobs)
        db.execute(statement.on_conflict_do_update(index_elements=["key"], set_={"value": jobs}))


def list_jobs(store: Any) -> list[dict[str, Any]]:
    return _read_jobs(store)


async def run_sync(app: Any, operation_id: str, selections: list[dict[str, Any]], cancel_event: asyncio.Event) -> None:
    store, bridge = app.state.store, app.state.daemon_bridge
    job = {"id": operation_id, "status": "running", "created_at": _now(), "updated_at": _now(), "targets": []}
    save_job(store, job)
    try:
        config = get_gateway_config(store)
        catalog = await fetch_catalog(config)
        plans = await asyncio.gather(*(
            build_target_plan(store, bridge, catalog, str(item["target_id"]), list(item["agents"]), config)
            for item in selections
        ))
        if cancel_event.is_set():
            job.update(status="cancelled", updated_at=_now(), targets=[])
            return

        async def apply(plan: dict[str, Any]) -> dict[str, Any]:
            try:
                if cancel_event.is_set():
                    return {"target_id": plan["target_id"], "status": "cancelled"}
                changed: list[str] = []
                if plan["files"]:
                    response = await bridge.request_control("model_config.apply", {
                        "target": plan["target"], "files": plan["files"], "api_key": config["api_key"],
                    })
                    changed = response["result"].get("changed", [])
                reload_state = {"pending": [], "reloaded": []}
                reload_agents = [agent for agent in plan["agents"] if agent in {"codex", "grok"}]
                if plan["target_id"] == "local" and changed and reload_agents:
                    reload_response = await bridge.request_control("model_config.reload", {"agents": reload_agents})
                    reload_state = reload_response["result"]
                hermes = plan["hermes"]
                if "hermes" in plan["agents"]:
                    hermes = await validate_hermes_catalog(app, plan["target_id"], catalog)
                return {
                    "target_id": plan["target_id"], "status": "success",
                    "changed": changed,
                    "reload_pending": bool(reload_state.get("pending")),
                    "reloaded": reload_state.get("reloaded", []), "hermes": hermes,
                }
            except Exception as exc:  # noqa: BLE001 - each target reports its own deployment failure
                return {"target_id": plan["target_id"], "status": "failed", "error": str(exc)[:1000]}

        results = await asyncio.gather(*(apply(plan) for plan in plans))
        failures = [result for result in results if result["status"] == "failed"]
        cancelled = [result for result in results if result["status"] == "cancelled"]
        job.update(
            status="failed" if failures else "cancelled" if cancelled else "success", updated_at=_now(), targets=results,
            catalog_fingerprint=catalog["fingerprint"],
            **({"error": f"{len(failures)} 个目标同步失败"} if failures else {}),
        )
    except Exception as exc:  # noqa: BLE001 - background jobs persist all target failures
        job.update(status="failed", updated_at=_now(), error=str(exc)[:1000])
    finally:
        save_job(store, job)
        app.state.background_tasks.finish(operation_id, cancel_event)
