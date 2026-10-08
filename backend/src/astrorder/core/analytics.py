import asyncio
import glob
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


def _active_secret(payload: dict[str, Any], current: dict[str, Any], name: str, clear_name: str) -> str:
    target = str(payload.get("gateway_type") or current.get("gateway_type") or "opencodex").strip().lower()
    if payload.get(clear_name) is True:
        return ""
    if name in payload:
        return str(payload.get(name) or "").strip()
    if target == str(current.get("gateway_type") or ""):
        return str(current.get(name) or "")
    saved = (current.get("gateway_profiles") or {}).get(target) or {}
    return str(saved.get(name) or "")


def _magpie_period(range_str: str, since: int | None) -> str:
    if since is None:
        return range_str if range_str in {"7d", "30d"} else "all"
    now = datetime.now().astimezone()
    start = datetime.fromtimestamp(since / 1000, tz=now.tzinfo)
    today = now.replace(hour=0, minute=0, second=0, microsecond=0)
    if start >= today - timedelta(days=6):
        return "7d"
    if start >= today - timedelta(days=29):
        return "30d"
    return "all"


def _magpie_url(config: dict[str, Any], resource: str) -> str:
    return f"{str(config.get('management_url') or '').rstrip('/')}/api/{resource}"


def _magpie_params(config: dict[str, Any], params: dict[str, Any]) -> dict[str, Any]:
    query = {key: value for key, value in params.items() if value not in (None, "")}
    key = str(config.get("management_key") or "").strip()
    if key:
        query["k"] = key
    return query


async def _magpie_get(client: httpx.AsyncClient, config: dict[str, Any], resource: str, params: dict[str, Any]) -> Any:
    response = await client.get(_magpie_url(config, resource), params=_magpie_params(config, params))
    response.raise_for_status()
    return response.json()


def _whole(value: Any) -> int:
    try:
        return int(value or 0)
    except (TypeError, ValueError):
        return 0


def _amount(value: Any) -> float:
    try:
        return float(value or 0)
    except (TypeError, ValueError):
        return 0.0


def _when(value: Any) -> datetime | None:
    if not value:
        return None
    text = str(value).replace("Z", "+00:00")
    try:
        return datetime.fromisoformat(text)
    except ValueError:
        return None


def _epoch_ms(value: Any) -> int | None:
    parsed = _when(value)
    return int(parsed.timestamp() * 1000) if parsed else None


def _coverage(calls: int, unpriced: int) -> float:
    return (calls - unpriced) / calls if calls else 0.0


def _summary_from_totals(item: dict[str, Any]) -> dict[str, Any]:
    calls = _whole(item.get("calls"))
    input_tokens = _whole(item.get("input"))
    output_tokens = _whole(item.get("output"))
    cache_read = _whole(item.get("cache_read"))
    unpriced = _whole(item.get("unpriced"))
    observed = input_tokens + cache_read
    return {
        "requests": calls,
        "attemptCount": calls,
        "measuredRequests": calls,
        "reportedRequests": calls,
        "estimatedRequests": 0,
        "unreportedRequests": 0,
        "unmeteredRequests": 0,
        "totalTokens": input_tokens + output_tokens,
        "inputTokens": input_tokens,
        "outputTokens": output_tokens,
        "cacheReadInputTokens": cache_read,
        "cacheObservedInputTokens": observed,
        "reasoningOutputTokens": _whole(item.get("reasoning")),
        "estimatedCostUsd": _amount(item.get("cost")),
        "pricedRequests": max(calls - unpriced, 0),
        "unpricedRequests": unpriced,
        "coverageRatio": 1 if calls else 0,
    }


def _share_ratios(rows: list[dict[str, Any]]) -> None:
    total = sum(_whole(row.get("totalTokens")) for row in rows)
    for row in rows:
        row["shareRatio"] = round(row["totalTokens"] / total, 4) if total else 0.0


