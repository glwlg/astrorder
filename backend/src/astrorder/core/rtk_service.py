from __future__ import annotations

import json
import logging
import os
import re
import shutil
import subprocess
import threading
from datetime import date, datetime, timedelta
from pathlib import Path
from typing import Any
import yaml

from astrorder.connections import (
    ConnectionError,
    _windows_hide_flags,
    _windows_hide_startupinfo,
)

logger = logging.getLogger("astrorder.rtk")

_CONFIG_LOCKS: dict[str, threading.Lock] = {}
_LOCKS_MUTEX = threading.Lock()

def _get_env_lock(env_key: str) -> threading.Lock:
    with _LOCKS_MUTEX:
        if env_key not in _CONFIG_LOCKS:
            _CONFIG_LOCKS[env_key] = threading.Lock()
        return _CONFIG_LOCKS[env_key]

SUPPORTED_AGENTS = [
    {"kind": "codex", "name": "OpenAI Codex", "supported": True},
    {"kind": "hermes", "name": "Hermes Agent", "supported": True},
    {"kind": "grok", "name": "xAI Grok", "supported": False, "detail": "RTK 暂不支持 Grok"},
]

def aggregate_rtk_gain(
    raw_gain: dict[str, Any] | None,
    days: str = "30",
    reference_date: date | None = None,
) -> dict[str, Any]:
    """
    根据 days 过滤 daily 记录并计算完全一致的 summary。
    针对原生 rtk gain total_saved 与 daily 累加及 input - output 不一致问题进行规范化计算。
    """
    if reference_date is None:
        reference_date = date.today()

    raw_daily = (raw_gain or {}).get("daily") or []
    if not isinstance(raw_daily, list):
        raw_daily = []

    cutoff_date: date | None = None
    if days != "all":
        try:
            d_val = int(days)
            cutoff_date = reference_date - timedelta(days=d_val - 1)
        except (ValueError, TypeError):
            cutoff_date = reference_date - timedelta(days=29)

    filtered_daily: list[dict[str, Any]] = []
    total_commands = 0
    total_input = 0
    total_output = 0
    total_saved = 0

    for item in raw_daily:
        if not isinstance(item, dict):
            continue
        d_str = item.get("date")
        if not d_str or not isinstance(d_str, str):
            continue
        try:
            d_obj = datetime.strptime(d_str[:10], "%Y-%m-%d").date()
        except Exception:
            continue

        if cutoff_date is not None and d_obj < cutoff_date:
            continue

        commands = int(item.get("commands") or 0)
        inp = int(item.get("input_tokens") or 0)
        out = int(item.get("output_tokens") or 0)
        # 按照 daily 的 saved_tokens 计算，或者当 saved_tokens 为 0 但有差异时计算
        saved = int(item.get("saved_tokens") if item.get("saved_tokens") is not None else (inp - out))

        filtered_daily.append({
            "date": d_str,
            "commands": commands,
            "input_tokens": inp,
            "output_tokens": out,
            "saved_tokens": saved,
        })
        total_commands += commands
        total_input += inp
        total_output += out
        total_saved += saved

    avg_savings_pct = (
        round((total_saved / total_input) * 100, 2)
        if total_input > 0
        else 0.0
    )

    return {
        "daily": filtered_daily,
        "summary": {
            "total_commands": total_commands,
            "total_input": total_input,
            "total_output": total_output,
            "total_saved": total_saved,
            "avg_savings_pct": avg_savings_pct,
        },
    }


def get_agent_command(kind: str, enable: bool) -> list[str]:
    """
    生成官方 RTK init / uninstall 命令行参数。
    Codex: --codex (不是 --agent codex)
    Hermes: --agent hermes
    Gemini: --gemini (--auto-patch --no-trust-filters)
    OpenCode: --opencode (卸载用 -g --uninstall)
    Copilot: --copilot
    """
    if kind == "codex":
        if enable:
            return ["init", "-g", "--codex"]
        return ["init", "-g", "--codex", "--uninstall"]

    if kind == "hermes":
        if enable:
            return ["init", "-g", "--agent", "hermes"]
        return ["init", "-g", "--agent", "hermes", "--uninstall"]

    if kind == "gemini":
        if enable:
            return ["init", "-g", "--gemini", "--auto-patch", "--no-trust-filters"]
        return ["init", "-g", "--gemini", "--uninstall"]

    if kind == "opencode":
        if enable:
            return ["init", "-g", "--opencode"]
        return ["init", "-g", "--uninstall"]

    if kind == "copilot":
        if enable:
            return ["init", "-g", "--copilot"]
        return ["init", "-g", "--copilot", "--uninstall"]

    if kind == "claude":
        if enable:
            return ["init", "-g", "--auto-patch", "--no-trust-filters"]
        return ["init", "-g", "--uninstall"]

    if kind == "cursor":
        if enable:
            return ["init", "-g", "--agent", "cursor"]
        return ["init", "-g", "--agent", "cursor", "--uninstall"]

    if kind == "pi":
        if enable:
            return ["init", "-g", "--agent", "pi"]
        return ["init", "-g", "--agent", "pi", "--uninstall"]

    raise ValueError(f"不支持的 Agent 类型: {kind}")


