from __future__ import annotations

import base64
import hashlib
import json
import os
import re
import subprocess
import tomllib
from collections.abc import Mapping
from pathlib import Path
from typing import Any

from .errors import DaemonProtocolError

FILES = {
    "codex_config": ".codex/config.toml",
    "codex_catalog": ".codex/opencodex-catalog.json",
    "codex_magpie_catalog": ".codex/magpie-catalog.json",
    "grok_config": ".grok/config.toml",
}
ENV_FILES = {
    "OPENCODEX_API_AUTH_TOKEN": ".config/environment.d/astrorder-opencodex.conf",
    "MAGPIE_API_KEY": ".config/environment.d/astrorder-magpie.conf",
}

_REMOTE = r'''
import hashlib,json,os,sys,tomllib
from pathlib import Path
FILES={"codex_config":".codex/config.toml","codex_catalog":".codex/opencodex-catalog.json","codex_magpie_catalog":".codex/magpie-catalog.json","grok_config":".grok/config.toml"}
def digest(data): return hashlib.sha256(data).hexdigest()
def validate(name,data):
    if name=="hermes_config":
        if b"\0" in data: raise ValueError("invalid hermes config")
        return
    if name in ("codex_catalog", "codex_magpie_catalog"): json.loads(data.decode("utf-8-sig"))
    else: tomllib.loads(data.decode("utf-8-sig"))
def inspect():
    out={"_home":str(Path.home())}
    items=dict(FILES); items["hermes_config"]=".hermes/config.yaml"
    for name,rel in items.items():
        path=Path.home()/rel
        data=path.read_bytes() if path.is_file() else b""
        out[name]={"exists":path.is_file(),"sha256":digest(data),"content":data.decode("utf-8",errors="replace")}
    return out
ENV={"OPENCODEX_API_AUTH_TOKEN":".config/environment.d/astrorder-opencodex.conf","MAGPIE_API_KEY":".config/environment.d/astrorder-magpie.conf"}
def env_line(name,key):
    if any(c in key for c in "\r\n\0"): raise ValueError("invalid API key")
    return (name+'="'+key.replace('\\','\\\\').replace('"','\\"')+'"\n').encode()
def credential_values(payload):
    values={}
    raw=payload.get("api_keys")
    if isinstance(raw,dict):
        for name in ENV:
            value=raw.get(name)
            if isinstance(value,str) and value: values[name]=value
    key=payload.get("api_key")
    if isinstance(key,str) and key and "OPENCODEX_API_AUTH_TOKEN" not in values: values["OPENCODEX_API_AUTH_TOKEN"]=key
    return values
def restore(path,current,existed):
    temp=path.with_name("."+path.name+".astrorder-rollback")
    if existed:
        temp.write_bytes(current);os.replace(temp,path)
    else:path.unlink(missing_ok=True)
def apply(payload):
    prepared=[]
    for name,text in payload["files"].items():
        if name=="hermes_config": rel=".hermes/config.yaml"
        elif name in FILES: rel=FILES[name]
        else: raise ValueError("invalid managed file")
        if not isinstance(text,str): raise ValueError("invalid managed file")
        path=Path.home()/rel
        if name=="hermes_config" and not path.is_file(): continue
        data=text.encode("utf-8");validate(name,data);path.parent.mkdir(parents=True,exist_ok=True)
        existed=path.is_file();current=path.read_bytes() if existed else b""
        if current==data: continue
        temp=path.with_name("."+path.name+".astrorder-tmp");temp.write_bytes(data)
        try: validate(name,temp.read_bytes())
        except Exception:
            temp.unlink(missing_ok=True)
            for _,_,earlier,_,_ in prepared: earlier.unlink(missing_ok=True)
            raise
        prepared.append((name,path,temp,current,existed))
    envs=[]
    for cname,ckey in credential_values(payload).items():
        path=Path.home()/ENV[cname];path.parent.mkdir(parents=True,exist_ok=True)
        existed=path.is_file();current=path.read_bytes() if existed else b"";data=env_line(cname,ckey)
        if current!=data:
            temp=path.with_name("."+path.name+".tmp");temp.write_bytes(data);os.chmod(temp,0o600);envs.append((path,temp,current,existed))
    replaced=[]
    try:
        for name,path,temp,current,existed in prepared:
            if existed:
                backup=path.with_name(path.name+".astrorder-backup-"+str(__import__('time').time_ns()));backup.write_bytes(current)
            os.replace(temp,path);os.chmod(path,0o600);replaced.append((path,current,existed))
        for path,temp,current,existed in envs:
            os.replace(temp,path);os.chmod(path,0o600);replaced.append((path,current,existed))
    except Exception:
        for path,current,existed in reversed(replaced): restore(path,current,existed)
        for _,_,temp,_,_ in prepared: temp.unlink(missing_ok=True)
        for _,temp,_,_ in envs: temp.unlink(missing_ok=True)
        raise
    for _,path,_,_,_ in prepared:
        backups=sorted(path.parent.glob(path.name+".astrorder-backup-*"),key=lambda p:p.stat().st_mtime,reverse=True)
        for stale in backups[3:]: stale.unlink(missing_ok=True)
    return {"changed":[item[0] for item in prepared],"files":inspect()}
try:
    request=json.loads(sys.stdin.read());result=inspect() if request["action"]=="plan" else apply(request)
    print(json.dumps({"ok":True,"result":result},ensure_ascii=False,separators=(",",":")))
except Exception as exc:
    print(json.dumps({"ok":False,"detail":str(exc)[:500]},ensure_ascii=False,separators=(",",":")));raise SystemExit(2)
'''


