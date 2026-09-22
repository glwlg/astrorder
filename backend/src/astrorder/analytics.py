import glob
import json
import logging
import os
import sqlite3
from datetime import date, datetime, timedelta, timezone
from typing import Any

from fastapi import APIRouter, HTTPException, Query, Request
from sqlalchemy import func, select, desc
from sqlalchemy.dialects.sqlite import insert

from .auth import require_browser
from .models import TokenMetricRow, SessionRow

logger = logging.getLogger(__name__)

router = APIRouter()


def _private(request: Request) -> None:
    require_browser(request, request.app.state.settings)


def sync_all_agent_token_metrics(store: Any) -> dict[str, int]:
    scanned_codex = 0
    scanned_hermes = 0
    scanned_grok = 0
    records_to_upsert: list[dict[str, Any]] = []

    codex_pattern = os.path.expanduser("~/.codex/sessions/**/*.jsonl")
    for fpath in glob.glob(codex_pattern, recursive=True):
        fname = os.path.basename(fpath)
        parts = fname.replace(".jsonl", "").split("-")
        sid = "-".join(parts[-5:]) if len(parts) >= 5 else None
        if not sid or len(sid) != 36:
            continue

        model_name = "unknown"
        provider = "codex"
        context_window = 0
        last_input_tokens = 0
        latest_tot: dict[str, Any] = {}
        file_date = ""

        try:
            with open(fpath, "r", encoding="utf-8", errors="ignore") as f:
                for line in f:
                    if not line.strip():
                        continue
                    if '"turn_context"' in line:
                        try:
                            d = json.loads(line)
                            m = d.get("payload", {}).get("model")
                            if m:
                                model_name = m
                            ts = d.get("timestamp")
                            if ts and not file_date:
                                file_date = ts[:10]
                        except Exception:
                            pass
                    if '"total_token_usage"' in line:
                        try:
                            d = json.loads(line)
                            info = d.get("payload", {}).get("info", {})
                            if "total_token_usage" in info:
                                latest_tot = info["total_token_usage"]
                                context_window = info.get("model_context_window", context_window)
                                last_u = info.get("last_token_usage", {})
                                if "input_tokens" in last_u:
                                    last_input_tokens = last_u["input_tokens"]
                            ts = d.get("timestamp")
                            if ts:
                                file_date = ts[:10]
                        except Exception:
                            pass
        except Exception:
            continue

        if latest_tot:
            scanned_codex += 1
            if not file_date:
                mtime = os.path.getmtime(fpath)
                file_date = datetime.fromtimestamp(mtime, tz=timezone.utc).strftime("%Y-%m-%d")

            records_to_upsert.append({
                "agent_id": "local-codex",
                "session_id": sid,
                "model": model_name,
                "provider": provider,
                "date": file_date,
                "input_tokens": latest_tot.get("input_tokens", 0),
                "output_tokens": latest_tot.get("output_tokens", 0),
                "cached_tokens": latest_tot.get("cached_input_tokens", 0),
                "reasoning_tokens": latest_tot.get("reasoning_output_tokens", 0),
                "total_tokens": latest_tot.get("total_tokens", 0),
                "context_window": context_window or 1000000,
                "last_input_tokens": last_input_tokens or latest_tot.get("input_tokens", 0),
            })

    hermes_db = os.path.expanduser("~/AppData/Local/hermes/state.db")
    if os.path.exists(hermes_db):
        try:
            conn = sqlite3.connect(hermes_db)
            c = conn.cursor()
            c.execute("""
                SELECT session_id, model, billing_provider, input_tokens, output_tokens,
                       cache_read_tokens, reasoning_tokens, first_seen, last_seen
                FROM session_model_usage
            """)
            rows = c.fetchall()
            for r in rows:
                sid, model, prov, in_tok, out_tok, cache_tok, r_tok, f_seen, l_seen = r
                actual_in = max(in_tok or 0, cache_tok or 0)
                date_str = datetime.fromtimestamp(l_seen or f_seen or 0, tz=timezone.utc).strftime("%Y-%m-%d")
                scanned_hermes += 1
                cw = 256000 if "kimi" in (model or "").lower() else 128000
                records_to_upsert.append({
                    "agent_id": "local-hermes-default",
                    "session_id": sid,
                    "model": model or "unknown",
                    "provider": prov or "hermes",
                    "date": date_str,
                    "input_tokens": actual_in,
                    "output_tokens": out_tok or 0,
                    "cached_tokens": cache_tok or 0,
                    "reasoning_tokens": r_tok or 0,
                    "total_tokens": actual_in + (out_tok or 0),
                    "context_window": cw,
                    "last_input_tokens": actual_in,
                })
            conn.close()
        except Exception:
            pass

    grok_pattern = os.path.expanduser("~/.grok/sessions/**/updates.jsonl")
    for fpath in glob.glob(grok_pattern, recursive=True):
        try:
            with open(fpath, "r", encoding="utf-8", errors="ignore") as f:
                for line in f:
                    if "turn_completed" in line and "usage" in line:
                        d = json.loads(line)
                        p = d.get("params", {})
                        sid = p.get("sessionId")
                        upd = p.get("update", {})
                        usage = upd.get("usage", {})
                        if not sid or not usage:
                            continue
                        scanned_grok += 1
                        ts = d.get("timestamp", 0)
                        date_str = datetime.fromtimestamp(ts, tz=timezone.utc).strftime("%Y-%m-%d") if ts else datetime.now(timezone.utc).strftime("%Y-%m-%d")
                        model_usage = usage.get("modelUsage", {})
                        m_name = next(iter(model_usage.keys())) if model_usage else "grok"
                        records_to_upsert.append({
                            "agent_id": "local-grok",
                            "session_id": sid,
                            "model": m_name,
                            "provider": "grok",
                            "date": date_str,
                            "input_tokens": usage.get("inputTokens", 0),
                            "output_tokens": usage.get("outputTokens", 0),
                            "cached_tokens": usage.get("cachedReadTokens", 0),
                            "reasoning_tokens": usage.get("reasoningTokens", 0),
                            "total_tokens": usage.get("totalTokens", 0),
                            "context_window": 131072,
                            "last_input_tokens": usage.get("inputTokens", 0),
                        })
        except Exception:
            continue

    now = datetime.now(timezone.utc)
    with store.session() as db:
        for r in records_to_upsert:
            stmt = insert(TokenMetricRow).values(
                agent_id=r["agent_id"],
                session_id=r["session_id"],
                model=r["model"],
                provider=r["provider"],
                date=r["date"],
                input_tokens=r["input_tokens"],
                output_tokens=r["output_tokens"],
                cached_tokens=r["cached_tokens"],
                reasoning_tokens=r["reasoning_tokens"],
                total_tokens=r["total_tokens"],
                context_window=r["context_window"],
                last_input_tokens=r["last_input_tokens"],
                updated_at=now,
            )
            stmt = stmt.on_conflict_do_update(
                index_elements=["agent_id", "session_id", "model", "date"],
                set_={
                    "input_tokens": func.max(TokenMetricRow.input_tokens, r["input_tokens"]),
                    "output_tokens": func.max(TokenMetricRow.output_tokens, r["output_tokens"]),
                    "cached_tokens": func.max(TokenMetricRow.cached_tokens, r["cached_tokens"]),
                    "reasoning_tokens": func.max(TokenMetricRow.reasoning_tokens, r["reasoning_tokens"]),
                    "total_tokens": func.max(TokenMetricRow.total_tokens, r["total_tokens"]),
                    "context_window": r["context_window"],
                    "last_input_tokens": r["last_input_tokens"],
                    "updated_at": now,
                },
            )
            db.execute(stmt)

    return {
        "codex": scanned_codex,
        "hermes": scanned_hermes,
        "grok": scanned_grok,
        "total_records": len(records_to_upsert),
    }