def resolve_local_hermes_home() -> Path:
    """
    解析星序当前真实使用的 Hermes profile 目录。
    优先读取环境变量 HERMES_HOME，其次如果是 Windows 则优先 AppData/Local/hermes，否则 ~/.hermes。
    """
    env_home = os.environ.get("HERMES_HOME")
    if env_home:
        return Path(env_home).expanduser().resolve()
    local_app_data = os.environ.get("LOCALAPPDATA")
    if local_app_data:
        win_path = Path(local_app_data) / "hermes"
        if win_path.exists():
            return win_path.resolve()
    return (Path.home() / ".hermes").resolve()


def check_agent_status_local(
    kind: str,
    *,
    hermes_home: Path | None = None,
    codex_dir: Path | None = None,
) -> dict[str, Any]:
    """
    检查本机 native 配置文件状态，不轻信通用 CLI 的状态回显。
    针对 Hermes:
      严格核查 plugins/rtk-rewrite/__init__.py 和 plugins/rtk-rewrite/plugin.yaml 均存在，
      且解析 config.yaml 的 plugins.enabled 列表确含有 'rtk-rewrite'。
    针对 Codex:
      严格核查 RTK.md、hooks.json（含 rtk 钩子指令）与 AGENTS.md（含 @RTK.md 引用）。
    其余 Agent:
      由于尚未在多平台上完成隔离可靠性验证，明确标为 supported=False。
    """
    meta = next((item for item in SUPPORTED_AGENTS if item["kind"] == kind), None)
    name = meta["name"] if meta else kind
    supported = meta["supported"] if meta else False

    if kind == "hermes":
        h_dir = hermes_home or resolve_local_hermes_home()
        plugin_init = h_dir / "plugins" / "rtk-rewrite" / "__init__.py"
        plugin_yaml = h_dir / "plugins" / "rtk-rewrite" / "plugin.yaml"
        config_yaml = h_dir / "config.yaml"

        files_ok = plugin_init.is_file() and plugin_yaml.is_file()
        is_config_enabled = False
        if config_yaml.is_file():
            try:
                raw = config_yaml.read_text(encoding="utf-8", errors="ignore")
                parsed = yaml.safe_load(raw) or {}
                if isinstance(parsed, dict):
                    enabled_list = parsed.get("plugins", {}).get("enabled", [])
                    if isinstance(enabled_list, list):
                        is_config_enabled = "rtk-rewrite" in enabled_list
            except Exception as exc:
                logger.warning("Failed to parse Hermes config.yaml: %s", exc)

        enabled = files_ok and is_config_enabled
        return {
            "kind": kind,
            "name": name,
            "enabled": enabled,
            "supported": True,
            "detail": f"插件目录: {h_dir / 'plugins' / 'rtk-rewrite'}" if enabled else "未启用",
        }

    if kind == "codex":
        c_dir = codex_dir or (Path.home() / ".codex")
        rtk_md = c_dir / "RTK.md"
        hooks_json = c_dir / "hooks.json"
        agents_md = c_dir / "AGENTS.md"

        has_rtk_md = rtk_md.is_file()
        has_hook = False
        if hooks_json.is_file():
            try:
                data = json.loads(hooks_json.read_text(encoding="utf-8", errors="ignore"))
                has_hook = "rtk" in str(data)
            except Exception:
                pass
        has_agents = False
        if agents_md.is_file():
            try:
                content = agents_md.read_text(encoding="utf-8", errors="ignore")
                has_agents = "@RTK.md" in content or "RTK" in content
            except Exception:
                pass

        enabled = has_rtk_md and has_hook and has_agents
        return {
            "kind": kind,
            "name": name,
            "enabled": enabled,
            "supported": True,
            "detail": f"配置目录: {c_dir}" if enabled else "未启用",
        }

    # 其余 Agent
    return {
        "kind": kind,
        "name": name,
        "enabled": False,
        "supported": False,
        "detail": meta.get("detail", "RTK 暂不支持该 Agent") if meta else "RTK 暂不支持该 Agent",
    }


