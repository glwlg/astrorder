from __future__ import annotations

import json
import logging
import os
import shutil
import tomllib
from typing import Any
import yaml
import tomlkit

from .ov_sync import list_targets, resolve_target, _run_cmd_in_target
from ..connections import run_subprocess_hidden

logger = logging.getLogger("astrorder.capabilities")


def _redact_env(env_dict: dict[str, Any]) -> dict[str, str]:
    res = {}
    for k, v in env_dict.items():
        k_str = str(k)
        v_str = str(v)
        lower_k = k_str.lower()
        if any(secret_kw in lower_k for secret_kw in ("secret", "key", "token", "password", "auth", "bearer")):
            res[k_str] = "[REDACTED]"
        else:
            res[k_str] = v_str
    return res


# ==========================================
# 1. 本机配置读取 (Local Readers)
# ==========================================

def _read_local_codex_config() -> dict[str, Any]:
    codex_path = os.path.expanduser("~/.codex/config.toml")
    if not os.path.isfile(codex_path):
        return {}
    try:
        with open(codex_path, "rb") as f:
            return tomllib.load(f)
    except Exception as exc:
        logger.error(f"Failed to load local codex config.toml: {exc}")
        return {}


def _read_local_hermes_config() -> dict[str, Any]:
    paths = [
        os.path.expanduser("~/AppData/Local/hermes/config.yaml"),
        os.path.expanduser("~/.hermes/config.yaml"),
    ]
    for p in paths:
        if os.path.isfile(p):
            try:
                with open(p, "r", encoding="utf-8") as f:
                    return yaml.safe_load(f) or {}
            except Exception as exc:
                logger.error(f"Failed to load local hermes config.yaml from {p}: {exc}")
    return {}


def _read_local_grok_config() -> dict[str, Any]:
    grok_path = os.path.expanduser("~/.grok/config.toml")
    if not os.path.isfile(grok_path):
        return {}
    try:
        with open(grok_path, "r", encoding="utf-8-sig") as f:
            return tomllib.loads(f.read())
    except Exception as exc:
        logger.error(f"Failed to load local grok config.toml: {exc}")
        return {}


# ==========================================
# 2. 远程配置读取 (Remote Target Readers)
# ==========================================

def _read_target_all_configs(target: dict[str, Any]) -> dict[str, dict[str, Any]]:
    """在远程目标（WSL / SSH）中一次性读取 Codex、Hermes 和 Grok 的配置文件"""
    remote_script = """
import os, json, tomllib, yaml

out = {'codex': {}, 'hermes': {}, 'grok': {}}

p_codex = os.path.expanduser('~/.codex/config.toml')
if os.path.isfile(p_codex):
    try:
        out['codex'] = tomllib.load(open(p_codex, 'rb'))
    except Exception:
        pass

p_hermes = os.path.expanduser('~/.hermes/config.yaml')
if os.path.isfile(p_hermes):
    try:
        out['hermes'] = yaml.safe_load(open(p_hermes, 'r', encoding='utf-8')) or {}
    except Exception:
        pass

p_grok = os.path.expanduser('~/.grok/config.toml')
if os.path.isfile(p_grok):
    try:
        with open(p_grok, 'r', encoding='utf-8-sig') as f:
            out['grok'] = tomllib.loads(f.read())
    except Exception:
        pass

print('###ASTRORDER_CAPS_JSON###' + json.dumps(out))
"""
    cmd = f"python3 -c \"{remote_script}\""
    code, output = _run_cmd_in_target(target, cmd, timeout=15)
    if code == 0 and "###ASTRORDER_CAPS_JSON###" in output:
        try:
            raw = output.split("###ASTRORDER_CAPS_JSON###")[1].strip()
            return json.loads(raw)
        except Exception as exc:
            logger.error(f"Failed to decode remote configs json: {exc}")
    return {"codex": {}, "hermes": {}, "grok": {}}


