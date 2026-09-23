import asyncio
import glob
import json
import os
from datetime import datetime
from typing import Any
from uuid import uuid4

import httpx
from fastapi import APIRouter, HTTPException, Query, Request
from sqlalchemy import select

from .auth import require_browser
from .daemon.bridge import DaemonBridgeError
from .gateway_config import get_gateway_config, public_gateway_config, save_gateway_config
from .model_sync_service import (
    build_target_plan,
    fetch_catalog,
    list_jobs,
    list_targets,
    run_sync,
    save_job,
    validate_hermes_catalog,
    validate_selections,
)
from .models import TokenMetricRow

router = APIRouter()

VALID_RANGES = {"all", "30d", "7d"}
VALID_SURFACES = {"all", "codex", "claude", "grok"}

_quota_snapshot_cache: dict[str, Any] = {"expires_at": 0.0, "payload": None}
_session_gateway_cache: dict[str, tuple[float, int, float | None]] = {}


def _private(request: Request) -> None:
    require_browser(request, request.app.state.settings)


def _gateway_endpoint(config: dict[str, Any], resource: str) -> str:
    return f"{config['management_url']}/api/{resource}"


def _gateway_headers(config: dict[str, Any]) -> dict[str, str]:
    return {"Authorization": f"Bearer {config['api_key']}"} if config["api_key"] else {}

async def _fetch_session_gateway_stats(config: dict[str, Any], session_id: str) -> tuple[int | None, float | None]:
    import time
    now = time.monotonic()
    cached = _session_gateway_cache.get(session_id)
    if cached and now < cached[0]:
        return cached[1], cached[2]
    if not config.get("management_url"):
        return None, None
    try:
        async with httpx.AsyncClient(follow_redirects=True, timeout=5.0) as client:
            url = _gateway_endpoint(config, "logs")
            response = await client.get(url, params={"conversationId": session_id, "limit": 2000}, headers=_gateway_headers(config))
            if response.status_code != 200:
                return None, None
            payload = response.json()
            logs = payload.get("logs", [])
            if not logs:
                return None, None
            total_tokens = sum(int(item.get("totalTokens") or 0) for item in logs)
            speed = None
            latest = logs[0]
            metric_val = latest.get("displayMetrics", {}).get("tokPerSecond", {}).get("value")
            if isinstance(metric_val, (int, float)) and metric_val > 0:
                speed = round(float(metric_val), 1)
            _session_gateway_cache[session_id] = (now + 15.0, total_tokens, speed)
            return total_tokens, speed
    except Exception:
        return None, None


def _model_quota(
    model_route: str,
    models: list[dict[str, Any]],
    combos: list[dict[str, Any]],
    reports: list[dict[str, Any]],
    accounts: list[dict[str, Any]] | None = None,
) -> dict[str, Any]:
    route = model_route.strip()
    candidate = route.split("/", 1)[1] if "/" in route else route
    exact = [row for row in models if not row.get("disabled") and row.get("namespaced") in {route, candidate}]
    matches = exact or [row for row in models if not row.get("disabled") and row.get("id") == candidate]
    providers = {str(row.get("provider")) for row in matches if row.get("provider")}
    combo = next((row for row in combos if row.get("model") in {route, candidate}), None)
    if combo:
        providers = {str(row.get("provider")) for row in combo.get("targets", []) if row.get("provider")}
    selected_reports = [row for row in reports if row.get("provider") in providers]
    if accounts is not None and "openai" in providers:
        selected_reports = [row for row in selected_reports if row.get("provider") != "openai"]
    return {
        "model": candidate,
        "providers": sorted(providers),
        "reports": selected_reports,
        "accounts": accounts if "openai" in providers else [],
    }


