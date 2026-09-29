"""Session usage and context window analytics for Hermes, Grok, and Codex."""
from __future__ import annotations

import glob
import json
import logging
import os
import sqlite3
import time
import tomllib
from pathlib import Path
from typing import Any
import yaml

from ..connections import run_subprocess_hidden
from ..ssh_transport import SshNativeRuntime, build_remote_python_command

logger = logging.getLogger(__name__)

_USAGE_CACHE_TTL = 4.0  # seconds
_usage_cache: dict[str, tuple[float, dict[str, Any]]] = {}

_GW_MODELS_CACHE_TTL = 60.0  # seconds
_gw_models_cache: tuple[float, dict[str, int]] | None = None


def _get_gateway_models_context_windows(store: Any | None = None) -> dict[str, int]:
    """Fetch and cache model context windows from configured LLM gateway."""
    global _gw_models_cache
    now = time.monotonic()
    if _gw_models_cache and now < _gw_models_cache[0]:
        return _gw_models_cache[1]

    model_map: dict[str, int] = {}
    inference_url = None
    api_key = None

    if store is not None:
        try:
            from .gateway_config import get_gateway_config
            cfg = get_gateway_config(store)
            inference_url = cfg.get("inference_url")
            api_key = cfg.get("api_key")
        except Exception:
            pass

    if not inference_url:
        inference_url = "https://llm.651971564.xyz/v1"

    try:
        import urllib.request
        req = urllib.request.Request(f"{inference_url.rstrip('/')}/models")
        if api_key:
            req.add_header("Authorization", f"Bearer {api_key}")
        with urllib.request.urlopen(req, timeout=3) as resp:
            data = json.loads(resp.read().decode())
            for m in data.get("data", []):
                mid = m.get("id")
                cw = (
                    m.get("context_window")
                    or m.get("context_length")
                    or (m.get("capabilities") or {}).get("context_length")
                )
                if mid and cw:
                    try:
                        model_map[mid] = int(cw)
                    except (ValueError, TypeError):
                        pass
        _gw_models_cache = (now + _GW_MODELS_CACHE_TTL, model_map)
    except Exception as exc:
        logger.debug("Failed fetching gateway models context length: %s", exc)
        if _gw_models_cache:
            return _gw_models_cache[1]

    return model_map


def _read_hermes_context_window(model_name: str | None, profile_name: str | None = None) -> int | None:
    if not model_name:
        return None
    # 1. Check Hermes endpoint_model_metadata.json cache
    meta_paths = [
        Path(os.path.expanduser("~/AppData/Local/hermes/cache/endpoint_model_metadata.json")),
        Path(os.path.expanduser("~/.hermes/cache/endpoint_model_metadata.json")),
    ]
    for p in meta_paths:
        if p.is_file():
            try:
                data = json.loads(p.read_text(encoding="utf-8"))
                for endpoint_data in data.values():
                    if isinstance(endpoint_data, dict):
                        models = endpoint_data.get("models") or {}
                        if model_name in models and models[model_name].get("context_length"):
                            return int(models[model_name]["context_length"])
            except Exception:
                pass

    # 2. Check Hermes config.yaml
    cfg_paths = [
        Path(os.path.expanduser("~/AppData/Local/hermes/config.yaml")),
        Path(os.path.expanduser("~/.hermes/config.yaml")),
    ]
    if profile_name and profile_name != "default":
        cfg_paths.insert(0, Path(os.path.expanduser(f"~/AppData/Local/hermes/profiles/{profile_name}/config.yaml")))
        cfg_paths.insert(1, Path(os.path.expanduser(f"~/.hermes/profiles/{profile_name}/config.yaml")))
    for p in cfg_paths:
        if p.is_file():
            try:
                cfg = yaml.safe_load(p.read_text(encoding="utf-8")) or {}
                model_cfg = cfg.get("model") or {}
                if model_cfg.get("context_length"):
                    return int(model_cfg["context_length"])
                providers = cfg.get("providers") or {}
                for prov in providers.values():
                    if isinstance(prov, dict):
                        p_models = prov.get("models") or {}
                        if model_name in p_models and isinstance(p_models[model_name], dict):
                            ctx = p_models[model_name].get("context_length") or p_models[model_name].get("context_window")
                            if ctx:
                                return int(ctx)
            except Exception:
                pass
    return None


