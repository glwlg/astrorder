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
    "grok_config": ".grok/config.toml",
}

_REMOTE = r'''
import hashlib,json,os,sys,tomllib
from pathlib import Path
FILES={"codex_config":".codex/config.toml","codex_catalog":".codex/opencodex-catalog.json","grok_config":".grok/config.toml"}
def digest(data): return hashlib.sha256(data).hexdigest()
def validate(name,data):
    if name=="codex_catalog": json.loads(data.decode("utf-8"))
    else: tomllib.loads(data.decode("utf-8"))
def inspect():
    out={"_home":str(Path.home())}
    for name,rel in FILES.items():
        path=Path.home()/rel
        data=path.read_bytes() if path.is_file() else b""
        out[name]={"exists":path.is_file(),"sha256":digest(data),"content":data.decode("utf-8",errors="replace")}
    return out
def env_data(key):
    if any(c in key for c in "\r\n\0"): raise ValueError("invalid API key")
    return ('OPENCODEX_API_AUTH_TOKEN="'+key.replace('\\','\\\\').replace('"','\\"')+'"\n').encode()
def restore(path,current,existed):
    temp=path.with_name("."+path.name+".astrorder-rollback")
    if existed:
        temp.write_bytes(current);os.replace(temp,path)
    else:path.unlink(missing_ok=True)
def apply(payload):
    prepared=[]
    for name,text in payload["files"].items():
        if name not in FILES or not isinstance(text,str): raise ValueError("invalid managed file")
        data=text.encode("utf-8");validate(name,data);path=Path.home()/FILES[name];path.parent.mkdir(parents=True,exist_ok=True)
        existed=path.is_file();current=path.read_bytes() if existed else b""
        if current==data: continue
        temp=path.with_name("."+path.name+".astrorder-tmp");temp.write_bytes(data)
        try: validate(name,temp.read_bytes())
        except Exception:
            temp.unlink(missing_ok=True)
            for _,_,earlier,_,_ in prepared: earlier.unlink(missing_ok=True)
            raise
        prepared.append((name,path,temp,current,existed))
    env=None
    key=payload.get("api_key")
    if isinstance(key,str) and key:
        path=Path.home()/".config/environment.d/astrorder-opencodex.conf";path.parent.mkdir(parents=True,exist_ok=True)
        existed=path.is_file();current=path.read_bytes() if existed else b"";data=env_data(key)
        if current!=data:
            temp=path.with_name("."+path.name+".tmp");temp.write_bytes(data);os.chmod(temp,0o600);env=(path,temp,current,existed)
    replaced=[]
    try:
        for name,path,temp,current,existed in prepared:
            if existed:
                backup=path.with_name(path.name+".astrorder-backup-"+str(__import__('time').time_ns()));backup.write_bytes(current)
            os.replace(temp,path);os.chmod(path,0o600);replaced.append((path,current,existed))
        if env:
            path,temp,current,existed=env;os.replace(temp,path);os.chmod(path,0o600);replaced.append((path,current,existed))
    except Exception:
        for path,current,existed in reversed(replaced): restore(path,current,existed)
        for _,_,temp,_,_ in prepared: temp.unlink(missing_ok=True)
        if env: env[1].unlink(missing_ok=True)
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
    text = data.decode("utf-8")
    json.loads(text) if name == "codex_catalog" else tomllib.loads(text)


def _environment_data(api_key: str) -> bytes:
    if any(char in api_key for char in "\r\n\0"):
        raise DaemonProtocolError("模型网关 Key 不能包含换行或空字符")
    escaped = api_key.replace("\\", "\\\\").replace('"', '\\"')
    return f'OPENCODEX_API_AUTH_TOKEN="{escaped}"\n'.encode()


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
    for name, relative in FILES.items():
        path = Path.home() / relative
        data = path.read_bytes() if path.is_file() else b""
        result[name] = {"exists": path.is_file(), "sha256": _digest(data), "content": data.decode("utf-8", errors="replace")}
    return result


def _apply_local(files: Mapping[str, Any], api_key: str) -> dict[str, Any]:
    prepared: list[tuple[str, Path, Path, bytes, bool]] = []
    for name, text in files.items():
        if name not in FILES or not isinstance(text, str):
            raise DaemonProtocolError("模型配置包含未授权文件")
        data = text.encode()
        _validate(name, data)
        path = Path.home() / FILES[name]
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
    environment: tuple[Path, Path, bytes, bool] | None = None
    if api_key and os.name != "nt":
        path = Path.home() / ".config/environment.d/astrorder-opencodex.conf"
        path.parent.mkdir(parents=True, exist_ok=True)
        existed = path.is_file()
        current = path.read_bytes() if existed else b""
        data = _environment_data(api_key)
        if current != data:
            temp = path.with_name("." + path.name + ".tmp")
            temp.write_bytes(data)
            temp.chmod(0o600)
            environment = (path, temp, current, existed)
    replaced: list[tuple[Path, bytes, bool]] = []
    registry: tuple[Any, bool, str] | None = None
    try:
        for name, path, temp, current, existed in prepared:
            if existed:
                backup = path.with_name(path.name + f".astrorder-backup-{__import__('time').time_ns()}")
                backup.write_bytes(current)
            os.replace(temp, path)
            if os.name != "nt":
                path.chmod(0o600)
            replaced.append((path, current, existed))
        if environment:
            path, temp, current, existed = environment
            os.replace(temp, path)
            path.chmod(0o600)
            replaced.append((path, current, existed))
        if api_key and os.name == "nt":
            import winreg
            key = winreg.CreateKey(winreg.HKEY_CURRENT_USER, "Environment")
            try:
                previous = winreg.QueryValueEx(key, "OPENCODEX_API_AUTH_TOKEN")[0]
                registry = (key, True, previous)
            except FileNotFoundError:
                registry = (key, False, "")
            winreg.SetValueEx(key, "OPENCODEX_API_AUTH_TOKEN", 0, winreg.REG_SZ, api_key)
            os.environ["OPENCODEX_API_AUTH_TOKEN"] = api_key
    except Exception:
        if registry:
            key, existed, previous = registry
            import winreg
            if existed:
                winreg.SetValueEx(key, "OPENCODEX_API_AUTH_TOKEN", 0, winreg.REG_SZ, previous)
            else:
                winreg.DeleteValue(key, "OPENCODEX_API_AUTH_TOKEN")
        for path, current, existed in reversed(replaced):
            _restore_file(path, current, existed)
        for _name, _path, temp, _current, _existed in prepared:
            temp.unlink(missing_ok=True)
        if environment:
            environment[1].unlink(missing_ok=True)
        raise
    finally:
        if registry:
            registry[0].Close()
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
        if not isinstance(files, Mapping) or not isinstance(api_key, str):
            raise DaemonProtocolError("模型配置写入内容无效")
        payload = {"action": "apply", "files": dict(files), "api_key": api_key}
    if kind == "local":
        return _inspect_local() if action == "model_config.plan" else _apply_local(payload["files"], payload["api_key"])
    return _remote_request(target, payload)