@router.get("/api/v1/sessions/{session_id}/usage")
async def get_session_usage(session_id: str, request: Request, agent_id: str | None = None) -> dict[str, Any]:
    _private(request)
    store = request.app.state.store
    config = get_gateway_config(store)
    gw_total_tokens, gw_speed = await _fetch_session_gateway_stats(config, session_id)

    with store.session() as db:
        q = select(TokenMetricRow).where(TokenMetricRow.session_id == session_id)
        if agent_id:
            q = q.where(TokenMetricRow.agent_id == agent_id)
        rows = db.scalars(q.order_by(TokenMetricRow.updated_at.desc())).all()

    pattern = os.path.expanduser(f"~/.codex/sessions/**/rollout-*{session_id}*.jsonl")
    matches = glob.glob(pattern, recursive=True)
    if matches:
        last_u = None
        cw = 1000000
        last_in = 0
        m_name = "default"
        try:
            with open(matches[0], "r", encoding="utf-8", errors="ignore") as f:
                for line in f:
                    if "total_token_usage" in line:
                        d = json.loads(line)
                        info = d.get("payload", {}).get("info", {})
                        if "total_token_usage" in info:
                            last_u = info["total_token_usage"]
                            cw = info.get("model_context_window", cw)
                            last_in = info.get("last_token_usage", {}).get("input_tokens", last_in)
                    if "turn_context" in line and "model" in line:
                        m_name = json.loads(line).get("payload", {}).get("model", m_name)
        except (OSError, json.JSONDecodeError, TypeError, ValueError):
            last_u = None
        if last_u:
            in_tok = last_u.get("input_tokens", 0)
            cached = last_u.get("cached_input_tokens", 0)
            return {
                "ok": True,
                "session_id": session_id,
                "model": m_name,
                "context_window": cw,
                "last_input_tokens": last_in or in_tok,
                "used_percentage": round(((last_in or in_tok) / max(cw, 1)) * 100, 2),
                "total_tokens": gw_total_tokens if gw_total_tokens is not None else last_u.get("total_tokens", 0),
                "input_tokens": in_tok,
                "output_tokens": last_u.get("output_tokens", 0),
                "cached_tokens": cached,
                "reasoning_tokens": last_u.get("reasoning_output_tokens", 0),
                "cache_hit_rate": round((cached / max(in_tok, 1)) * 100, 1) if in_tok > 0 else 0,
                "speed": gw_speed,
            }

    if not rows:
        return {
            "ok": True,
            "session_id": session_id,
            "model": "default",
            "context_window": 1000000,
            "last_input_tokens": 0,
            "used_percentage": 0,
            "total_tokens": gw_total_tokens or 0,
            "input_tokens": 0,
            "output_tokens": 0,
            "cached_tokens": 0,
            "reasoning_tokens": 0,
            "cache_hit_rate": 0,
            "speed": gw_speed,
        }

    latest = rows[0]
    in_tok = latest.input_tokens
    cached = latest.cached_tokens
    cw = latest.context_window or 1000000
    last_in = latest.last_input_tokens or in_tok
    return {
        "ok": True,
        "session_id": session_id,
        "model": latest.model,
        "context_window": cw,
        "last_input_tokens": last_in,
        "used_percentage": round((last_in / max(cw, 1)) * 100, 2),
        "total_tokens": gw_total_tokens if gw_total_tokens is not None else latest.total_tokens,
        "input_tokens": in_tok,
        "output_tokens": latest.output_tokens,
        "cached_tokens": cached,
        "reasoning_tokens": latest.reasoning_tokens,
        "cache_hit_rate": round((cached / max(in_tok, 1)) * 100, 1) if in_tok > 0 else 0,
        "speed": gw_speed,
    }


@router.get("/api/v1/analytics/config")
def get_analytics_config(request: Request) -> dict[str, Any]:
    _private(request)
    return public_gateway_config(get_gateway_config(request.app.state.store))


@router.put("/api/v1/analytics/config")
def update_analytics_config(request: Request, payload: dict[str, Any]) -> dict[str, Any]:
    _private(request)
    current = get_gateway_config(request.app.state.store)
    try:
        updates = dict(payload)
        if "management_url" not in updates and updates.get("base_url"):
            updates["management_url"] = updates.pop("base_url")
        config = save_gateway_config(request.app.state.store, {
            **current, **updates,
            "api_key": current["api_key"] if "api_key" not in payload else payload.get("api_key"),
        })
    except (TypeError, ValueError) as exc:
        raise HTTPException(status_code=422, detail=str(exc)) from exc
    return public_gateway_config(config)


@router.get("/api/v1/analytics/usage")
async def get_analytics_usage(
    request: Request,
    range: str = Query(default="30d"),
    surface: str = Query(default="all"),
    since: int | None = Query(default=None, ge=0),
    until: int | None = Query(default=None, ge=0),
) -> Any:
    _private(request)
    if range not in VALID_RANGES:
        raise HTTPException(status_code=422, detail="统计范围无效")
    if surface not in VALID_SURFACES:
        raise HTTPException(status_code=422, detail="Agent 类型无效")
    if (since is None) != (until is None) or (since is not None and since >= until):
        raise HTTPException(status_code=422, detail="自定义时间范围无效")

    params: dict[str, str | int] = {"range": range, "surface": surface}
    if since is not None and until is not None:
        params.update(since=since, until=until)
    config = get_gateway_config(request.app.state.store)
    try:
        async with httpx.AsyncClient(follow_redirects=True, timeout=15.0) as client:
            response = await client.get(_gateway_endpoint(config, "usage"), params=params, headers=_gateway_headers(config))
            response.raise_for_status()
            return response.json()
    except (httpx.HTTPError, ValueError) as exc:
        raise HTTPException(status_code=502, detail=f"LLM 网关用量请求失败：{exc}") from exc