def _read_codex_context_window(model_name: str | None) -> int | None:
    if not model_name:
        return None
    cat_path = Path(os.path.expanduser("~/.codex/opencodex-catalog.json"))
    if cat_path.is_file():
        try:
            data = json.loads(cat_path.read_text(encoding="utf-8"))
            for m in (data if isinstance(data, list) else data.get("models", [])):
                if m.get("slug") == model_name or m.get("id") == model_name:
                    cw = m.get("context_window") or m.get("max_context_window")
                    if cw:
                        return int(cw)
        except Exception:
            pass
    return None


def _read_grok_context_window(model_name: str | None) -> int | None:
    if not model_name:
        return None
    grok_path = Path(os.path.expanduser("~/.grok/config.toml"))
    if grok_path.is_file():
        try:
            cfg = tomllib.loads(grok_path.read_text(encoding="utf-8-sig"))
            models = cfg.get("model") or {}
            for m_key, m_val in models.items():
                if isinstance(m_val, dict) and (m_val.get("model") == model_name or m_key == model_name):
                    cw = m_val.get("context_window") or m_val.get("context_length")
                    if cw:
                        return int(cw)
        except Exception:
            pass
    return None


def resolve_model_context_window(
    model_name: str | None,
    agent_kind: str | None = None,
    profile_name: str | None = None,
    store: Any | None = None,
) -> int | None:
    """Resolve context window size (tokens): Agent config -> Gateway -> None (no fake data)."""
    if not model_name or model_name == "default":
        return None

    # 1. Read from Agent's own configuration
    if agent_kind == "hermes":
        cw = _read_hermes_context_window(model_name, profile_name)
        if cw:
            return cw
    elif agent_kind == "codex":
        cw = _read_codex_context_window(model_name)
        if cw:
            return cw
    elif agent_kind == "grok":
        cw = _read_grok_context_window(model_name)
        if cw:
            return cw
    else:
        cw = (
            _read_hermes_context_window(model_name, profile_name)
            or _read_codex_context_window(model_name)
            or _read_grok_context_window(model_name)
        )
        if cw:
            return cw

    # 2. Read from configured LLM Gateway
    gw_models = _get_gateway_models_context_windows(store)
    if model_name in gw_models:
        return gw_models[model_name]
    for key, val in gw_models.items():
        if (
            key.endswith(f"/{model_name}")
            or model_name.endswith(f"/{key}")
            or key.split("/")[-1] == model_name.split("/")[-1]
        ):
            return val

    # 3. No fake data fallback — return None
    return None


def _find_local_hermes_state_dbs(profile_name: str | None = None) -> list[Path]:
    dbs: list[Path] = []
    if os.name == "nt":
        appdata = os.environ.get("LOCALAPPDATA")
        if appdata:
            base = Path(appdata) / "hermes"
            if profile_name and profile_name != "default":
                dbs.append(base / "profiles" / profile_name / "state.db")
            dbs.append(base / "state.db")
    home = Path.home() / ".hermes"
    if profile_name and profile_name != "default":
        dbs.append(home / "profiles" / profile_name / "state.db")
    dbs.append(home / "state.db")
    return dbs


def _parse_hermes_row(row: dict[str, Any], profile_name: str | None = None, store: Any | None = None) -> dict[str, Any]:
    model = row.get("model") or "default"
    in_tok = int(row.get("input_tokens") or 0)
    cached = int(row.get("cache_read_tokens") or 0)
    out_tok = int(row.get("output_tokens") or 0)
    reasoning = int(row.get("reasoning_tokens") or 0)

    prompt_tokens = None
    model_config_raw = row.get("model_config")
    if model_config_raw:
        try:
            cfg = json.loads(model_config_raw) if isinstance(model_config_raw, str) else model_config_raw
            if isinstance(cfg, dict):
                anchor = cfg.get("_usage_anchor")
                if isinstance(anchor, dict):
                    prompt_tokens = anchor.get("prompt_tokens")
        except Exception:
            pass

    last_in = int(prompt_tokens if prompt_tokens is not None else in_tok)
    cw = resolve_model_context_window(model, agent_kind="hermes", profile_name=profile_name, store=store)
    if cw is not None and prompt_tokens is None and last_in > cw:
        last_in = min(last_in, cw)
    total = in_tok + cached + out_tok
    cache_hit_rate = round((cached / max(in_tok + cached, 1)) * 100, 1) if (in_tok + cached) > 0 else 0.0
    used_pct = round((last_in / cw) * 100, 2) if (cw and cw > 0) else None

    return {
        "model": model,
        "context_window": cw,
        "last_input_tokens": last_in,
        "used_percentage": used_pct,
        "total_tokens": total,
        "input_tokens": in_tok,
        "output_tokens": out_tok,
        "cached_tokens": cached,
        "reasoning_tokens": reasoning,
        "cache_hit_rate": cache_hit_rate,
    }