# ==========================================
# 3. 聚合能力总览 (Capabilities Aggregation)
# ==========================================

def get_target_capabilities(store: Any, target_id: str, agent_filter: str | None = None) -> dict[str, Any]:
    from .skills_sync import list_target_skills

    target = resolve_target(store, target_id)
    kind = target.get("kind")

    # 1. 获取技能列表 (已标记 agent: codex/hermes/grok)
    all_skills = list_target_skills(store, target_id)

    # 2. 读取配置
    if kind == "local":
        codex_cfg = _read_local_codex_config()
        hermes_cfg = _read_local_hermes_config()
        grok_cfg = _read_local_grok_config()
    else:
        configs = _read_target_all_configs(target)
        codex_cfg = configs.get("codex", {})
        hermes_cfg = configs.get("hermes", {})
        grok_cfg = configs.get("grok", {})

    mcp_servers: list[dict[str, Any]] = []
    plugins: list[dict[str, Any]] = []
    marketplaces: list[dict[str, Any]] = []

    # -----------------
    # A. Codex 生态提取
    # -----------------
    for name, cfg in codex_cfg.get("mcp_servers", {}).items():
        is_enabled = cfg.get("enabled", True)
        url = cfg.get("url")
        command = cfg.get("command")
        transport = "sse/http" if url else "stdio"
        env_dict = cfg.get("env", {})
        
        sample_tools = []
        if name == "astrorder":
            sample_tools = ["sessions_list", "sessions_send", "blackboard_get", "blackboard_set"]
        elif name == "stitch":
            sample_tools = ["generate_screen_from_text", "list_screens", "get_screen"]
        elif name == "blender":
            sample_tools = ["render_scene", "execute_python"]
        elif name == "devspace":
            sample_tools = ["workspace_exec", "terminal_open"]
        elif name == "node_repl":
            sample_tools = ["eval_javascript", "node_repl_run"]

        mcp_servers.append({
            "name": name,
            "agent": "codex",
            "enabled": is_enabled,
            "transport": transport,
            "url": url,
            "command": command,
            "args": cfg.get("args", []),
            "headers": _redact_env(cfg.get("headers", {})),
            "env": _redact_env(env_dict),
            "tools": sample_tools,
            "source": "codex",
        })

    for name, cfg in codex_cfg.get("plugins", {}).items():
        pkg_name = name
        market_name = ""
        if "@" in name:
            pkg_name, market_name = name.split("@", 1)
        plugins.append({
            "id": f"codex::{name}",
            "name": pkg_name,
            "full_name": name,
            "agent": "codex",
            "marketplace": market_name,
            "enabled": cfg.get("enabled", True) if isinstance(cfg, dict) else True,
            "source": "codex-plugin",
            "config": cfg if isinstance(cfg, dict) else {},
        })

    for name, cfg in codex_cfg.get("marketplaces", {}).items():
        marketplaces.append({
            "id": f"codex::{name}",
            "name": name,
            "agent": "codex",
            "source_type": cfg.get("source_type", "git"),
            "source": cfg.get("source", ""),
            "last_updated": cfg.get("last_updated"),
            "ref": cfg.get("ref"),
            "description": f"Codex 已订阅的 {cfg.get('source_type', 'git')} 市场源",
        })

    # ------------------
    # B. Hermes 生态提取
    # ------------------
    for name, cfg in hermes_cfg.get("mcp_servers", {}).items():
        url = cfg.get("url")
        command = cfg.get("command")
        transport = "sse/http" if url else "stdio"
        mcp_servers.append({
            "name": name,
            "agent": "hermes",
            "enabled": True,
            "transport": transport,
            "url": url,
            "command": command,
            "args": cfg.get("args", []),
            "headers": _redact_env(cfg.get("headers", {})),
            "env": _redact_env(cfg.get("env", {})),
            "tools": ["blackboard_set", "blackboard_get", "sessions_tree"] if name == "astrorder" else [],
            "source": "hermes",
        })

    h_plugins = hermes_cfg.get("plugins", {})
    if isinstance(h_plugins, dict):
        h_enabled = set(h_plugins.get("enabled", []))
        h_disabled = set(h_plugins.get("disabled", []))
        h_entries = h_plugins.get("entries", {})
        all_h_names = h_enabled | h_disabled | set(h_entries.keys())
        for name in all_h_names:
            plugins.append({
                "id": f"hermes::{name}",
                "name": name,
                "full_name": name,
                "agent": "hermes",
                "marketplace": "hermes-official",
                "enabled": (name in h_enabled) or (name not in h_disabled),
                "source": "hermes-plugin",
                "config": h_entries.get(name, {}),
            })

    # ----------------
    # C. Grok 生态提取
    # ----------------
    for name, cfg in grok_cfg.get("mcp_servers", {}).items():
        url = cfg.get("url")
        command = cfg.get("command")
        transport = "sse/http" if url else "stdio"
        mcp_servers.append({
            "name": name,
            "agent": "grok",
            "enabled": True,
            "transport": transport,
            "url": url,
            "command": command,
            "args": cfg.get("args", []),
            "headers": _redact_env(cfg.get("headers", {})),
            "env": _redact_env(cfg.get("env", {})),
            "tools": ["local_grok_agent"] if name == "astrorder" else [],
            "source": "grok",
        })

    g_plugins = grok_cfg.get("plugins", {})
    if isinstance(g_plugins, dict):
        for name in g_plugins.get("enabled", []):
            plugins.append({
                "id": f"grok::{name}",
                "name": name,
                "full_name": name,
                "agent": "grok",
                "marketplace": "xai-official",
                "enabled": True,
                "source": "grok-plugin",
                "config": {},
            })

    g_market = grok_cfg.get("marketplace", {})
    if isinstance(g_market, dict):
        for src in g_market.get("sources", []):
            src_name = src.get("name", "xAI Marketplace")
            marketplaces.append({
                "id": f"grok::{src_name}",
                "name": src_name,
                "agent": "grok",
                "source_type": "git",
                "source": src.get("git", ""),
                "description": "xAI Grok 官方订阅市场",
            })

    marketplaces.insert(0, {
        "id": "global::skills.sh",
        "name": "Vercel Skills 官方生态 (skills.sh)",
        "agent": "all",
        "source_type": "registry",
        "source": "https://skills.sh",
        "description": "全球最大的开源 Agent 技能市场，支持一键分发到各 Agent",
    })

    if agent_filter and agent_filter != "all":
        af_lower = agent_filter.lower()
        filtered_skills = [
            s for s in all_skills
            if (
                s.get("agent") in (af_lower, "all")
                or any(af_lower in str(a).lower() for a in s.get("agents", []))
                or (af_lower == "codex" and "codex" in str(s.get("path", "")).lower())
                or (af_lower == "hermes" and "hermes" in str(s.get("path", "")).lower())
            )
        ]
        filtered_mcp = [m for m in mcp_servers if m.get("agent") in (agent_filter, "all")]
        filtered_plugins = [p for p in plugins if p.get("agent") in (agent_filter, "all")]
        filtered_markets = [m for m in marketplaces if m.get("agent") in (agent_filter, "all")]
    else:
        filtered_skills = all_skills
        filtered_mcp = mcp_servers
        filtered_plugins = plugins
        filtered_markets = marketplaces

    return {
        "target_id": target_id,
        "target_name": target.get("name"),
        "kind": kind,
        "counts": {
            "skills": len(filtered_skills),
            "mcp": len(filtered_mcp),
            "plugins": len(filtered_plugins),
            "marketplaces": len(filtered_markets),
        },
        "skills": filtered_skills,
        "mcp_servers": filtered_mcp,
        "plugins": filtered_plugins,
        "marketplaces": filtered_markets,
    }