def _usage_row(item: dict[str, Any], model: str, provider: str) -> dict[str, Any]:
    calls = _whole(item.get("calls"))
    input_tokens = _whole(item.get("input"))
    output_tokens = _whole(item.get("output"))
    cache_read = _whole(item.get("cache_read"))
    unpriced = _whole(item.get("unpriced"))
    observed = input_tokens + cache_read
    total = input_tokens + output_tokens or _whole(item.get("tokens"))
    return {
        "provider": provider,
        "model": model,
        "requests": calls,
        "attemptCount": calls,
        "measuredRequests": calls,
        "reportedRequests": calls,
        "estimatedRequests": 0,
        "totalTokens": total,
        "inputTokens": input_tokens,
        "outputTokens": output_tokens,
        "cachedInputTokens": cache_read,
        "cacheReadInputTokens": cache_read,
        "cacheCreationInputTokens": _whole(item.get("cache_write")),
        "cacheHitRate": (cache_read / observed) if observed else None,
        "cacheObservedInputTokens": observed,
        "priceCoverageRatio": _coverage(calls, unpriced),
        "pricedRequests": max(calls - unpriced, 0),
        "unpricedRequests": unpriced,
        "shareRatio": 0.0,
        "estimatedCostUsd": _amount(item.get("cost")),
        "reasoningOutputTokens": _whole(item.get("reasoning")),
    }


def _model_at(item: dict[str, Any]) -> tuple[str, str]:
    raw = str(item.get("id") or "")
    provider, sep, model = raw.partition("/")
    if not sep:
        model = str(item.get("name") or raw or "unknown")
        provider = ""
    label = str(item.get("name") or "")
    if " · " in label:
        provider = label.split(" · ", 1)[1]
    return model or "unknown", provider


def _rows_from_shares(items: Any, kind: str) -> list[dict[str, Any]]:
    rows = []
    for item in items or []:
        if not isinstance(item, dict):
            continue
        if kind == "provider":
            name = str(item.get("name") or item.get("id") or "unknown")
            rows.append(_usage_row(item, name, name))
        else:
            model, provider = _model_at(item)
            rows.append(_usage_row(item, model, provider))
    rows.sort(key=lambda row: row["totalTokens"], reverse=True)
    _share_ratios(rows)
    return rows


def _rows_from_points(points: list[dict[str, Any]], kind: str) -> list[dict[str, Any]]:
    grouped: dict[str, dict[str, Any]] = {}
    for point in points:
        parts = (point.get("by") or {}).get(kind) or {}
        if not isinstance(parts, dict):
            continue
        for key, part in parts.items():
            if not isinstance(part, dict):
                continue
            current = grouped.setdefault(str(key), {"id": key, "calls": 0, "tokens": 0, "cost": 0.0})
            current["calls"] += _whole(part.get("calls"))
            current["tokens"] += _whole(part.get("tokens"))
            current["cost"] += _amount(part.get("cost"))
    return _rows_from_shares(list(grouped.values()), "model" if kind == "modelAt" else "provider")


def _sum_points(points: list[dict[str, Any]]) -> dict[str, Any]:
    total = {"calls": 0, "input": 0, "output": 0, "cache_read": 0, "cache_write": 0, "reasoning": 0, "cost": 0.0, "unpriced": 0}
    for point in points:
        for key in ("calls", "input", "output", "cache_read", "cache_write", "reasoning", "unpriced"):
            total[key] += _whole(point.get(key))
        total["cost"] += _amount(point.get("cost"))
    return total


def _days_from_series(series: Any, since: int | None, until: int | None) -> tuple[list[dict[str, Any]], list[dict[str, Any]], bool]:
    kept: list[dict[str, Any]] = []
    dropped = False
    for point in series or []:
        if not isinstance(point, dict):
            continue
        when = _when(point.get("time"))
        if when is None:
            continue
        stamp = when.timestamp() * 1000
        if (since is not None and stamp < since) or (until is not None and stamp > until):
            dropped = True
            continue
        kept.append(point)
    days = []
    for point in kept:
        when = _when(point.get("time"))
        assert when is not None
        parts = (point.get("by") or {}).get("modelAt") or {}
        models = []
        if isinstance(parts, dict):
            for key, part in parts.items():
                if not isinstance(part, dict):
                    continue
                provider, sep, model = str(key).partition("/")
                if not sep:
                    model, provider = provider, ""
                models.append({
                    "model": model or provider or "unknown",
                    "provider": provider,
                    "requests": _whole(part.get("calls")),
                    "totalTokens": _whole(part.get("tokens")),
                })
        models.sort(key=lambda item: item["totalTokens"], reverse=True)
        calls = _whole(point.get("calls"))
        token_total = _whole(point.get("input")) + _whole(point.get("output"))
        stacked = sum(item["totalTokens"] for item in models)
        days.append({
            "date": when.strftime("%Y-%m-%d"),
            "requests": calls,
            "measuredRequests": calls,
            "reportedRequests": calls,
            "totalTokens": max(token_total, stacked),
            "estimatedCostUsd": _amount(point.get("cost")),
            "models": models,
        })
    days.sort(key=lambda item: item["date"])
    return days, kept, dropped