def _read_local_hermes_usage(session_id: str, profile_name: str | None = None, store: Any | None = None) -> dict[str, Any] | None:
    for db_path in _find_local_hermes_state_dbs(profile_name):
        if not db_path.is_file():
            continue
        try:
            conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
            conn.row_factory = sqlite3.Row
            row = conn.execute(
                "SELECT model, model_config, input_tokens, output_tokens, cache_read_tokens, reasoning_tokens "
                "FROM sessions WHERE id = ?",
                (session_id,),
            ).fetchone()
            conn.close()
            if row:
                return _parse_hermes_row(dict(row), profile_name=profile_name, store=store)
        except Exception as exc:
            logger.debug("Failed reading Hermes state.db at %s: %s", db_path, exc)
    return None


def _read_remote_hermes_usage(
    session_id: str,
    store: Any,
    connection_id: str,
    profile_name: str | None = None,
) -> dict[str, Any] | None:
    ssh_conn = store.get_ssh_connection(connection_id)
    if not ssh_conn:
        return None
    runtime = SshNativeRuntime(
        ssh_conn.get("settings") or {},
        ssh_conn.get("id") or connection_id,
        0,
        None,
        None,
        connector_secret=None,
    )
    argv = [a for a in runtime._base_ssh_argv() if a != "-T"] + ["-o", "BatchMode=yes", runtime._target()]
    profile_target = f"~/.hermes/profiles/{profile_name}/state.db" if profile_name and profile_name != "default" else "~/.hermes/state.db"
    script = f"""
import json, os, sqlite3
target = os.path.expanduser({repr(profile_target)})
fallback = os.path.expanduser('~/.hermes/state.db')
db = target if os.path.isfile(target) else fallback
res = {{}}
if os.path.isfile(db):
    try:
        conn = sqlite3.connect(f'file:{{db}}?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        row = conn.execute(
            'SELECT model, model_config, input_tokens, output_tokens, cache_read_tokens, reasoning_tokens FROM sessions WHERE id = ?',
            ({repr(session_id)},)
        ).fetchone()
        conn.close()
        if row:
            res = dict(row)
    except Exception:
        pass
print(json.dumps(res))
"""
    cmd = build_remote_python_command(script)
    try:
        proc = run_subprocess_hidden([*argv, cmd], capture_output=True, text=True, timeout=6)
        if proc.returncode == 0 and proc.stdout.strip():
            data = json.loads(proc.stdout.strip())
            if data and (data.get("model") or data.get("input_tokens") is not None):
                return _parse_hermes_row(data, profile_name=profile_name, store=store)
    except Exception as exc:
        logger.debug("Failed remote Hermes usage over SSH for session %s: %s", session_id, exc)
    return None


def _parse_codex_rollout_file(filepath: str | Path, store: Any | None = None) -> dict[str, Any] | None:
    last_u = None
    cw = None
    last_in = 0
    m_name = "default"
    try:
        with open(filepath, "r", encoding="utf-8", errors="ignore") as f:
            for line in f:
                if "total_token_usage" in line:
                    d = json.loads(line)
                    info = d.get("payload", {}).get("info", {})
                    if "total_token_usage" in info:
                        last_u = info["total_token_usage"]
                        if "model_context_window" in info:
                            cw = info.get("model_context_window")
                        last_in = info.get("last_token_usage", {}).get("input_tokens", last_in)
                if "turn_context" in line and "model" in line:
                    m_name = json.loads(line).get("payload", {}).get("model", m_name)
    except (OSError, json.JSONDecodeError, TypeError, ValueError):
        last_u = None

    if not last_u:
        return None

    if cw is None and m_name and m_name != "default":
        cw = resolve_model_context_window(m_name, agent_kind="codex", store=store)

    in_tok = last_u.get("input_tokens", 0)
    cached = last_u.get("cached_input_tokens", 0)
    used_pct = round(((last_in or in_tok) / cw) * 100, 2) if (cw and cw > 0) else None
    return {
        "model": m_name,
        "context_window": cw,
        "last_input_tokens": last_in or in_tok,
        "used_percentage": used_pct,
        "total_tokens": last_u.get("total_tokens", 0),
        "input_tokens": in_tok,
        "output_tokens": last_u.get("output_tokens", 0),
        "cached_tokens": cached,
        "reasoning_tokens": last_u.get("reasoning_output_tokens", 0),
        "cache_hit_rate": round((cached / max(in_tok, 1)) * 100, 1) if in_tok > 0 else 0.0,
    }