# ==========================================
# 4. 配置写入与 CRUD 管理 (Config Writers)
# ==========================================

def update_mcp_server(
    store: Any,
    target_id: str,
    agent: str,
    action: str,
    server_data: dict[str, Any],
) -> dict[str, Any]:
    server_name = server_data.get("name")
    if not server_name:
        raise ValueError("Server name is required")

    target = resolve_target(store, target_id)
    kind = target.get("kind")

    if kind == "local":
        return _update_local_mcp_server(agent, action, server_name, server_data)
    else:
        return _update_remote_mcp_server(target, agent, action, server_name, server_data)


def _update_local_mcp_server(agent: str, action: str, server_name: str, data: dict[str, Any]) -> dict[str, Any]:
    if agent == "codex":
        p = os.path.expanduser("~/.codex/config.toml")
        doc = tomlkit.parse(open(p, "r", encoding="utf-8").read()) if os.path.isfile(p) else tomlkit.document()
        if "mcp_servers" not in doc:
            doc["mcp_servers"] = tomlkit.table()
        
        if action == "delete":
            if server_name in doc["mcp_servers"]:
                del doc["mcp_servers"][server_name]
        else:
            tbl = tomlkit.table()
            tbl["enabled"] = data.get("enabled", True)
            if data.get("url"):
                tbl["url"] = data["url"]
            if data.get("command"):
                tbl["command"] = data["command"]
            if data.get("args"):
                tbl["args"] = data["args"]
            if data.get("env"):
                orig_env = doc.get("mcp_servers", {}).get(server_name, {}).get("env", {})
                new_env = dict(orig_env)
                for k, v in data["env"].items():
                    if v != "[REDACTED]":
                        new_env[k] = v
                tbl["env"] = new_env
            if data.get("headers"):
                orig_h = doc.get("mcp_servers", {}).get(server_name, {}).get("headers", {})
                new_h = dict(orig_h)
                for k, v in data["headers"].items():
                    if v != "[REDACTED]":
                        new_h[k] = v
                tbl["headers"] = new_h
            doc["mcp_servers"][server_name] = tbl

        with open(p, "w", encoding="utf-8") as f:
            f.write(tomlkit.dumps(doc))
        return {"ok": True, "action": action, "agent": "codex", "server": server_name}

    elif agent == "hermes":
        paths = [os.path.expanduser("~/AppData/Local/hermes/config.yaml"), os.path.expanduser("~/.hermes/config.yaml")]
        p = next((x for x in paths if os.path.isfile(x)), paths[0])
        h_data = yaml.safe_load(open(p, "r", encoding="utf-8")) if os.path.isfile(p) else {}
        if "mcp_servers" not in h_data or not isinstance(h_data["mcp_servers"], dict):
            h_data["mcp_servers"] = {}

        if action == "delete":
            h_data["mcp_servers"].pop(server_name, None)
        else:
            item = {}
            if data.get("url"):
                item["url"] = data["url"]
            if data.get("command"):
                item["command"] = data["command"]
            if data.get("args"):
                item["args"] = data["args"]
            if data.get("headers"):
                orig_h = h_data["mcp_servers"].get(server_name, {}).get("headers", {})
                new_h = dict(orig_h)
                for k, v in data["headers"].items():
                    if v != "[REDACTED]":
                        new_h[k] = v
                item["headers"] = new_h
            if data.get("env"):
                orig_e = h_data["mcp_servers"].get(server_name, {}).get("env", {})
                new_e = dict(orig_e)
                for k, v in data["env"].items():
                    if v != "[REDACTED]":
                        new_e[k] = v
                item["env"] = new_e
            h_data["mcp_servers"][server_name] = item

        with open(p, "w", encoding="utf-8") as f:
            yaml.dump(h_data, f, allow_unicode=True)
        return {"ok": True, "action": action, "agent": "hermes", "server": server_name}

    elif agent == "grok":
        p = os.path.expanduser("~/.grok/config.toml")
        content = ""
        if os.path.isfile(p):
            with open(p, "r", encoding="utf-8-sig") as f:
                content = f.read()
        doc = tomlkit.parse(content) if content else tomlkit.document()
        if "mcp_servers" not in doc:
            doc["mcp_servers"] = tomlkit.table()

        if action == "delete":
            if server_name in doc["mcp_servers"]:
                del doc["mcp_servers"][server_name]
        else:
            tbl = tomlkit.table()
            if data.get("url"):
                tbl["url"] = data["url"]
            if data.get("command"):
                tbl["command"] = data["command"]
            if data.get("args"):
                tbl["args"] = data["args"]
            if data.get("headers"):
                orig_h = doc.get("mcp_servers", {}).get(server_name, {}).get("headers", {})
                new_h = dict(orig_h)
                for k, v in data["headers"].items():
                    if v != "[REDACTED]":
                        new_h[k] = v
                tbl["headers"] = new_h
            doc["mcp_servers"][server_name] = tbl

        with open(p, "w", encoding="utf-8") as f:
            f.write(tomlkit.dumps(doc))
        return {"ok": True, "action": action, "agent": "grok", "server": server_name}

    raise ValueError(f"Unsupported agent: {agent}")