def _account_rows(items: Any) -> list[dict[str, Any]]:
    rows = []
    for item in items or []:
        if not isinstance(item, dict):
            continue
        name = str(item.get("name") or item.get("account") or item.get("id") or "未记录账号")
        provider = str(item.get("sub") or item.get("provider") or "")
        label = f"{name} · {provider}" if provider and provider not in name else name
        calls = _whole(item.get("calls"))
        unpriced = _whole(item.get("unpriced"))
        rows.append({
            "accountLogLabel": label,
            "ambiguous": False,
            "requests": calls,
            "attemptCount": calls,
            "measuredAttempts": calls,
            "reportedAttempts": calls,
            "estimatedAttempts": 0,
            "unmeteredAttempts": 0,
            "inputTokens": _whole(item.get("input")),
            "outputTokens": _whole(item.get("output")),
            "cacheReadInputTokens": _whole(item.get("cache_read")),
            "cacheCreationInputTokens": _whole(item.get("cache_write")),
            "reasoningOutputTokens": _whole(item.get("reasoning")),
            "totalTokens": _whole(item.get("input")) + _whole(item.get("output")),
            "usageCoverageRatio": 1 if calls else 0,
            "estimatedCostUsd": _amount(item.get("cost")),
            "pricedAttempts": max(calls - unpriced, 0),
            "unpricedAttempts": unpriced,
            "priceCoverageRatio": _coverage(calls, unpriced),
        })
    return rows


def magpie_usage_view(requests_payload: dict[str, Any], overview: dict[str, Any] | None, range_str: str, surface: str, since: int | None, until: int | None) -> dict[str, Any]:
    days, kept, dropped = _days_from_series(requests_payload.get("series"), since, until)
    if dropped:
        summary = _summary_from_totals(_sum_points(kept))
        models = _rows_from_points(kept, "modelAt")
        providers = _rows_from_points(kept, "provider")
        accounts: list[dict[str, Any]] = []
    else:
        summary = _summary_from_totals(requests_payload)
        by = requests_payload.get("by") or {}
        models = _rows_from_shares(by.get("modelAt"), "model")
        providers = _rows_from_shares(by.get("provider"), "provider")
        accounts = _account_rows((overview or {}).get("accounts")) if surface == "all" else []
    first = _when(kept[0].get("time")) if kept else None
    return {
        "range": range_str,
        "surface": surface,
        "since": since if since is not None else (int(first.timestamp() * 1000) if first else 0),
        "generatedAt": datetime.now().astimezone().isoformat(),
        "summary": summary,
        "days": days,
        "models": models,
        "providers": providers,
        "accounts": accounts,
        "historyTruncated": False,
        "truncatedPrefixBytes": 0,
        "entriesTruncated": False,
        "entriesDropped": 0,
    }


def _window_kind(name: str) -> str:
    text = " ".join(name.lower().split())
    if text.startswith("5 ") and "hour" in text:
        return "five"
    if "week" in text or (text.startswith("7 ") and "day" in text):
        return "week"
    if "month" in text or (text.startswith("30 ") and "day" in text):
        return "month"
    return "custom"