def _digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def _validate(name: str, data: bytes) -> None:
    text = data.decode("utf-8-sig")
    if name == "hermes_config":
        if "\x00" in text:
            raise DaemonProtocolError("Hermes 配置无效")
        return
    json.loads(text) if name in ("codex_catalog", "codex_magpie_catalog") else tomllib.loads(text)


def _environment_data(api_key: str) -> bytes:
    return _environment_line("OPENCODEX_API_AUTH_TOKEN", api_key)


def _environment_line(name: str, api_key: str) -> bytes:
    if name not in ENV_FILES:
        raise DaemonProtocolError("模型网关 Key 名称无效")
    if any(char in api_key for char in "\r\n\0"):
        raise DaemonProtocolError("模型网关 Key 不能包含换行或空字符")
    escaped = api_key.replace("\\", "\\\\").replace('"', '\\"')
    return f'{name}="{escaped}"\n'.encode()


def _credential_values(api_key: str, api_keys: Mapping[str, Any] | None) -> dict[str, str]:
    values: dict[str, str] = {}
    if isinstance(api_keys, Mapping):
        for name in ENV_FILES:
            value = api_keys.get(name)
            if isinstance(value, str) and value:
                _environment_line(name, value)
                values[name] = value
    if api_key and "OPENCODEX_API_AUTH_TOKEN" not in values:
        _environment_line("OPENCODEX_API_AUTH_TOKEN", api_key)
        values["OPENCODEX_API_AUTH_TOKEN"] = api_key
    return values


def _hermes_path() -> Path:
    relative = "AppData/Local/hermes/config.yaml" if os.name == "nt" else ".hermes/config.yaml"
    return Path.home() / relative


def _restore_file(path: Path, current: bytes, existed: bool) -> None:
    if not existed:
        path.unlink(missing_ok=True)
        return
    temp = path.with_name("." + path.name + ".astrorder-rollback")
    temp.write_bytes(current)
    os.replace(temp, path)
    if os.name != "nt":
        path.chmod(0o600)