def _update_remote_mcp_server(target: dict[str, Any], agent: str, action: str, server_name: str, data: dict[str, Any]) -> dict[str, Any]:
    payload = json.dumps({"agent": agent, "action": action, "name": server_name, "data": data})
    script = f"""
import json, os, yaml, tomllib

req = json.loads({repr(payload)})
agent = req['agent']
act = req['action']
name = req['name']
data = req['data']

if agent == 'codex':
    p = os.path.expanduser('~/.codex/config.toml')
    try:
        import tomlkit
        doc = tomlkit.parse(open(p, 'r', encoding='utf-8').read()) if os.path.isfile(p) else tomlkit.document()
    except Exception:
        doc = {{}}
    if 'mcp_servers' not in doc:
        doc['mcp_servers'] = {{}}
    if act == 'delete':
        doc['mcp_servers'].pop(name, None)
    else:
        doc['mcp_servers'][name] = data
    with open(p, 'w', encoding='utf-8') as f:
        f.write(tomlkit.dumps(doc) if hasattr(tomlkit, 'dumps') else str(doc))
    print('###REMOTE_MCP_OK###')

elif agent == 'hermes':
    p = os.path.expanduser('~/.hermes/config.yaml')
    h_data = yaml.safe_load(open(p, 'r', encoding='utf-8')) if os.path.isfile(p) else {{}}
    if 'mcp_servers' not in h_data or not isinstance(h_data['mcp_servers'], dict):
        h_data['mcp_servers'] = {{}}
    if act == 'delete':
        h_data['mcp_servers'].pop(name, None)
    else:
        h_data['mcp_servers'][name] = data
    with open(p, 'w', encoding='utf-8') as f:
        yaml.dump(h_data, f, allow_unicode=True)
    print('###REMOTE_MCP_OK###')
"""
    code, out = _run_cmd_in_target(target, f"python3 -c \"{script}\"", timeout=15)
    if code == 0 and "###REMOTE_MCP_OK###" in out:
        return {"ok": True, "action": action, "agent": agent, "server": server_name}
    raise RuntimeError(f"Remote MCP update failed (code {code}): {out}")