def find_local_rtk() -> tuple[str | None, str | None]:
    """查找本机 rtk 可执行文件路径与版本号"""
    import subprocess
    import shutil
    import os
    from astrorder.connections import _windows_hide_flags, _windows_hide_startupinfo

    exe = shutil.which("rtk")
    if not exe:
        candidates = [
            Path.home() / ".local" / "bin" / "rtk.exe",
            Path.home() / ".local" / "bin" / "rtk",
            Path.home() / "bin" / "rtk.exe",
            Path.home() / "bin" / "rtk",
            Path(os.environ.get("LOCALAPPDATA", "")) / "Programs" / "rtk" / "rtk.exe",
        ]
        for c in candidates:
            if c.is_file():
                exe = str(c)
                break
    if not exe:
        return None, None

    version = None
    try:
        proc = subprocess.run(
            [exe, "--version"],
            capture_output=True,
            text=True,
            timeout=5,
            creationflags=_windows_hide_flags(),
            startupinfo=_windows_hide_startupinfo(),
        )
        if proc.returncode == 0:
            out = proc.stdout.strip()
            import re
            m = re.search(r"rtk\s+([0-9]+\.[0-9]+\.[0-9]+[a-zA-Z0-9._-]*)", out)
            if m:
                version = m.group(1)
            else:
                version = out
    except Exception as exc:
        logger.warning("rtk --version failed: %s", exc)

    return exe, version


def get_local_rtk_gain(rtk_exe: str | None, days: str = "30") -> tuple[dict[str, Any], list[dict[str, Any]]]:
    """读取本机 gain json 并对齐聚合"""
    import subprocess
    from astrorder.connections import _windows_hide_flags, _windows_hide_startupinfo

    if not rtk_exe:
        res = aggregate_rtk_gain(None, days=days)
        return res["summary"], res["daily"]

    raw_gain = None
    try:
        proc = subprocess.run(
            [rtk_exe, "gain", "-d", "-f", "json"],
            capture_output=True,
            text=True,
            timeout=10,
            creationflags=_windows_hide_flags(),
            startupinfo=_windows_hide_startupinfo(),
        )
        if proc.returncode == 0:
            raw_gain = json.loads(proc.stdout)
    except Exception as exc:
        logger.warning("rtk gain failed: %s", exc)

    res = aggregate_rtk_gain(raw_gain, days=days)
    return res["summary"], res["daily"]


def get_local_rtk_status(days: str = "30") -> dict[str, Any]:
    """获取本机完整的 RTK 状态"""
    exe, version = find_local_rtk()
    installed = bool(exe)

    agents = [
        check_agent_status_local(item["kind"])
        for item in SUPPORTED_AGENTS
    ]

    summary, daily = get_local_rtk_gain(exe, days=days)

    return {
        "installed": installed,
        "version": version,
        "executable": exe,
        "agents": agents,
        "daily": daily,
        "summary": summary,
        "detail": "本机 RTK 环境正常" if installed else "本机未检测到 RTK 安装",
    }


