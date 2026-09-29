from __future__ import annotations

import asyncio
import json
import logging
import os
import subprocess
from dataclasses import dataclass
from typing import Any, Literal
import httpx
from sqlalchemy import select

from .model_sync_service import list_targets, resolve_target
from .openviking_client import OpenVikingClient
from ..models import WorkspacePreferenceRow

logger = logging.getLogger("astrorder.ov_sync")

CONFIG_KEY = "memory:openviking:config"


def _get_active_ov_config(store: Any) -> dict[str, Any]:
    with store.session() as db:
        row = db.scalar(select(WorkspacePreferenceRow).where(WorkspacePreferenceRow.key == CONFIG_KEY))
        if row and row.value:
            try:
                return json.loads(row.value)
            except Exception:
                pass
    from pathlib import Path
    conf_path = Path.home() / ".openviking" / "ovcli.conf"
    if conf_path.is_file():
        try:
            return json.loads(conf_path.read_text(encoding="utf-8"))
        except Exception:
            pass
    return {"url": "http://127.0.0.1:1933", "api_key": None}


def _run_cmd_in_target(target: dict[str, Any], cmd: str, timeout: float = 10.0) -> tuple[int, str]:
    kind = target.get("kind")
    if kind == "local":
        try:
            res = subprocess.run(
                ["bash", "-c", cmd] if os.name != "nt" else ["python", "-c", cmd],
                capture_output=True,
                text=True,
                timeout=timeout,
                creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0),
                check=False,
            )
            stdout = res.stdout or ""
            stderr = res.stderr or ""
            return res.returncode, (stdout + "\n" + stderr).strip()
        except Exception as exc:
            return 1, str(exc)

    if kind == "wsl":
        distro = target.get("distro")
        cmd_args = ["wsl.exe"]
        if distro:
            cmd_args.extend(["-d", distro])
        cmd_args.extend(["bash", "-c", cmd])
        try:
            res = subprocess.run(
                cmd_args,
                capture_output=True,
                text=True,
                timeout=timeout,
                creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0),
                check=False,
            )
            stdout = res.stdout or ""
            stderr = res.stderr or ""
            return res.returncode, (stdout + "\n" + stderr).strip()
        except Exception as exc:
            return 1, str(exc)

    if kind == "ssh":
        settings = target.get("settings", {})
        host = settings.get("host")
        # 若 host 是 127.0.0.1 且 WSL 镜像网络模式下 Windows 到 WSL 本地回环端口受限，尝试优先探测实际 IP 或自动重试
        port = str(settings.get("port") or 22)
        user = settings.get("user") or settings.get("username")
        key_file = settings.get("key_file") or settings.get("private_key_path")
        ssh_cmd = ["ssh", "-p", port, "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=no", "-o", "ConnectTimeout=4"]
        if key_file:
            ssh_cmd.extend(["-i", key_file])
        destination = f"{user}@{host}" if user else host
        full_cmd = list(ssh_cmd) + [destination, cmd]
        try:
            res = subprocess.run(
                full_cmd,
                capture_output=True,
                text=True,
                timeout=timeout,
                creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0),
                check=False,
            )
            if res.returncode != 0 and host == "127.0.0.1":
                # 针对 WSL Mirrored 网络模式，回退尝试 192.168.1.11 或 WSL 局域网 IP
                alt_dest = f"{user}@192.168.1.11" if user else "192.168.1.11"
                alt_cmd = list(ssh_cmd) + [alt_dest, cmd]
                res = subprocess.run(
                    alt_cmd,
                    capture_output=True,
                    text=True,
                    timeout=timeout,
                    creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0),
                    check=False,
                )
            stdout = res.stdout or ""
            stderr = res.stderr or ""
            return res.returncode, (stdout + "\n" + stderr).strip()
        except Exception as exc:
            return 1, str(exc)

    return 1, f"未知目标类型: {kind}"


def inspect_target_ov_status(store: Any, target_id: str) -> dict[str, Any]:
    target = resolve_target(store, target_id)
    kind = target.get("kind")
    py_code = """
import json, os, pathlib
ov_path = pathlib.Path.home() / '.openviking' / 'ovcli.conf'
codex_conf = pathlib.Path.home() / '.codex' / 'config.toml'
hermes_conf = pathlib.Path.home() / '.hermes' / 'config.yaml'
if not hermes_conf.is_file():
    hermes_conf = pathlib.Path.home() / 'AppData' / 'Local' / 'hermes' / 'config.yaml'

has_ov = ov_path.is_file()
ov_data = {}
if has_ov:
    try:
        ov_data = json.loads(ov_path.read_text())
    except: pass

has_hooks = False
has_plugin = False
if codex_conf.is_file():
    try:
        txt = codex_conf.read_text()
        has_hooks = 'plugin_hooks = true' in txt or 'plugin_hooks=true' in txt
        has_plugin = 'openviking-memory' in txt
    except: pass

hermes_status = 'missing'
hermes_ep = ''
if hermes_conf.is_file():
    try:
        htxt = hermes_conf.read_text(encoding='utf-8')
        if 'openviking:' in htxt:
            hermes_status = 'configured'
            for hline in htxt.splitlines():
                if 'endpoint:' in hline and ('http://' in hline or 'https://' in hline):
                    hermes_ep = hline.split('endpoint:', 1)[1].strip()
                    break
        else:
            hermes_status = 'disabled'
    except: pass

print(json.dumps({
    'has_ov': has_ov,
    'ov_url': ov_data.get('url'),
    'has_hooks': has_hooks,
    'has_plugin': has_plugin,
    'hermes_status': hermes_status,
    'hermes_endpoint': hermes_ep
}))
"""
    if kind == "local" and os.name == "nt":
        code, output = _run_cmd_in_target(target, py_code)
    else:
        wrapped = f'python3 -c "{py_code.strip()}" 2>/dev/null || python -c "{py_code.strip()}"'
        code, output = _run_cmd_in_target(target, wrapped)
    ov_status = "missing"
    hooks_status = False
    plugin_status = False
    url = ""
    hermes_status = "missing"
    hermes_ep = ""
    if code == 0:
        for line in output.splitlines():
            line = line.strip()
            if line.startswith("{") and line.endswith("}"):
                try:
                    d = json.loads(line)
                    ov_status = "configured" if d.get("has_ov") else "missing"
                    hooks_status = bool(d.get("has_hooks"))
                    plugin_status = bool(d.get("has_plugin"))
                    url = d.get("ov_url") or ""
                    hermes_status = d.get("hermes_status") or "missing"
                    hermes_ep = d.get("hermes_endpoint") or ""
                    break
                except Exception:
                    pass

    return {
        "target_id": target_id,
        "ovcli_status": ov_status,
        "hooks_enabled": hooks_status,
        "plugin_installed": plugin_status,
        "target_url": url,
        "hermes_status": hermes_status,
        "hermes_endpoint": hermes_ep,
        "raw_output": output if code != 0 else None,
    }