@router.get("/api/v1/analytics/model-quota")
async def get_model_quota(request: Request, model: str = Query(min_length=1)) -> dict[str, Any]:
    _private(request)
    store = request.app.state.store
    config = get_gateway_config(store)
    headers = _gateway_headers(config)
    loop = asyncio.get_event_loop()
    now = loop.time()
    try:
        cached = _quota_snapshot_cache.get("payload")
        if cached and now < _quota_snapshot_cache.get("expires_at", 0):
            models, combos, reports, accounts = cached
        else:
            async with httpx.AsyncClient(follow_redirects=True, timeout=15.0) as client:
                responses = await asyncio.gather(*(
                    client.get(_gateway_endpoint(config, resource), headers=headers)
                    for resource in ("models", "combos", "provider-quotas")
                ))
                for response in responses:
                    response.raise_for_status()
                models, combos_payload, quotas_payload = (response.json() for response in responses)
                accounts_response = await client.get(_gateway_endpoint(config, "codex-auth/accounts"), headers=headers)
                accounts = [
                    account for account in accounts_response.json().get("accounts", [])
                    if not account.get("paused")
                ] if accounts_response.status_code == 200 else []
                combos = combos_payload.get("combos", [])
                reports = quotas_payload.get("reports", [])
                _quota_snapshot_cache["expires_at"] = now + 60.0
                _quota_snapshot_cache["payload"] = (models, combos, reports, accounts)
        resolved = _model_quota(model, models, combos, reports, accounts)
        return resolved
    except (httpx.HTTPError, ValueError, TypeError, AttributeError) as exc:
        raise HTTPException(status_code=502, detail=f"LLM 网关模型额度请求失败：{exc}") from exc


@router.get("/api/v1/model-sync/targets")
def get_model_sync_targets(request: Request) -> dict[str, Any]:
    _private(request)
    return {"items": list_targets(request.app.state.store)}


@router.post("/api/v1/model-sync/preview")
async def preview_model_sync(request: Request, payload: dict[str, Any]) -> dict[str, Any]:
    _private(request)
    try:
        selections = validate_selections(request.app.state.store, payload.get("targets"))
    except (TypeError, ValueError) as exc:
        raise HTTPException(status_code=422, detail=str(exc)) from exc
    bridge = getattr(request.app.state, "daemon_bridge", None)
    if bridge is None:
        raise HTTPException(status_code=503, detail="小内核未启用")
    try:
        config = get_gateway_config(request.app.state.store)
        catalog = await fetch_catalog(config)
        plans = await asyncio.gather(*(
            build_target_plan(
                request.app.state.store, bridge, catalog,
                str(item.get("target_id") or ""), list(item.get("agents") or []), config,
            )
            for item in selections
        ))
        for plan in plans:
            if "hermes" in plan["agents"]:
                plan["hermes"] = await validate_hermes_catalog(request.app, plan["target_id"], catalog)
        return {
            "catalog_fingerprint": catalog["fingerprint"], "model_count": len(catalog["models"]),
            "targets": [{key: value for key, value in plan.items() if key not in {"files", "target"}} for plan in plans],
        }
    except (ValueError, httpx.HTTPError, KeyError, TypeError, DaemonBridgeError) as exc:
        raise HTTPException(status_code=502, detail=f"模型同步预览失败：{exc}") from exc


@router.post("/api/v1/model-sync/jobs")
async def start_model_sync(request: Request, payload: dict[str, Any]) -> dict[str, Any]:
    _private(request)
    try:
        selections = validate_selections(request.app.state.store, payload.get("targets"))
    except ValueError as exc:
        raise HTTPException(status_code=422, detail=str(exc)) from exc
    if getattr(request.app.state, "daemon_bridge", None) is None:
        raise HTTPException(status_code=503, detail="小内核未启用")
    operation_id = f"model-sync-{uuid4().hex}"
    cancel_event = request.app.state.background_tasks.start(operation_id)
    task = asyncio.create_task(run_sync(request.app, operation_id, selections, cancel_event))
    tasks = getattr(request.app.state, "model_sync_tasks", None)
    if tasks is None:
        tasks = request.app.state.model_sync_tasks = set()
    tasks.add(task)
    task.add_done_callback(tasks.discard)
    return {"id": operation_id, "status": "running"}


@router.get("/api/v1/model-sync/jobs")
def get_model_sync_jobs(request: Request) -> dict[str, Any]:
    _private(request)
    jobs = list_jobs(request.app.state.store)
    for job in jobs:
        if job.get("status") == "running" and not request.app.state.background_tasks.is_active(job["id"]):
            job.update(status="failed", updated_at=datetime.now().astimezone().isoformat(), error="大内核重启，同步任务已中断")
            save_job(request.app.state.store, job)
    return {"items": jobs}


@router.post("/api/v1/model-sync/jobs/{operation_id}/cancel")
def cancel_model_sync(operation_id: str, request: Request) -> dict[str, bool]:
    _private(request)
    return {"cancelled": request.app.state.background_tasks.cancel(operation_id)}