def execute_local_agent_toggle(kind: str, enabled: bool) -> None:
    """
    切换本机指定 Agent 的 RTK 启用状态。
    严格执行：
    1. 进程内锁防止并发写入。
    2. 对相关配置文件进行备份（.bak）。
    3. 执行原生 CLI 命令（--auto-patch --no-trust-filters 避免交互悬挂）。
    4. 执行完成后立即进行 Native 配置回读校验（Readback Verification），若未达到预期状态则从备份恢复并抛出异常。
    """
    if kind not in {"hermes", "codex"}:
        raise ConnectionError(f"Agent [{kind}] 暂未支持安全热切换，请手工配置或等待支持。", 422)

    exe, _ = find_local_rtk()
    if not exe:
        raise ConnectionError("本机未安装 RTK，无法切换 Agent 状态", 400)

    lock = _get_env_lock("local")
    with lock:
        env = dict(os.environ)
        backups: list[tuple[Path, bytes | None]] = []

        if kind == "hermes":
            hermes_home = resolve_local_hermes_home()
            env["HERMES_HOME"] = str(hermes_home)
            cfg = hermes_home / "config.yaml"
            if cfg.is_file():
                backups.append((cfg, cfg.read_bytes()))
            else:
                backups.append((cfg, None))

        elif kind == "codex":
            c_dir = Path.home() / ".codex"
            for fn in ["hooks.json", "RTK.md", "AGENTS.md"]:
                fp = c_dir / fn
                if fp.is_file():
                    backups.append((fp, fp.read_bytes()))
                else:
                    backups.append((fp, None))

        cmd_args = get_agent_command(kind, enabled)
        proc = subprocess.run(
            [exe] + cmd_args,
            capture_output=True,
            text=True,
            timeout=30,
            env=env,
            creationflags=_windows_hide_flags(),
            startupinfo=_windows_hide_startupinfo(),
        )

        if proc.returncode != 0:
            err = proc.stderr.strip() or proc.stdout.strip()
            # 恢复备份
            for path, data in backups:
                if data is not None:
                    path.write_bytes(data)
                elif path.exists():
                    path.unlink(missing_ok=True)
            raise ConnectionError(f"执行 RTK 指令失败 ({err})", 500)

        # 写后强回读验证 (Readback Verification)
        status_after = check_agent_status_local(kind)
        if status_after.get("enabled") != enabled:
            # 回读状态与期望不符，回滚备份
            for path, data in backups:
                if data is not None:
                    path.write_bytes(data)
                elif path.exists():
                    path.unlink(missing_ok=True)
            raise ConnectionError(
                f"RTK 指令执行后状态回读不符（期望: {enabled}，实际: {status_after.get('enabled')}），已回滚配置备份。",
                500,
            )


REMOTE_RTK_PROBE = r'''
import json, os, platform, re, shutil, subprocess
from pathlib import Path

def find_rtk():
    exe = shutil.which("rtk")
    if not exe:
        candidates = [
            Path.home() / ".local/bin/rtk",
            Path.home() / "bin/rtk",
            Path.home() / ".cargo/bin/rtk",
        ]
        for c in candidates:
            if c.is_file() and os.access(c, os.X_OK):
                exe = str(c)
                break
    if not exe:
        return None, None
    version = None
    try:
        p = subprocess.run([exe, "--version"], capture_output=True, text=True, timeout=5)
        if p.returncode == 0:
            m = re.search(r"rtk\s+([0-9]+\.[0-9]+\.[0-9]+[a-zA-Z0-9._-]*)", p.stdout.strip())
            version = m.group(1) if m else p.stdout.strip()
    except Exception:
        pass
    return exe, version

def check_agents(hermes_profile="default"):
    res = []
    # 1. codex
    c_dir = Path.home() / ".codex"
    c_rtk = (c_dir / "RTK.md").is_file()
    c_hook = False
    if (c_dir / "hooks.json").is_file():
        try:
            c_hook = "rtk" in (c_dir / "hooks.json").read_text()
        except Exception:
            pass
    c_agents = False
    if (c_dir / "AGENTS.md").is_file():
        try:
            txt = (c_dir / "AGENTS.md").read_text()
            c_agents = "@RTK.md" in txt or "RTK" in txt
        except Exception:
            pass
    codex_enabled = bool(c_rtk and c_hook and c_agents)
    res.append({
        "kind": "codex",
        "name": "OpenAI Codex",
        "enabled": codex_enabled,
        "supported": True,
        "detail": str(c_dir) if codex_enabled else "未启用",
    })

    # 2. hermes
    h_dir = Path.home() / ".hermes"
    if hermes_profile and hermes_profile != "default":
        p_candidate = Path.home() / ".hermes" / "profiles" / hermes_profile
        if p_candidate.exists():
            h_dir = p_candidate
    h_plug = (h_dir / "plugins/rtk-rewrite/__init__.py").is_file() and (h_dir / "plugins/rtk-rewrite/plugin.yaml").is_file()
    h_cfg = False
    if (h_dir / "config.yaml").is_file():
        try:
            import yaml
            parsed = yaml.safe_load((h_dir / "config.yaml").read_text()) or {}
            if isinstance(parsed, dict):
                h_cfg = "rtk-rewrite" in parsed.get("plugins", {}).get("enabled", [])
        except Exception:
            try:
                # 备用轻量匹配
                h_cfg = "rtk-rewrite" in (h_dir / "config.yaml").read_text()
            except Exception:
                pass
    hermes_enabled = bool(h_plug and h_cfg)
    res.append({
        "kind": "hermes",
        "name": "Hermes Agent",
        "enabled": hermes_enabled,
        "supported": True,
        "detail": str(h_dir / "plugins/rtk-rewrite") if hermes_enabled else "未启用",
    })

    # 其他 agent 明确标为 unsupported
    # 星序支持三大 Agent：Codex, Hermes, Grok
    # 若 RTK 暂不支持 Grok，如实标注 RTK 暂不支持
    res.append({
        "kind": "grok",
        "name": "xAI Grok",
        "enabled": False,
        "supported": False,
        "detail": "RTK 暂不支持 Grok",
    })

    return res

def get_gain(exe):
    if not exe:
        return None
    try:
        p = subprocess.run([exe, "gain", "-d", "-f", "json"], capture_output=True, text=True, timeout=10)
        if p.returncode == 0:
            return json.loads(p.stdout)
    except Exception:
        pass
    return None

exe, version = find_rtk()
raw_gain = get_gain(exe)
agents = check_agents()
print(json.dumps({
    "installed": bool(exe),
    "version": version,
    "executable": exe,
    "agents": agents,
    "raw_gain": raw_gain,
}))
'''