@router.get("/api/v1/sessions/{session_id}/usage")
def get_session_usage(session_id: str, request: Request, agent_id: str | None = None) -> dict[str, Any]:
    _private(request)
    store = request.app.state.store

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
        except Exception:
            pass
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
                "total_tokens": last_u.get("total_tokens", 0),
                "input_tokens": in_tok,
                "output_tokens": last_u.get("output_tokens", 0),
                "cached_tokens": cached,
                "reasoning_tokens": last_u.get("reasoning_output_tokens", 0),
                "cache_hit_rate": round((cached / max(in_tok, 1)) * 100, 1) if in_tok > 0 else 0,
            }

    if not rows:
        return {
            "ok": True,
            "session_id": session_id,
            "model": "default",
            "context_window": 1000000,
            "last_input_tokens": 0,
            "used_percentage": 0,
            "total_tokens": 0,
            "input_tokens": 0,
            "output_tokens": 0,
            "cached_tokens": 0,
            "reasoning_tokens": 0,
            "cache_hit_rate": 0,
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
        "total_tokens": latest.total_tokens,
        "input_tokens": in_tok,
        "output_tokens": latest.output_tokens,
        "cached_tokens": cached,
        "reasoning_tokens": latest.reasoning_tokens,
        "cache_hit_rate": round((cached / max(in_tok, 1)) * 100, 1) if in_tok > 0 else 0,
    }


