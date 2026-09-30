import asyncio
import glob
import json
import os
from datetime import datetime, timedelta
from typing import Any
from uuid import uuid4

import httpx
from fastapi import APIRouter, HTTPException, Query, Request
from sqlalchemy import select

from .auth import require_browser
from astrorder.daemon.bridge import DaemonBridgeError
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
from astrorder.models import TokenMetricRow

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
    selected_accounts: list[dict[str, Any]] = []
    if accounts is not None:
        if "openai" in providers:
            selected_reports = [row for row in selected_reports if row.get("provider") != "openai"]
            selected_accounts = [acc for acc in accounts if acc.get("provider", "openai") in providers or not acc.get("provider")]
        elif any(p in {"codex", "antigravity"} for p in providers):
            selected_accounts = [acc for acc in accounts if acc.get("provider") in providers]
    return {
        "model": candidate,
        "providers": sorted(providers),
        "reports": selected_reports,
        "accounts": selected_accounts,
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

    from .session_usage import resolve_session_usage

    resolved = resolve_session_usage(session_id, store, agent_id)
    if resolved:
        total = gw_total_tokens if gw_total_tokens is not None else resolved.get("total_tokens", 0)
        cw = resolved.get("context_window")
        used_pct = round((resolved.get("last_input_tokens", 0) / cw) * 100, 2) if (cw and cw > 0) else None
        return {
            "ok": True,
            "session_id": session_id,
            "model": resolved.get("model", "default"),
            "context_window": cw,
            "last_input_tokens": resolved.get("last_input_tokens", 0),
            "used_percentage": used_pct,
            "total_tokens": total,
            "input_tokens": resolved.get("input_tokens", 0),
            "output_tokens": resolved.get("output_tokens", 0),
            "cached_tokens": resolved.get("cached_tokens", 0),
            "reasoning_tokens": resolved.get("reasoning_tokens", 0),
            "cache_hit_rate": resolved.get("cache_hit_rate", 0.0),
            "speed": gw_speed,
        }

    if not rows:
        return {
            "ok": True,
            "session_id": session_id,
            "model": "default",
            "context_window": None,
            "last_input_tokens": 0,
            "used_percentage": None,
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
    cw = latest.context_window
    last_in = latest.last_input_tokens or in_tok
    used_pct = round((last_in / cw) * 100, 2) if (cw and cw > 0) else None
    return {
        "ok": True,
        "session_id": session_id,
        "model": latest.model,
        "context_window": cw,
        "last_input_tokens": last_in,
        "used_percentage": used_pct,
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


def _summarize_magpie_usage(range_str: str, surface: str, since: int | None, until: int | None) -> dict[str, Any]:
    path = os.path.expanduser("~/.config/magpie/usage.jsonl")
    if not os.path.isfile(path):
        return {"range": range_str, "surface": surface, "since": 0, "generatedAt": datetime.now().isoformat(), "summary": {"requests": 0, "totalTokens": 0, "inputTokens": 0, "outputTokens": 0}, "days": [], "models": [], "providers": [], "accounts": []}

    now_dt = datetime.now().astimezone()
    since_dt = None
    until_dt = None
    if since is not None and until is not None:
        since_dt = datetime.fromtimestamp(since / 1000.0, tz=now_dt.tzinfo)
        until_dt = datetime.fromtimestamp(until / 1000.0, tz=now_dt.tzinfo)
    elif range_str == "7d":
        since_dt = now_dt - timedelta(days=7)
    elif range_str == "30d":
        since_dt = now_dt - timedelta(days=30)

    records = []
    try:
        with open(path, "r", encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if not line:
                    continue
                try:
                    rec = json.loads(line)
                    t_str = rec.get("t")
                    if not t_str:
                        continue
                    dt = datetime.fromisoformat(t_str)
                    if since_dt and dt < since_dt:
                        continue
                    if until_dt and dt > until_dt:
                        continue
                    agent = str(rec.get("agent") or "").lower()
                    if surface != "all":
                        if surface == "codex" and "codex" not in agent:
                            continue
                        elif surface == "grok" and "grok" not in agent:
                            continue
                        elif surface == "claude" and "claude" not in agent:
                            continue
                    records.append((dt, rec))
                except Exception:
                    pass
    except Exception:
        pass

    total_requests = len(records)
    total_tokens = sum(int(r.get("in") or 0) + int(r.get("out") or 0) for _, r in records)
    input_tokens = sum(int(r.get("in") or 0) for _, r in records)
    output_tokens = sum(int(r.get("out") or 0) for _, r in records)
    cache_read = sum(int(r.get("cache_read") or 0) for _, r in records)
    reasoning_tokens = sum(int(r.get("reasoning") or 0) for _, r in records)

    days_map: dict[str, dict[str, Any]] = {}
    models_map: dict[str, dict[str, Any]] = {}
    providers_map: dict[str, dict[str, Any]] = {}

    for dt, r in records:
        day_str = dt.strftime("%Y-%m-%d")
        if day_str not in days_map:
            days_map[day_str] = {"date": day_str, "requests": 0, "measuredRequests": 0, "reportedRequests": 0, "totalTokens": 0, "estimatedCostUsd": 0.0, "models": {}}
        day_entry = days_map[day_str]
        day_entry["requests"] += 1
        toks = int(r.get("in") or 0) + int(r.get("out") or 0)
        day_entry["totalTokens"] += toks

        m_name = str(r.get("served") or r.get("model") or "unknown")
        p_name = str(r.get("provider") or "magpie")
        if m_name not in day_entry["models"]:
            day_entry["models"][m_name] = {"model": m_name, "provider": p_name, "requests": 0, "totalTokens": 0, "inputTokens": 0, "outputTokens": 0}
        dm = day_entry["models"][m_name]
        dm["requests"] += 1
        dm["totalTokens"] += toks
        dm["inputTokens"] += int(r.get("in") or 0)
        dm["outputTokens"] += int(r.get("out") or 0)

        # models breakdown
        if m_name not in models_map:
            models_map[m_name] = {"name": m_name, "model": m_name, "provider": p_name, "requests": 0, "totalTokens": 0, "inputTokens": 0, "outputTokens": 0, "cacheReadInputTokens": 0, "reasoningOutputTokens": 0, "estimatedCostUsd": 0.0, "shareRatio": 0.0}
        mb = models_map[m_name]
        mb["requests"] += 1
        mb["totalTokens"] += toks
        mb["inputTokens"] += int(r.get("in") or 0)
        mb["outputTokens"] += int(r.get("out") or 0)
        mb["cacheReadInputTokens"] += int(r.get("cache_read") or 0)
        mb["reasoningOutputTokens"] += int(r.get("reasoning") or 0)

        # providers breakdown
        if p_name not in providers_map:
            providers_map[p_name] = {"name": p_name, "model": p_name, "provider": p_name, "requests": 0, "totalTokens": 0, "inputTokens": 0, "outputTokens": 0, "cacheReadInputTokens": 0, "reasoningOutputTokens": 0, "estimatedCostUsd": 0.0, "shareRatio": 0.0}
        pb = providers_map[p_name]
        pb["requests"] += 1
        pb["totalTokens"] += toks
        pb["inputTokens"] += int(r.get("in") or 0)
        pb["outputTokens"] += int(r.get("out") or 0)
        pb["cacheReadInputTokens"] += int(r.get("cache_read") or 0)
        pb["reasoningOutputTokens"] += int(r.get("reasoning") or 0)

    # calc share ratios
    for mb in models_map.values():
        mb["shareRatio"] = round(mb["totalTokens"] / max(total_tokens, 1), 4)
    for pb in providers_map.values():
        pb["shareRatio"] = round(pb["totalTokens"] / max(total_tokens, 1), 4)

    days_list = []
    for day_str in sorted(days_map.keys()):
        d = days_map[day_str]
        days_list.append({
            "date": d["date"],
            "requests": d["requests"],
            "measuredRequests": d["requests"],
            "reportedRequests": d["requests"],
            "totalTokens": d["totalTokens"],
            "estimatedCostUsd": 0.0,
            "models": list(d["models"].values()),
        })

    return {
        "range": range_str,
        "surface": surface,
        "since": int(since_dt.timestamp() * 1000) if since_dt else 0,
        "generatedAt": now_dt.isoformat(),
        "summary": {
            "requests": total_requests,
            "totalTokens": total_tokens,
            "inputTokens": input_tokens,
            "outputTokens": output_tokens,
            "cacheReadInputTokens": cache_read,
            "reasoningOutputTokens": reasoning_tokens,
            "estimatedCostUsd": 0.0,
        },
        "days": days_list,
        "models": sorted(models_map.values(), key=lambda x: x["totalTokens"], reverse=True),
        "providers": sorted(providers_map.values(), key=lambda x: x["totalTokens"], reverse=True),
        "accounts": [],
    }


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
    gw_type = str(config.get("gateway_type") or "opencodex").strip().lower()
    if gw_type == "magpie":
        return _summarize_magpie_usage(range, surface, since, until)

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
        gw_type = str(config.get("gateway_type") or "opencodex").strip().lower()
        cached = _quota_snapshot_cache.get("payload")
        if cached and now < _quota_snapshot_cache.get("expires_at", 0):
            models, combos, reports, accounts = cached
        elif gw_type == "magpie":
            async with httpx.AsyncClient(follow_redirects=True, timeout=15.0) as client:
                models_url = f"{config['inference_url']}/models" if not config['inference_url'].endswith("/models") else config['inference_url']
                models_resp = await client.get(models_url, headers=headers)
                models_resp.raise_for_status()
                raw_models = models_resp.json().get("data", [])
                models = [
                    {"id": m.get("id"), "namespaced": m.get("id"), "provider": m.get("owned_by") or (m.get("id", "").split("/")[0] if "/" in m.get("id", "") else "magpie"), "disabled": False}
                    for m in raw_models if isinstance(m, dict) and m.get("id")
                ]
                combos = []
                reports = []
                accounts = []
                quotas_url = "http://127.0.0.1:3425/v1/magpie/quotas"
                try:
                    q_resp = await client.get(quotas_url, headers=headers, timeout=5.0)
                    if q_resp.status_code == 200:
                        q_data = q_resp.json().get("data", [])
                        for item in q_data:
                            prov = str(item.get("provider") or "").strip().lower()
                            pname = str(item.get("name") or prov)
                            user = str(item.get("user") or "")
                            wins = item.get("windows") or []
                            five_h = next((w for w in wins if "5" in str(w.get("name", ""))), None)
                            weekly = next((w for w in wins if "7" in str(w.get("name", "")) or "week" in str(w.get("name", "")).lower()), None)
                            if not weekly and wins:
                                weekly = wins[0]
                            rep_quota = {}
                            if five_h:
                                rep_quota["fiveHourPercent"] = round(float(five_h.get("used") or 0), 1)
                            if weekly:
                                rep_quota["weeklyPercent"] = round(float(weekly.get("used") or 0), 1)
                            accounts.append({
                                "id": user or pname,
                                "email": user,
                                "provider": prov,
                                "logLabel": f"{pname} ({user})" if user else pname,
                                "plan": item.get("plan", ""),
                                "paused": False,
                                "quota": {
                                    "shortPercent": rep_quota.get("fiveHourPercent"),
                                    "weeklyPercent": rep_quota.get("weeklyPercent"),
                                }
                            })
                            reports.append({
                                "provider": prov,
                                "label": f"{pname} - {user}" if user else pname,
                                "quota": rep_quota,
                            })
                except Exception:
                    pass
                _quota_snapshot_cache["expires_at"] = now + 60.0
                _quota_snapshot_cache["payload"] = (models, combos, reports, accounts)
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