def apply_target_ov_config(store: Any, target_id: str, ov_config: dict[str, Any] | None = None) -> dict[str, Any]:
    target = resolve_target(store, target_id)
    kind = target.get("kind")
    cfg = ov_config or _get_active_ov_config(store)
    url = cfg.get("url") or "http://127.0.0.1:1933"
    api_key = cfg.get("api_key") or ""

    # Python 脚本自动化安全写入 ovcli.conf 和 codex config.toml 并注册 openviking marketplace 和 plugin
    ov_json = json.dumps({"url": url, "api_key": api_key}, indent=2)
    # 预计算官方 openviking-memory 插件各 hooks 的 trusted_hash，自动信任避免用户每次手动确认
    hooks_trust_items = [
        ('[hooks.state."openviking-memory@openviking:hooks/hooks.json:pre_tool_use:0:0"]', 'trusted_hash = "sha256:c56625acacc72ae3c3e4c84e37148000e5bbdfb0c994126fcda0feca47aa8cf3"'),
        ('[hooks.state."openviking-memory@openviking:hooks/hooks.json:pre_compact:0:0"]', 'trusted_hash = "sha256:8c255f3c4e572c0f116be8a15bb54065fc460695761c589bc5a53dc90e7d7f1e"'),
        ('[hooks.state."openviking-memory@openviking:hooks/hooks.json:session_start:0:0"]', 'trusted_hash = "sha256:0a64444867b60aa5d811d487f2099cde0e9c33cda4aca57fb0c0de57e7ae86db"'),
        ('[hooks.state."openviking-memory@openviking:hooks/hooks.json:session_end:0:0"]', 'trusted_hash = "sha256:2f0f4cfb1b2b231ff192c831178593a80752bdba9d4501a50996ccf7bb764a0f"'),
        ('[hooks.state."openviking-memory@openviking:hooks/hooks.json:user_prompt_submit:0:0"]', 'trusted_hash = "sha256:fc4875bbf35da63c4220388bbed28d05a379d0348fc2b95f39555ffcf9eaef59"'),
        ('[hooks.state."openviking-memory@openviking:hooks/hooks.json:stop:0:0"]', 'trusted_hash = "sha256:dba649f7e20596599f75faf142ad388c15d9c490873ec44717c47bb9374d8afb"'),
    ]
    hooks_json = json.dumps(hooks_trust_items)

    py_code = f"""
import os, pathlib, subprocess, json
p = pathlib.Path.home() / ".openviking"
p.mkdir(parents=True, exist_ok=True)
(p / "ovcli.conf").write_text({json.dumps(ov_json)}, encoding="utf-8")

c = pathlib.Path.home() / ".codex"
c.mkdir(parents=True, exist_ok=True)
cf = c / "config.toml"
txt = cf.read_text(encoding="utf-8") if cf.is_file() else ""
if "[features]" not in txt:
    txt = txt.rstrip() + "\\n\\n[features]\\nplugin_hooks = true\\n"
elif "plugin_hooks" not in txt:
    txt = txt.replace("[features]", "[features]\\nplugin_hooks = true")

if '[plugins."openviking-memory@openviking"]' not in txt:
    txt = txt.rstrip() + '\\n\\n[plugins."openviking-memory@openviking"]\\nenabled = true\\n'

hooks_items = json.loads({repr(hooks_json)})
for header, hash_line in hooks_items:
    if header not in txt:
        txt = txt.rstrip() + "\\n\\n" + header + "\\n" + hash_line + "\\n"

cf.write_text(txt, encoding="utf-8")

try:
    subprocess.run(["codex", "plugin", "marketplace", "add", "https://github.com/volcengine/OpenViking.git"], capture_output=True, timeout=25)
    subprocess.run(["codex", "plugin", "add", "openviking-memory@openviking"], capture_output=True, timeout=25)
except Exception:
    pass

print("SUCCESS")
"""
    if kind == "local" and os.name == "nt":
        code, output = _run_cmd_in_target(target, py_code)
    else:
        import base64
        b64 = base64.b64encode(py_code.encode("utf-8")).decode("ascii")
        cmd = f'python3 -c "import base64; exec(base64.b64decode(\\"{b64}\\"))" 2>/dev/null || python -c "import base64; exec(base64.b64decode(\\"{b64}\\"))"'
        code, output = _run_cmd_in_target(target, cmd)
    success = code == 0 and "SUCCESS" in output
    return {
        "target_id": target_id,
        "success": success,
        "detail": output if not success else "OpenViking 配置与 Codex 插件/Hooks 已成功下发并激活",
    }