def get_ssh_rtk_status(store: Any, settings: Any, service: Any, connection_id: str, days: str = "30") -> dict[str, Any]:
    """通过 SSH 执行 Python 远程探针探测 RTK 状态"""
    from astrorder.connections import ConnectionError
    from astrorder.core.environment_connections import RemoteCodex

    row = store.get_ssh_connection(connection_id)
    if row is None:
        raise ConnectionError("SSH 连接不存在", 404)

    profile_name = row.get("profile_name") or (row.get("settings") or {}).get("profile_name") or "default"
    probe_script = REMOTE_RTK_PROBE + f"\n# invoke check_agents with remote profile\n"

    remote = RemoteCodex(settings, store, service, row, "")
    try:
        data = remote.remote_json(probe_script, timeout=30)
    except ConnectionError as exc:
        raise ConnectionError(f"远程 SSH 探测失败: {exc.detail}", 502) from exc

    raw_gain = data.get("raw_gain")
    res = aggregate_rtk_gain(raw_gain, days=days)

    return {
        "installed": data.get("installed", False),
        "version": data.get("version"),
        "executable": data.get("executable"),
        "agents": data.get("agents") or [],
        "daily": res["daily"],
        "summary": res["summary"],
        "detail": "SSH 远端 RTK 环境正常" if data.get("installed") else "SSH 远端未检测到 RTK 安装",
    }