# ==========================================
# 5. 插件完整生命周期管理 (Plugins Lifecycle)
# ==========================================

def update_plugin_state(
    store: Any,
    target_id: str,
    agent: str,
    action: str,
    plugin_name: str,
    enabled: bool = True,
) -> dict[str, Any]:
    target = resolve_target(store, target_id)
    kind = target.get("kind")

    if kind == "local":
        if agent == "codex":
            p = os.path.expanduser("~/.codex/config.toml")
            if not os.path.isfile(p):
                return {"ok": False, "error": "Codex config not found"}
            doc = tomlkit.parse(open(p, "r", encoding="utf-8").read())
            if "plugins" not in doc:
                doc["plugins"] = tomlkit.table()
            
            if action == "uninstall":
                if plugin_name in doc["plugins"]:
                    del doc["plugins"][plugin_name]
            else:
                if plugin_name in doc["plugins"]:
                    if isinstance(doc["plugins"][plugin_name], dict):
                        doc["plugins"][plugin_name]["enabled"] = enabled
                    else:
                        doc["plugins"][plugin_name] = {"enabled": enabled}
                else:
                    doc["plugins"][plugin_name] = {"enabled": enabled}

            with open(p, "w", encoding="utf-8") as f:
                f.write(tomlkit.dumps(doc))
            return {"ok": True, "action": action, "agent": "codex", "plugin": plugin_name}

        elif agent == "hermes":
            paths = [os.path.expanduser("~/AppData/Local/hermes/config.yaml"), os.path.expanduser("~/.hermes/config.yaml")]
            p = next((x for x in paths if os.path.isfile(x)), paths[0])
            h_data = yaml.safe_load(open(p, "r", encoding="utf-8")) if os.path.isfile(p) else {}
            if "plugins" not in h_data or not isinstance(h_data["plugins"], dict):
                h_data["plugins"] = {"enabled": [], "disabled": []}
            
            enabled_list = list(h_data["plugins"].get("enabled", []))
            disabled_list = list(h_data["plugins"].get("disabled", []))

            if action == "uninstall":
                if plugin_name in enabled_list:
                    enabled_list.remove(plugin_name)
                if plugin_name in disabled_list:
                    disabled_list.remove(plugin_name)
                h_data["plugins"].get("entries", {}).pop(plugin_name, None)
            else:
                if enabled:
                    if plugin_name not in enabled_list:
                        enabled_list.append(plugin_name)
                    if plugin_name in disabled_list:
                        disabled_list.remove(plugin_name)
                else:
                    if plugin_name in enabled_list:
                        enabled_list.remove(plugin_name)
                    if plugin_name not in disabled_list:
                        disabled_list.append(plugin_name)

            h_data["plugins"]["enabled"] = enabled_list
            h_data["plugins"]["disabled"] = disabled_list
            with open(p, "w", encoding="utf-8") as f:
                yaml.dump(h_data, f, allow_unicode=True)
            return {"ok": True, "action": action, "agent": "hermes", "plugin": plugin_name}

        elif agent == "grok":
            p = os.path.expanduser("~/.grok/config.toml")
            content = ""
            if os.path.isfile(p):
                with open(p, "r", encoding="utf-8-sig") as f:
                    content = f.read()
            doc = tomlkit.parse(content) if content else tomlkit.document()
            if "plugins" not in doc:
                doc["plugins"] = tomlkit.table()
            enabled_arr = list(doc["plugins"].get("enabled", []))

            if action == "uninstall":
                if plugin_name in enabled_arr:
                    enabled_arr.remove(plugin_name)
            else:
                if enabled and plugin_name not in enabled_arr:
                    enabled_arr.append(plugin_name)
                elif not enabled and plugin_name in enabled_arr:
                    enabled_arr.remove(plugin_name)

            doc["plugins"]["enabled"] = enabled_arr
            with open(p, "w", encoding="utf-8") as f:
                f.write(tomlkit.dumps(doc))
            return {"ok": True, "action": action, "agent": "grok", "plugin": plugin_name}

    return {"ok": True, "action": action, "agent": agent, "plugin": plugin_name}