@router.get("/api/v1/analytics/overview")
def get_analytics_overview(request: Request) -> dict[str, Any]:
    _private(request)
    store = request.app.state.store

    with store.session() as db:
        today_str = datetime.now(timezone.utc).strftime("%Y-%m-%d")

        tot_q = select(
            func.sum(TokenMetricRow.total_tokens),
            func.sum(TokenMetricRow.input_tokens),
            func.sum(TokenMetricRow.output_tokens),
            func.sum(TokenMetricRow.cached_tokens),
            func.sum(TokenMetricRow.reasoning_tokens),
            func.count(func.distinct(TokenMetricRow.session_id)),
        )
        total_res = db.execute(tot_q).first()
        t_tokens = total_res[0] or 0
        t_input = total_res[1] or 0
        t_output = total_res[2] or 0
        t_cached = total_res[3] or 0
        t_reasoning = total_res[4] or 0
        session_count = total_res[5] or 0

        today_q = select(
            func.sum(TokenMetricRow.total_tokens),
            func.sum(TokenMetricRow.input_tokens),
            func.sum(TokenMetricRow.output_tokens),
            func.sum(TokenMetricRow.cached_tokens),
        ).where(TokenMetricRow.date == today_str)
        today_res = db.execute(today_q).first()
        today_tokens = today_res[0] or 0
        today_input = today_res[1] or 0
        today_output = today_res[2] or 0
        today_cached = today_res[3] or 0

        model_q = select(
            TokenMetricRow.model,
            func.sum(TokenMetricRow.total_tokens).label("tokens"),
            func.sum(TokenMetricRow.input_tokens).label("in_tok"),
            func.sum(TokenMetricRow.output_tokens).label("out_tok"),
            func.sum(TokenMetricRow.cached_tokens).label("cache_tok"),
            func.count(func.distinct(TokenMetricRow.session_id)).label("sess_cnt"),
        ).group_by(TokenMetricRow.model).order_by(desc("tokens"))
        models = []
        for r in db.execute(model_q).all():
            m_in = r.in_tok or 0
            m_cache = r.cache_tok or 0
            models.append({
                "model": r.model,
                "total_tokens": r.tokens or 0,
                "input_tokens": m_in,
                "output_tokens": r.out_tok or 0,
                "cached_tokens": m_cache,
                "session_count": r.sess_cnt or 0,
                "cache_hit_rate": min(100.0, round((m_cache / max(m_in, 1)) * 100, 1)) if m_in > 0 else 0,
                "share": round(((r.tokens or 0) / max(t_tokens, 1)) * 100, 1),
            })

        agent_q = select(
            TokenMetricRow.agent_id,
            TokenMetricRow.provider,
            func.sum(TokenMetricRow.total_tokens).label("tokens"),
            func.sum(TokenMetricRow.input_tokens).label("in_tok"),
            func.sum(TokenMetricRow.output_tokens).label("out_tok"),
            func.sum(TokenMetricRow.cached_tokens).label("cache_tok"),
            func.count(func.distinct(TokenMetricRow.session_id)).label("sess_cnt"),
        ).group_by(TokenMetricRow.agent_id, TokenMetricRow.provider).order_by(desc("tokens"))
        agents_data = [
            {
                "agent_id": r.agent_id,
                "provider": r.provider or "codex",
                "tokens": r.tokens or 0,
                "input_tokens": r.in_tok or 0,
                "output_tokens": r.out_tok or 0,
                "cached_tokens": r.cache_tok or 0,
                "session_count": r.sess_cnt or 0,
                "cache_hit_rate": min(100.0, round(((r.cache_tok or 0) / max(r.in_tok or 0, 1)) * 100, 1)) if (r.in_tok or 0) > 0 else 0,
                "share": round(((r.tokens or 0) / max(t_tokens, 1)) * 100, 1),
            }
            for r in db.execute(agent_q).all()
        ]

    cache_hit_rate = min(100.0, round((t_cached / max(t_input, 1)) * 100, 1)) if t_input > 0 else 0
    today_cache_hit_rate = min(100.0, round((today_cached / max(today_input, 1)) * 100, 1)) if today_input > 0 else 0

    return {
        "ok": True,
        "totals": {
            "total_tokens": t_tokens,
            "input_tokens": t_input,
            "output_tokens": t_output,
            "cached_tokens": t_cached,
            "reasoning_tokens": t_reasoning,
            "session_count": session_count,
            "cache_hit_rate": cache_hit_rate,
        },
        "today": {
            "date": today_str,
            "total_tokens": today_tokens,
            "input_tokens": today_input,
            "output_tokens": today_output,
            "cached_tokens": today_cached,
            "cache_hit_rate": today_cache_hit_rate,
        },
        "by_model": models,
        "by_agent": agents_data,
    }