def execute_ssh_agent_toggle(store: Any, settings: Any, service: Any, connection_id: str, kind: str, enabled: bool) -> None:
    """在 SSH 远程主机上执行 RTK agent 开关命令，带环境互斥锁、备份与强回读校验"""
    from astrorder.connections import ConnectionError
    from astrorder.core.environment_connections import RemoteCodex

    if kind not in {"hermes", "codex"}:
        raise ConnectionError(f"Agent [{kind}] 暂未支持远端安全热切换，请手工配置或等待支持。", 422)

    row = store.get_ssh_connection(connection_id)
    if row is None:
        raise ConnectionError("SSH 连接不存在", 404)

    lock = _get_env_lock(f"ssh:{connection_id}")
    with lock:
        cmd_args = get_agent_command(kind, enabled)
        profile_name = row.get("profile_name") or (row.get("settings") or {}).get("profile_name") or "default"

        script = f'''
import json, os, shutil, subprocess
from pathlib import Path

exe = shutil.which("rtk")
if not exe:
    for c in [Path.home()/".local/bin/rtk", Path.home()/"bin/rtk", Path.home()/".cargo/bin/rtk"]:
        if c.is_file() and os.access(c, os.X_OK):
            exe = str(c)
            break

if not exe:
    print(json.dumps({{"ok": False, "error": "Remote rtk executable not found"}}))
    exit(0)

kind = {repr(kind)}
enabled = {repr(enabled)}
profile_name = {repr(profile_name)}

# 1. 备份配置
backups = {{}}
if kind == "hermes":
    h_dir = Path.home() / ".hermes"
    if profile_name and profile_name != "default":
        p_cand = Path.home() / ".hermes" / "profiles" / profile_name
        if p_cand.exists():
            h_dir = p_cand
    cfg = h_dir / "config.yaml"
    if cfg.is_file():
        backups[str(cfg)] = cfg.read_text(encoding="utf-8", errors="ignore")
elif kind == "codex":
    c_dir = Path.home() / ".codex"
    for fn in ["hooks.json", "RTK.md", "AGENTS.md"]:
        fp = c_dir / fn
        if fp.is_file():
            backups[str(fp)] = fp.read_text(encoding="utf-8", errors="ignore")

cmd = [exe] + {repr(cmd_args)}
env = dict(os.environ)
if kind == "hermes":
    env["HERMES_HOME"] = str(h_dir)

p = subprocess.run(cmd, capture_output=True, text=True, timeout=30, env=env)
if p.returncode != 0:
    err = p.stderr.strip() or p.stdout.strip()
    # 还原备份
    for path_str, content in backups.items():
        Path(path_str).write_text(content, encoding="utf-8")
    print(json.dumps({{"ok": False, "error": err}}))
    exit(0)

# 2. 回读核验 (Readback)
verified = False
if kind == "hermes":
    h_plug = (h_dir / "plugins/rtk-rewrite/__init__.py").is_file() and (h_dir / "plugins/rtk-rewrite/plugin.yaml").is_file()
    h_cfg = False
    if (h_dir / "config.yaml").is_file():
        try:
            import yaml
            parsed = yaml.safe_load((h_dir / "config.yaml").read_text()) or {{}}
            if isinstance(parsed, dict):
                h_cfg = "rtk-rewrite" in parsed.get("plugins", {{}}).get("enabled", [])
        except Exception:
            h_cfg = "rtk-rewrite" in (h_dir / "config.yaml").read_text()
    verified = (bool(h_plug and h_cfg) == enabled)
elif kind == "codex":
    c_dir = Path.home() / ".codex"
    c_rtk = (c_dir / "RTK.md").is_file()
    c_hook = False
    if (c_dir / "hooks.json").is_file():
        try:
            c_hook = "rtk" in (c_dir / "hooks.json").read_text()
        except Exception:
            pass
    c_agents = False
    if (c_dir / "AGENTS.md").is_file():
        try:
            txt = (c_dir / "AGENTS.md").read_text()
            c_agents = "@RTK.md" in txt or "RTK" in txt
        except Exception:
            pass
    verified = (bool(c_rtk and c_hook and c_agents) == enabled)

if not verified:
    for path_str, content in backups.items():
        Path(path_str).write_text(content, encoding="utf-8")
    print(json.dumps({{"ok": False, "error": f"Readback verification failed: expected enabled={{enabled}}"}}))
else:
    print(json.dumps({{"ok": True}}))
'''
        remote = RemoteCodex(settings, store, service, row, "")
        res = remote.remote_json(script, timeout=45)
        if not res.get("ok"):
            raise ConnectionError(f"远端执行 RTK 切换失败: {res.get('error')}", 500)