# ==========================================
# 6. 市场源完整生命周期管理 (Marketplaces Lifecycle)
# ==========================================

def update_marketplace(
    store: Any,
    target_id: str,
    agent: str,
    action: str,
    market_data: dict[str, Any],
) -> dict[str, Any]:
    market_name = market_data.get("name")
    if not market_name:
        raise ValueError("Marketplace name is required")

    target = resolve_target(store, target_id)
    kind = target.get("kind")

    if kind == "local":
        if agent == "codex":
            p = os.path.expanduser("~/.codex/config.toml")
            doc = tomlkit.parse(open(p, "r", encoding="utf-8").read()) if os.path.isfile(p) else tomlkit.document()
            if "marketplaces" not in doc:
                doc["marketplaces"] = tomlkit.table()

            if action == "delete":
                if market_name in doc["marketplaces"]:
                    del doc["marketplaces"][market_name]
            elif action == "pull":
                import datetime
                if market_name in doc["marketplaces"]:
                    doc["marketplaces"][market_name]["last_updated"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
            else:
                tbl = tomlkit.table()
                tbl["source_type"] = market_data.get("source_type", "git")
                tbl["source"] = market_data.get("source", "")
                if market_data.get("ref"):
                    tbl["ref"] = market_data["ref"]
                doc["marketplaces"][market_name] = tbl

            with open(p, "w", encoding="utf-8") as f:
                f.write(tomlkit.dumps(doc))
            return {"ok": True, "action": action, "agent": "codex", "marketplace": market_name}

        elif agent == "grok":
            p = os.path.expanduser("~/.grok/config.toml")
            content = ""
            if os.path.isfile(p):
                with open(p, "r", encoding="utf-8-sig") as f:
                    content = f.read()
            doc = tomlkit.parse(content) if content else tomlkit.document()
            if "marketplace" not in doc:
                doc["marketplace"] = tomlkit.table()
            sources = list(doc["marketplace"].get("sources", []))

            if action == "delete":
                sources = [s for s in sources if s.get("name") != market_name]
            else:
                found = False
                for s in sources:
                    if s.get("name") == market_name:
                        s["git"] = market_data.get("source", "")
                        found = True
                        break
                if not found:
                    sources.append({"name": market_name, "git": market_data.get("source", "")})

            doc["marketplace"]["sources"] = sources
            with open(p, "w", encoding="utf-8") as f:
                f.write(tomlkit.dumps(doc))
            return {"ok": True, "action": action, "agent": "grok", "marketplace": market_name}

    return {"ok": True, "action": action, "agent": agent, "marketplace": market_name}


# ==========================================
# 7. MCP 实时探活与握手 Ping (MCP Probe)
# ==========================================

async def ping_mcp_server(target_id: str, server_data: dict[str, Any]) -> dict[str, Any]:
    import time
    import httpx

    transport = server_data.get("transport", "stdio")
    url = server_data.get("url")
    start = time.time()

    if transport == "sse/http" and url:
        try:
            headers = {}
            raw_headers = server_data.get("headers", {})
            for k, v in raw_headers.items():
                if v != "[REDACTED]":
                    headers[k] = v
            async with httpx.AsyncClient(timeout=4.0) as client:
                resp = await client.get(url, headers=headers)
                latency_ms = int((time.time() - start) * 1000)
                return {
                    "ok": resp.status_code in (200, 404, 405),
                    "status_code": resp.status_code,
                    "latency_ms": latency_ms,
                    "message": f"HTTP {resp.status_code} ({latency_ms}ms)",
                }
        except Exception as exc:
            return {"ok": False, "latency_ms": -1, "message": f"Connection failed: {str(exc)}"}
    else:
        cmd = server_data.get("command")
        if not cmd:
            return {"ok": False, "latency_ms": -1, "message": "Command is missing"}
        executable = cmd[0] if isinstance(cmd, list) else cmd.split()[0]
        found = shutil.which(executable) is not None
        latency_ms = int((time.time() - start) * 1000)
        return {
            "ok": found,
            "latency_ms": latency_ms,
            "message": f"Executable {'found on PATH' if found else 'NOT found'}",
        }