def _inspect_local() -> dict[str, Any]:
    result = {"_home": str(Path.home())}
    paths = {name: Path.home() / relative for name, relative in FILES.items()}
    paths["hermes_config"] = _hermes_path()
    for name, path in paths.items():
        data = path.read_bytes() if path.is_file() else b""
        result[name] = {"exists": path.is_file(), "sha256": _digest(data), "content": data.decode("utf-8", errors="replace")}
    return result


def _apply_local(files: Mapping[str, Any], api_key: str, api_keys: Mapping[str, Any] | None = None) -> dict[str, Any]:
    values = _credential_values(api_key, api_keys)
    prepared: list[tuple[str, Path, Path, bytes, bool]] = []
    for name, text in files.items():
        if name == "hermes_config":
            path = _hermes_path()
            if not path.is_file():
                continue
        elif name in FILES:
            path = Path.home() / FILES[name]
        else:
            raise DaemonProtocolError("模型配置包含未授权文件")
        if not isinstance(text, str):
            raise DaemonProtocolError("模型配置包含未授权文件")
        data = text.encode()
        _validate(name, data)
        path.parent.mkdir(parents=True, exist_ok=True)
        existed = path.is_file()
        current = path.read_bytes() if existed else b""
        if current == data:
            continue
        temp = path.with_name("." + path.name + ".astrorder-tmp")
        temp.write_bytes(data)
        try:
            _validate(name, temp.read_bytes())
        except Exception:
            temp.unlink(missing_ok=True)
            for _name, _path, earlier, _current, _existed in prepared:
                earlier.unlink(missing_ok=True)
            raise
        prepared.append((name, path, temp, current, existed))
    environments: list[tuple[Path, Path, bytes, bool]] = []
    if values and os.name != "nt":
        try:
            for name, value in values.items():
                path = Path.home() / ENV_FILES[name]
                path.parent.mkdir(parents=True, exist_ok=True)
                existed = path.is_file()
                current = path.read_bytes() if existed else b""
                data = _environment_line(name, value)
                if current == data:
                    continue
                temp = path.with_name("." + path.name + ".tmp")
                temp.write_bytes(data)
                temp.chmod(0o600)
                environments.append((path, temp, current, existed))
        except Exception:
            for _name, _path, earlier, _current, _existed in prepared:
                earlier.unlink(missing_ok=True)
            for _path, temp, _current, _existed in environments:
                temp.unlink(missing_ok=True)
            raise
    replaced: list[tuple[Path, bytes, bool]] = []
    registry_key: Any = None
    registry_written: list[tuple[str, bool, str, str | None]] = []
    try:
        for name, path, temp, current, existed in prepared:
            if existed:
                backup = path.with_name(path.name + f".astrorder-backup-{__import__('time').time_ns()}")
                backup.write_bytes(current)
            os.replace(temp, path)
            if os.name != "nt":
                path.chmod(0o600)
            replaced.append((path, current, existed))
        for path, temp, current, existed in environments:
            os.replace(temp, path)
            path.chmod(0o600)
            replaced.append((path, current, existed))
        if values and os.name == "nt":
            import winreg
            registry_key = winreg.CreateKey(winreg.HKEY_CURRENT_USER, "Environment")
            for name, value in values.items():
                try:
                    previous = winreg.QueryValueEx(registry_key, name)[0]
                    existed = True
                except FileNotFoundError:
                    previous, existed = "", False
                prior_env = os.environ.get(name)
                winreg.SetValueEx(registry_key, name, 0, winreg.REG_SZ, value)
                os.environ[name] = value
                registry_written.append((name, existed, previous, prior_env))
    except Exception:
        if registry_key is not None and registry_written:
            import winreg
            for name, existed, previous, prior_env in reversed(registry_written):
                if existed:
                    winreg.SetValueEx(registry_key, name, 0, winreg.REG_SZ, previous)
                else:
                    try:
                        winreg.DeleteValue(registry_key, name)
                    except FileNotFoundError:
                        pass
                if prior_env is None:
                    os.environ.pop(name, None)
                else:
                    os.environ[name] = prior_env
        for path, current, existed in reversed(replaced):
            _restore_file(path, current, existed)
        for _name, _path, temp, _current, _existed in prepared:
            temp.unlink(missing_ok=True)
        for _path, temp, _current, _existed in environments:
            temp.unlink(missing_ok=True)
        raise
    finally:
        if registry_key is not None:
            registry_key.Close()
    for _name, path, _temp, _current, _existed in prepared:
        backups = sorted(path.parent.glob(path.name + ".astrorder-backup-*"), key=lambda item: item.stat().st_mtime, reverse=True)
        for stale in backups[3:]:
            stale.unlink(missing_ok=True)
    return {"changed": [item[0] for item in prepared], "files": _inspect_local()}