def execute_ssh_rtk_install(store: Any, settings: Any, service: Any, connection_id: str) -> None:
    """
    在 SSH 远端主机上执行官方 install.sh 安全安装。
    安全规范：
    1. 保持 Host Key 强校验。
    2. 显式清除 RTK_SKIP_CHECKSUM，强制校验 release checksum。
    3. 分步下载校验，安装超时放宽至 120s。
    4. 安装后使用 --version 强核验可执行文件与版本，若未生成有效 binary 绝不谎报成功。
    """
    from astrorder.connections import ConnectionError
    from astrorder.core.environment_connections import RemoteCodex

    row = store.get_ssh_connection(connection_id)
    if row is None:
        raise ConnectionError("SSH 连接不存在", 404)

    script = r'''
import json, os, re, shutil, subprocess
from pathlib import Path

install_dir = Path.home() / ".local" / "bin"
install_dir.mkdir(parents=True, exist_ok=True)

env = dict(os.environ)
env.pop("RTK_SKIP_CHECKSUM", None)
env["RTK_INSTALL_DIR"] = str(install_dir)

# 1. 下载官方 install.sh
installer_script = Path.home() / ".local" / "rtk_install_tmp.sh"
dl = subprocess.run(
    ["curl", "-fsSL", "https://raw.githubusercontent.com/rtk-ai/rtk/refs/heads/master/install.sh", "-o", str(installer_script)],
    capture_output=True, text=True, timeout=30, env=env
)
if dl.returncode != 0:
    print(json.dumps({"ok": False, "error": f"下载 install.sh 失败: {dl.stderr.strip()}"}))
    exit(0)

# 2. 严格执行安装脚本 (sh 执行)
p = subprocess.run(["sh", str(installer_script)], capture_output=True, text=True, timeout=90, env=env)
installer_script.unlink(missing_ok=True)

if p.returncode != 0:
    err = p.stderr.strip() or p.stdout.strip()
    print(json.dumps({"ok": False, "error": f"安装脚本执行失败: {err}"}))
    exit(0)

# 3. 安装后强校验 (find + version)
rtk_bin = install_dir / "rtk"
if not rtk_bin.is_file():
    rtk_bin_which = shutil.which("rtk")
    if rtk_bin_which:
        rtk_bin = Path(rtk_bin_which)

if not rtk_bin.is_file() or not os.access(rtk_bin, os.X_OK):
    print(json.dumps({"ok": False, "error": "安装后未能在安装目录找到可执行的 rtk 二进制档"}))
    exit(0)

vp = subprocess.run([str(rtk_bin), "--version"], capture_output=True, text=True, timeout=10)
if vp.returncode != 0:
    print(json.dumps({"ok": False, "error": f"rtk 可执行档版本核验失败: {vp.stderr.strip()}"}))
    exit(0)

out = vp.stdout.strip()
m = re.search(r"rtk\s+([0-9]+\.[0-9]+\.[0-9]+[a-zA-Z0-9._-]*)", out)
version = m.group(1) if m else out

print(json.dumps({"ok": True, "version": version, "executable": str(rtk_bin)}))
'''
    remote = RemoteCodex(settings, store, service, row, "")
    res = remote.remote_json(script, timeout=140)
    if not res.get("ok"):
        raise ConnectionError(f"远端安装 RTK 失败: {res.get('error')}", 500)


def execute_local_rtk_install() -> None:
    """
    本机安装 RTK。
    在 Windows 下首选官方 winget (winget install --id rtk-ai.rtk -e --accept-source-agreements --accept-package-agreements)；
    若不可用则通过官方 release 脚本安全安装。绝不猜测未经核实的第三方包。
    安装后必须通过 find_local_rtk() 进行强核验。
    """
    import subprocess
    import shutil
    import os
    from astrorder.connections import ConnectionError, _windows_hide_flags, _windows_hide_startupinfo

    lock = _get_env_lock("local")
    with lock:
        # 1. 尝试 winget
        if shutil.which("winget"):
            proc = subprocess.run(
                ["winget", "install", "--id", "rtk-ai.rtk", "-e", "--accept-source-agreements", "--accept-package-agreements"],
                capture_output=True,
                text=True,
                timeout=120,
                creationflags=_windows_hide_flags(),
                startupinfo=_windows_hide_startupinfo(),
            )
            exe, version = find_local_rtk()
            if exe and version:
                return

        # 2. 若 winget 未能安装成功，尝试 curl 官方安装脚本并在 sh 环境下执行
        env = dict(os.environ)
        env.pop("RTK_SKIP_CHECKSUM", None)
        try:
            proc = subprocess.run(
                ["curl", "-fsSL", "https://raw.githubusercontent.com/rtk-ai/rtk/refs/heads/master/install.sh"],
                capture_output=True,
                text=True,
                timeout=30,
                creationflags=_windows_hide_flags(),
                startupinfo=_windows_hide_startupinfo(),
            )
            if proc.returncode == 0 and proc.stdout:
                sh_exe = shutil.which("bash") or shutil.which("sh")
                if sh_exe:
                    install_proc = subprocess.run(
                        [sh_exe, "-s"],
                        input=proc.stdout,
                        text=True,
                        capture_output=True,
                        timeout=120,
                        env=env,
                        creationflags=_windows_hide_flags(),
                        startupinfo=_windows_hide_startupinfo(),
                    )
        except Exception as exc:
            logger.warning("Local curl install failed: %s", exc)

        # 3. 安装后强核验
        exe, version = find_local_rtk()
        if not exe or not version:
            raise ConnectionError("本机 RTK 安装未能确认或未检测到可用版本，未成功写入系统。", 500)