def _read_local_codex_usage(session_id: str, store: Any | None = None) -> dict[str, Any] | None:
    pattern = os.path.expanduser(f"~/.codex/sessions/**/rollout-*{session_id}*.jsonl")
    matches = glob.glob(pattern, recursive=True)
    if not matches:
        return None
    return _parse_codex_rollout_file(matches[0], store=store)


def _read_remote_codex_usage(session_id: str, store: Any, connection_id: str) -> dict[str, Any] | None:
    ssh_conn = store.get_ssh_connection(connection_id)
    if not ssh_conn:
        return None
    runtime = SshNativeRuntime(
        ssh_conn.get("settings") or {},
        ssh_conn.get("id") or connection_id,
        0,
        None,
        None,
        connector_secret=None,
    )
    argv = [a for a in runtime._base_ssh_argv() if a != "-T"] + ["-o", "BatchMode=yes", runtime._target()]
    script = f"""
import glob, json, os
pattern = os.path.expanduser('~/.codex/sessions/**/rollout-*{session_id}*.jsonl')
matches = glob.glob(pattern, recursive=True)
res = {{}}
if matches:
    with open(matches[0], 'r', encoding='utf-8', errors='ignore') as f:
        for line in f:
            if 'total_token_usage' in line:
                d = json.loads(line)
                info = d.get('payload', {{}}).get('info', {{}})
                if 'total_token_usage' in info:
                    res['last_u'] = info['total_token_usage']
                    res['cw'] = info.get('model_context_window', 1000000)
                    res['last_in'] = info.get('last_token_usage', {{}}).get('input_tokens', 0)
            if 'turn_context' in line and 'model' in line:
                res['model'] = json.loads(line).get('payload', {{}}).get('model', 'default')
print(json.dumps(res))
"""
    cmd = build_remote_python_command(script)
    try:
        proc = run_subprocess_hidden([*argv, cmd], capture_output=True, text=True, timeout=6)
        if proc.returncode == 0 and proc.stdout.strip():
            data = json.loads(proc.stdout.strip())
            if data and data.get("last_u"):
                last_u = data["last_u"]
                cw = data.get("cw")
                last_in = data.get("last_in") or 0
                m_name = data.get("model") or "default"
                if not cw and m_name and m_name != "default":
                    cw = resolve_model_context_window(m_name, agent_kind="codex", store=store)
                in_tok = last_u.get("input_tokens", 0)
                cached = last_u.get("cached_input_tokens", 0)
                used_pct = round(((last_in or in_tok) / cw) * 100, 2) if (cw and cw > 0) else None
                return {
                    "model": m_name,
                    "context_window": cw,
                    "last_input_tokens": last_in or in_tok,
                    "used_percentage": used_pct,
                    "total_tokens": last_u.get("total_tokens", 0),
                    "input_tokens": in_tok,
                    "output_tokens": last_u.get("output_tokens", 0),
                    "cached_tokens": cached,
                    "reasoning_tokens": last_u.get("reasoning_output_tokens", 0),
                    "cache_hit_rate": round((cached / max(in_tok, 1)) * 100, 1) if in_tok > 0 else 0.0,
                }
    except Exception as exc:
        logger.debug("Failed remote Codex usage over SSH for session %s: %s", session_id, exc)
    return None