@router.get("/api/v1/analytics/calendar")
def get_analytics_calendar(request: Request, days: int = Query(default=365, ge=30, le=730)) -> dict[str, Any]:
    _private(request)
    store = request.app.state.store

    start_d = date.today() - timedelta(days=days - 1)
    start_date_str = start_d.strftime("%Y-%m-%d")

    with store.session() as db:
        q = select(
            TokenMetricRow.date,
            func.sum(TokenMetricRow.total_tokens).label("tokens"),
            func.sum(TokenMetricRow.input_tokens).label("in_tok"),
            func.sum(TokenMetricRow.output_tokens).label("out_tok"),
            func.sum(TokenMetricRow.cached_tokens).label("cache_tok"),
            func.count(func.distinct(TokenMetricRow.session_id)).label("sessions"),
        ).where(TokenMetricRow.date >= start_date_str).group_by(TokenMetricRow.date).order_by(TokenMetricRow.date.asc())

        rows = db.execute(q).all()

    data_map = {}
    max_tokens = 0
    for r in rows:
        tok = r.tokens or 0
        if tok > max_tokens:
            max_tokens = tok
        m_in = r.in_tok or 0
        m_cache = r.cache_tok or 0
        data_map[r.date] = {
            "date": r.date,
            "tokens": tok,
            "input_tokens": m_in,
            "output_tokens": r.out_tok or 0,
            "cached_tokens": m_cache,
            "session_count": r.sessions or 0,
            "cache_hit_rate": round((m_cache / max(m_in, 1)) * 100, 1) if m_in > 0 else 0,
        }

    items = []
    for i in range(days):
        cur_d = (start_d + timedelta(days=i)).strftime("%Y-%m-%d")
        if cur_d in data_map:
            items.append(data_map[cur_d])
        else:
            items.append({
                "date": cur_d,
                "tokens": 0,
                "input_tokens": 0,
                "output_tokens": 0,
                "cached_tokens": 0,
                "session_count": 0,
                "cache_hit_rate": 0,
            })

    return {
        "ok": True,
        "days": len(items),
        "max_daily_tokens": max_tokens,
        "items": items,
    }


@router.get("/api/v1/analytics/top-sessions")
def get_top_sessions(request: Request, limit: int = Query(default=10, ge=1, le=50)) -> dict[str, Any]:
    _private(request)
    store = request.app.state.store

    with store.session() as db:
        q = select(
            TokenMetricRow.session_id,
            TokenMetricRow.agent_id,
            TokenMetricRow.model,
            func.sum(TokenMetricRow.total_tokens).label("tokens"),
            func.sum(TokenMetricRow.input_tokens).label("in_tok"),
            func.sum(TokenMetricRow.output_tokens).label("out_tok"),
            func.sum(TokenMetricRow.cached_tokens).label("cache_tok"),
            func.max(TokenMetricRow.date).label("latest_date"),
        ).group_by(TokenMetricRow.session_id, TokenMetricRow.agent_id, TokenMetricRow.model).order_by(desc("tokens")).limit(limit)

        rows = db.execute(q).all()

        results = []
        for r in rows:
            sess_row = db.execute(
                select(SessionRow).where(SessionRow.agent_id == r.agent_id, SessionRow.id == r.session_id)
            ).scalar_one_or_none()
            title = sess_row.title if sess_row else r.session_id
            m_in = r.in_tok or 0
            m_cache = r.cache_tok or 0
            results.append({
                "session_id": r.session_id,
                "agent_id": r.agent_id,
                "title": title,
                "model": r.model,
                "total_tokens": r.tokens or 0,
                "input_tokens": m_in,
                "output_tokens": r.out_tok or 0,
                "cached_tokens": m_cache,
                "cache_hit_rate": round((m_cache / max(m_in, 1)) * 100, 1) if m_in > 0 else 0,
                "latest_date": r.latest_date,
            })

    return {"ok": True, "items": results}


@router.post("/api/v1/analytics/sync")
def trigger_analytics_sync(request: Request) -> dict[str, Any]:
    _private(request)
    store = request.app.state.store
    res = sync_all_agent_token_metrics(store)
    return {"ok": True, "result": res}