def magpie_quota_cards(payload: Any) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    items = payload if isinstance(payload, list) else []
    reports: list[dict[str, Any]] = []
    accounts: list[dict[str, Any]] = []
    for item in items:
        if not isinstance(item, dict):
            continue
        provider = str(item.get("provider") or "").strip().lower()
        name = str(item.get("name") or provider)
        user = str(item.get("user") or "")
        report_quota: dict[str, Any] = {}
        account_quota: dict[str, Any] = {}
        custom = []
        for window in item.get("windows") or []:
            if not isinstance(window, dict) or window.get("unlimited"):
                continue
            percent = round(_amount(window.get("used")), 1)
            reset = _epoch_ms(window.get("resetsAt"))
            kind = _window_kind(str(window.get("name") or ""))
            if kind == "five":
                report_quota["fiveHourPercent"] = percent
                account_quota["shortPercent"] = percent
                if reset:
                    report_quota["fiveHourResetAt"] = reset
                    account_quota["shortResetAt"] = reset
            elif kind == "week":
                report_quota["weeklyPercent"] = percent
                account_quota["weeklyPercent"] = percent
                if reset:
                    report_quota["weeklyResetAt"] = reset
                    account_quota["weeklyResetAt"] = reset
            elif kind == "month":
                report_quota["monthlyPercent"] = percent
                if reset:
                    report_quota["monthlyResetAt"] = reset
            else:
                custom.append({"label": str(window.get("name") or "额度"), "percent": percent, **({"resetAt": reset} if reset else {})})
        if custom:
            report_quota["customWindows"] = custom
        label = f"{name} - {user}" if user else name
        reports.append({"provider": provider, "label": label, "quota": report_quota})
        accounts.append({
            "id": user or name,
            "email": user,
            "provider": provider,
            "logLabel": f"{name} ({user})" if user else name,
            "plan": item.get("plan", ""),
            "paused": False,
            "quota": {key: value for key, value in account_quota.items() if value is not None},
        })
    return reports, accounts

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
            "api_key": _active_secret(payload, current, "api_key", "clear_key"),
            "management_key": _active_secret(payload, current, "management_key", "clear_management_key"),
        })
    except (TypeError, ValueError) as exc:
        raise HTTPException(status_code=422, detail=str(exc)) from exc
    return public_gateway_config(config)


async def _fetch_magpie_usage(config: dict[str, Any], range_str: str, surface: str, since: int | None, until: int | None) -> dict[str, Any]:
    if not str(config.get("management_key") or "").strip():
        raise HTTPException(status_code=422, detail="未配置 Magpie 管理端访问参数")
    period = _magpie_period(range_str, since)
    params: dict[str, Any] = {"period": period, "limit": 1}
    if surface != "all":
        params["agent"] = surface
    async with httpx.AsyncClient(follow_redirects=True, timeout=20.0) as client:
        requests_payload = await _magpie_get(client, config, "usage/requests", params)
        overview = None
        if surface == "all" and since is None and until is None:
            overview = await _magpie_get(client, config, "usage", {"period": period})
    if not isinstance(requests_payload, dict):
        raise TypeError("Magpie 用量格式无效")
    if overview is not None and not isinstance(overview, dict):
        raise TypeError("Magpie 用量汇总格式无效")
    return magpie_usage_view(requests_payload, overview, range_str, surface, since, until)


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
        try:
            return await _fetch_magpie_usage(config, range, surface, since, until)
        except HTTPException:
            raise
        except (httpx.HTTPError, ValueError, TypeError) as exc:
            raise HTTPException(status_code=502, detail=f"Magpie 用量请求失败：{exc}") from exc

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
        cache_key = (gw_type, config.get("management_url"), config.get("inference_url"))
        cached = _quota_snapshot_cache.get("payload")
        if cached and now < _quota_snapshot_cache.get("expires_at", 0) and _quota_snapshot_cache.get("key") == cache_key:
            models, combos, reports, accounts = cached
        elif gw_type == "magpie":
            if not str(config.get("management_key") or "").strip():
                raise HTTPException(status_code=422, detail="未配置 Magpie 管理端访问参数")
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
                reports, accounts = magpie_quota_cards(await _magpie_get(client, config, "usage/quotas", {}))
                _quota_snapshot_cache["expires_at"] = now + 60.0
                _quota_snapshot_cache["key"] = cache_key
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
                _quota_snapshot_cache["key"] = cache_key
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
            if "hermes" in plan["agents"] and plan.get("hermes", {}).get("status") != "未找到配置":
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