def _parse_grok_session(session_dir: Path, summary: dict[str, Any], store: Any | None = None) -> dict[str, Any]:
    model = summary.get("current_model_id") or "grok-4.6"
    cw = resolve_model_context_window(model, agent_kind="grok", store=store)

    updates_path = session_dir / "updates.jsonl"
    usage_path = session_dir / "usage.json"
    last_in = 0
    in_tok = 0
    out_tok = 0
    cached = 0
    reasoning = 0

    if usage_path.is_file():
        try:
            ud = json.loads(usage_path.read_text(encoding="utf-8"))
            sess_u = ud.get("session") or {}
            in_tok = int(sess_u.get("inputTokens") or 0)
            out_tok = int(sess_u.get("outputTokens") or 0)
            cached = int(sess_u.get("cachedReadTokens") or 0)
            reasoning = int(sess_u.get("reasoningTokens") or 0)
            turns = ud.get("turns") or []
            if turns:
                latest_turn = turns[-1]
                last_in = int(latest_turn.get("inputTokens") or in_tok)
        except Exception as exc:
            logger.debug("Failed reading Grok usage.json: %s", exc)

    if updates_path.is_file():
        try:
            with open(updates_path, "r", encoding="utf-8", errors="replace") as f:
                for line in f:
                    if "tokens" in line or "usage" in line:
                        try:
                            d = json.loads(line)
                            update = (d.get("params") or {}).get("update") if isinstance(d, dict) else None
                            if isinstance(update, dict):
                                if "context_window_tokens" in update:
                                    last_in = int(update.get("context_window_tokens") or last_in)
                                if "tokens_used" in update and isinstance(update["tokens_used"], dict):
                                    u = update["tokens_used"]
                                    in_tok = max(in_tok, int(u.get("input_tokens") or 0))
                                    out_tok = max(out_tok, int(u.get("output_tokens") or 0))
                                    cached = max(cached, int(u.get("cached_tokens") or u.get("cached_input_tokens") or 0))
                                    reasoning = max(reasoning, int(u.get("reasoning_tokens") or 0))
                        except Exception:
                            continue
        except Exception as exc:
            logger.debug("Failed scanning Grok updates.jsonl: %s", exc)

    # When Grok uses custom third-party endpoints or zero usage recorded, estimate from session artifacts
    if in_tok == 0 and last_in == 0:
        try:
            chat_path = session_dir / "chat_history.jsonl"
            if chat_path.is_file():
                with open(chat_path, "r", encoding="utf-8", errors="replace") as f:
                    for line in f:
                        row = json.loads(line)
                        role = row.get("role") or row.get("type")
                        content = row.get("content")
                        text = content if isinstance(content, str) else " ".join(str(c.get("text", "")) for c in content if isinstance(c, dict)) if isinstance(content, list) else ""
                        t = max(1, len(text) // 4)
                        if role == "assistant":
                            out_tok += t
                        else:
                            in_tok += t
            sp_path = session_dir / "system_prompt.txt"
            if sp_path.is_file():
                in_tok += max(1, len(sp_path.read_text(encoding="utf-8", errors="replace")) // 4)
            tools_path = session_dir / "tool_definitions.json"
            if tools_path.is_file():
                in_tok += max(1, len(tools_path.read_text(encoding="utf-8", errors="replace")) // 4)
            if updates_path.is_file():
                with open(updates_path, "r", encoding="utf-8", errors="replace") as f:
                    for line in f:
                        r = json.loads(line)
                        update = (r.get("params") or {}).get("update") or {}
                        if update.get("sessionUpdate") in ("agent_message_chunk", "agent_thought_chunk"):
                            txt = (update.get("content") or {}).get("text") or ""
                            out_tok += max(1, len(txt) // 4) if txt else 0
            last_in = in_tok
        except Exception as exc:
            logger.debug("Failed estimating Grok session tokens: %s", exc)

    total = in_tok + cached + out_tok
    cache_hit_rate = round((cached / max(in_tok + cached, 1)) * 100, 1) if (in_tok + cached) > 0 else 0.0

    used_pct = round(((last_in or in_tok) / cw) * 100, 2) if (cw and cw > 0) else None

    return {
        "model": model,
        "context_window": cw,
        "last_input_tokens": last_in or in_tok,
        "used_percentage": used_pct,
        "total_tokens": total,
        "input_tokens": in_tok,
        "output_tokens": out_tok,
        "cached_tokens": cached,
        "reasoning_tokens": reasoning,
        "cache_hit_rate": cache_hit_rate,
    }


def _read_local_grok_usage(session_id: str, store: Any | None = None) -> dict[str, Any] | None:
    root = Path.home() / ".grok" / "sessions"
    if not root.is_dir():
        return None
    for path in root.rglob(f"{session_id}/summary.json"):
        if not path.is_file():
            continue
        try:
            summary = json.loads(path.read_text(encoding="utf-8"))
            return _parse_grok_session(path.parent, summary, store=store)
        except Exception as exc:
            logger.debug("Failed reading Grok session at %s: %s", path, exc)
    return None


def _read_remote_grok_usage(session_id: str, store: Any, connection_id: str) -> dict[str, Any] | None:
    ssh_conn = store.get_ssh_connection(connection_id)
    if not ssh_conn:
        return None
    runtime = SshNativeRuntime(
        ssh_conn.get("settings") or {},
        ssh_conn.get("id") or connection_id,
        0,
        None,
        None,
        connector_secret=None,
    )
    argv = [a for a in runtime._base_ssh_argv() if a != "-T"] + ["-o", "BatchMode=yes", runtime._target()]
    script = f"""
import glob, json, os
from pathlib import Path
root = Path.home() / '.grok' / 'sessions'
res = {{}}
if root.is_dir():
    for p in root.rglob('{session_id}/summary.json'):
        if p.is_file():
            try:
                summary = json.loads(p.read_text(encoding='utf-8'))
                res['summary'] = summary
                session_dir = p.parent
                usage_path = session_dir / 'usage.json'
                if usage_path.is_file():
                    try:
                        res['usage'] = json.loads(usage_path.read_text(encoding='utf-8'))
                    except Exception:
                        pass
                updates_path = session_dir / 'updates.jsonl'
                if updates_path.is_file():
                    with open(updates_path, 'r', encoding='utf-8', errors='replace') as f:
                        for line in f:
                            if 'tokens' in line or 'usage' in line:
                                try:
                                    d = json.loads(line)
                                    up = (d.get('params') or {{}}).get('update')
                                    if isinstance(up, dict):
                                        if 'context_window_tokens' in up:
                                            res['last_in'] = up.get('context_window_tokens')
                                        if 'tokens_used' in up and isinstance(up['tokens_used'], dict):
                                            res['tokens_used'] = up['tokens_used']
                                except Exception:
                                    pass
                # Artifact estimate if zero
                chat_path = session_dir / 'chat_history.jsonl'
                est_in, est_out = 0, 0
                if chat_path.is_file():
                    with open(chat_path, 'r', encoding='utf-8', errors='replace') as f:
                        for line in f:
                            try:
                                row = json.loads(line)
                                role = row.get('role') or row.get('type')
                                content = row.get('content')
                                text = content if isinstance(content, str) else ' '.join(str(c.get('text', '')) for c in content if isinstance(c, dict)) if isinstance(content, list) else ''
                                t = max(1, len(text) // 4)
                                if role == 'assistant':
                                    est_out += t
                                else:
                                    est_in += t
                            except Exception:
                                pass
                sp_path = session_dir / 'system_prompt.txt'
                if sp_path.is_file():
                    est_in += max(1, len(sp_path.read_text(encoding='utf-8', errors='replace')) // 4)
                tools_path = session_dir / 'tool_definitions.json'
                if tools_path.is_file():
                    est_in += max(1, len(tools_path.read_text(encoding='utf-8', errors='replace')) // 4)
                if updates_path.is_file():
                    with open(updates_path, 'r', encoding='utf-8', errors='replace') as f:
                        for line in f:
                            try:
                                r = json.loads(line)
                                update = (r.get('params') or {{}}).get('update') or {{}}
                                if update.get('sessionUpdate') in ('agent_message_chunk', 'agent_thought_chunk'):
                                    txt = (update.get('content') or {{}}).get('text') or ''
                                    est_out += max(1, len(txt) // 4) if txt else 0
                            except Exception:
                                pass
                res['estimate'] = {{'in_tok': est_in, 'out_tok': est_out}}
            except Exception:
                pass
            break
print(json.dumps(res))
"""
    cmd = build_remote_python_command(script)
    try:
        proc = run_subprocess_hidden([*argv, cmd], capture_output=True, text=True, timeout=6)
        if proc.returncode == 0 and proc.stdout.strip():
            data = json.loads(proc.stdout.strip())
            summary = data.get("summary")
            if summary:
                model = summary.get("current_model_id") or "grok-4.6"
                cw = resolve_model_context_window(model, agent_kind="grok", store=store)
                tokens_used = data.get("tokens_used") or {}
                usage_info = data.get("usage", {}).get("session", {})
                in_tok = int(usage_info.get("inputTokens") or tokens_used.get("input_tokens") or 0)
                out_tok = int(usage_info.get("outputTokens") or tokens_used.get("output_tokens") or 0)
                cached = int(usage_info.get("cachedReadTokens") or tokens_used.get("cached_tokens") or tokens_used.get("cached_input_tokens") or 0)
                reasoning = int(usage_info.get("reasoningTokens") or tokens_used.get("reasoning_tokens") or 0)
                last_in = int(data.get("last_in") or in_tok)

                # Fallback to estimate if zero
                if in_tok == 0 and last_in == 0:
                    est = data.get("estimate") or {}
                    in_tok = int(est.get("in_tok") or 0)
                    out_tok = int(est.get("out_tok") or 0)
                    last_in = in_tok

                total = in_tok + cached + out_tok
                cache_hit_rate = round((cached / max(in_tok + cached, 1)) * 100, 1) if (in_tok + cached) > 0 else 0.0
                used_pct = round(((last_in or in_tok) / cw) * 100, 2) if (cw and cw > 0) else None
                return {
                    "model": model,
                    "context_window": cw,
                    "last_input_tokens": last_in or in_tok,
                    "used_percentage": used_pct,
                    "total_tokens": total,
                    "input_tokens": in_tok,
                    "output_tokens": out_tok,
                    "cached_tokens": cached,
                    "reasoning_tokens": reasoning,
                    "cache_hit_rate": cache_hit_rate,
                }
    except Exception as exc:
        logger.debug("Failed remote Grok usage over SSH for session %s: %s", session_id, exc)
    return None


def resolve_session_usage(
    session_id: str,
    store: Any,
    agent_id: str | None = None,
) -> dict[str, Any] | None:
    """Resolve token usage and context window for a given session across all providers."""
    now = time.monotonic()
    if session_id in _usage_cache:
        exp, cached_res = _usage_cache[session_id]
        if now < exp:
            return dict(cached_res)

    sess = store.find_session_by_id(session_id)
    target_agent_id = agent_id or (sess.get("agent_id") if sess else None)
    agent = store.get_agent(target_agent_id) if target_agent_id else None
    agent_kind = agent.get("kind") if agent else None
    connection_id = (sess.get("connection_id") if sess else None) or (agent.get("connection_id") if agent else None)
    profile_name = agent.get("profile_name") if agent else "default"

    # Infer kind if not found directly
    if not agent_kind and target_agent_id:
        if "hermes" in target_agent_id:
            agent_kind = "hermes"
        elif "grok" in target_agent_id:
            agent_kind = "grok"
        elif "codex" in target_agent_id:
            agent_kind = "codex"

    result: dict[str, Any] | None = None

    if agent_kind == "hermes":
        if not connection_id:
            result = _read_local_hermes_usage(session_id, profile_name, store=store)
        else:
            result = _read_remote_hermes_usage(session_id, store, connection_id, profile_name)
        if not result and connection_id:
            result = _read_local_hermes_usage(session_id, profile_name, store=store)

    elif agent_kind == "grok":
        if not connection_id:
            result = _read_local_grok_usage(session_id, store=store)
        else:
            result = _read_remote_grok_usage(session_id, store, connection_id)
        if not result and connection_id:
            result = _read_local_grok_usage(session_id, store=store)

    elif agent_kind == "codex":
        if not connection_id:
            result = _read_local_codex_usage(session_id, store=store)
        else:
            result = _read_remote_codex_usage(session_id, store, connection_id)
        if not result and connection_id:
            result = _read_local_codex_usage(session_id, store=store)

    # Universal fallbacks if not matched or returned empty
    if not result:
        result = _read_local_hermes_usage(session_id, profile_name, store=store)
    if not result:
        result = _read_local_codex_usage(session_id, store=store)
    if not result:
        result = _read_local_grok_usage(session_id, store=store)

    if result:
        _usage_cache[session_id] = (now + _USAGE_CACHE_TTL, result)
        return dict(result)

    return None