def _ssh_argv(settings: Mapping[str, Any]) -> list[str]:
    target = settings.get("ssh_config_alias") or settings.get("host")
    if not isinstance(target, str) or not target or re.search(r"[\s\x00-\x1f]", target):
        raise DaemonProtocolError("SSH 目标无效")
    argv = ["ssh", "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=12"]
    if not settings.get("ssh_config_alias"):
        port = int(settings.get("port") or 22)
        if not 1 <= port <= 65535:
            raise DaemonProtocolError("SSH 端口无效")
        argv += ["-p", str(port)]
        identity = settings.get("identity_file")
        if identity:
            argv += ["-i", str(identity)]
        user = settings.get("user")
        if user:
            target = f"{user}@{target}"
    return [*argv, target]


def _remote_request(target: Mapping[str, Any], payload: dict[str, Any]) -> dict[str, Any]:
    encoded = base64.b64encode(_REMOTE.encode()).decode()
    command = f"python3 -c \"import base64;exec(base64.b64decode('{encoded}'))\""
    kind = target.get("kind")
    if kind == "wsl":
        distro = target.get("distro")
        if not isinstance(distro, str) or not distro or re.search(r"[\r\n\x00]", distro):
            raise DaemonProtocolError("WSL 发行版无效")
        argv = ["wsl.exe", "-d", distro, "--", "sh", "-lc", command]
    elif kind == "ssh":
        settings = target.get("settings")
        if not isinstance(settings, Mapping):
            raise DaemonProtocolError("SSH 设置无效")
        argv = [*_ssh_argv(settings), command]
    else:
        raise DaemonProtocolError("模型配置目标类型无效")
    completed = subprocess.run(
        argv, input=json.dumps(payload), text=True, encoding="utf-8", errors="replace",
        capture_output=True, timeout=45, check=False,
        creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0),
    )
    try:
        response = json.loads(completed.stdout.strip().splitlines()[-1])
    except (json.JSONDecodeError, IndexError) as exc:
        raise DaemonProtocolError("目标未返回有效的模型配置结果") from exc
    if completed.returncode or not response.get("ok"):
        raise DaemonProtocolError(str(response.get("detail") or "模型配置目标执行失败"))
    return response["result"]


def execute_model_config(action: str, request: Mapping[str, Any]) -> dict[str, Any]:
    if action not in {"model_config.plan", "model_config.apply"}:
        raise DaemonProtocolError("模型配置操作无效")
    target = request.get("target")
    if not isinstance(target, Mapping):
        raise DaemonProtocolError("模型配置目标无效")
    kind = target.get("kind")
    payload: dict[str, Any] = {"action": "plan"}
    if action == "model_config.apply":
        files = request.get("files")
        api_key = request.get("api_key", "")
        api_keys = request.get("api_keys") or {}
        if not isinstance(files, Mapping) or not isinstance(api_key, str) or not isinstance(api_keys, Mapping):
            raise DaemonProtocolError("模型配置写入内容无效")
        payload = {"action": "apply", "files": dict(files), "api_key": api_key, "api_keys": dict(api_keys)}
    if kind == "local":
        if action == "model_config.plan":
            return _inspect_local()
        return _apply_local(payload["files"], payload["api_key"], payload["api_keys"])
    return _remote_request(target, payload)
