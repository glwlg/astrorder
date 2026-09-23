from __future__ import annotations

import ctypes
import io
import os
import shutil
import struct
import subprocess
import tarfile
import urllib.parse
from pathlib import Path
from typing import Any
from fastapi import HTTPException, Request
from pydantic import BaseModel
from .auth import require_browser

def write_windows_filedrop(paths: list[str]) -> bool:
    """使用 Windows 原生 user32/kernel32 API 将物理文件路径列表写入系统 CF_HDROP 与 Preferred DropEffect 剪贴板。"""
    try:
        clean_paths = [str(Path(str(x).replace('\\', '/')).resolve()).replace('/', '\\') for x in paths if x and Path(str(x).replace('\\', '/')).exists()]
        if not clean_paths:
            return False
        user32 = ctypes.windll.user32
        kernel32 = ctypes.windll.kernel32

        kernel32.GlobalAlloc.restype = ctypes.c_void_p
        kernel32.GlobalAlloc.argtypes = [ctypes.c_uint, ctypes.c_size_t]
        kernel32.GlobalLock.restype = ctypes.c_void_p
        kernel32.GlobalLock.argtypes = [ctypes.c_void_p]
        kernel32.GlobalUnlock.argtypes = [ctypes.c_void_p]
        user32.OpenClipboard.argtypes = [ctypes.c_void_p]
        user32.SetClipboardData.restype = ctypes.c_void_p
        user32.SetClipboardData.argtypes = [ctypes.c_uint, ctypes.c_void_p]
        user32.RegisterClipboardFormatW.restype = ctypes.c_uint
        user32.RegisterClipboardFormatW.argtypes = [ctypes.c_wchar_p]

        # 1. CF_HDROP 数据包 (0x0042 = GMEM_MOVEABLE | GMEM_ZEROINIT)
        header = struct.pack("<IIIIi", 20, 0, 0, 0, 1)
        file_bytes = b"".join(p.encode("utf-16le") + b"\x00\x00" for p in clean_paths) + b"\x00\x00"
        payload = header + file_bytes

        hDrop = kernel32.GlobalAlloc(0x0042, len(payload))
        if not hDrop:
            return False
        pDrop = kernel32.GlobalLock(hDrop)
        if not pDrop:
            kernel32.GlobalFree(hDrop)
            return False
        ctypes.memmove(pDrop, payload, len(payload))
        kernel32.GlobalUnlock(hDrop)

        # 2. Preferred DropEffect (DROPEFFECT_COPY = 1，告诉 Explorer/Xftp 必须执行 Copy 操作)
        cf_effect = user32.RegisterClipboardFormatW("Preferred DropEffect")
        hEffect = kernel32.GlobalAlloc(0x0042, 4)
        pEffect = kernel32.GlobalLock(hEffect) if hEffect else None
        if pEffect:
            ctypes.memmove(pEffect, struct.pack("<I", 1), 4)
            kernel32.GlobalUnlock(hEffect)

        if not user32.OpenClipboard(None):
            kernel32.GlobalFree(hDrop)
            if hEffect:
                kernel32.GlobalFree(hEffect)
            return False
        try:
            user32.EmptyClipboard()
            res = user32.SetClipboardData(15, hDrop)
            if hEffect:
                user32.SetClipboardData(cf_effect, hEffect)
            return bool(res)
        finally:
            user32.CloseClipboard()
    except Exception:
        return False

class StageFilesPayload(BaseModel):
    paths: list[str]
    session_id: str | None = None
    connection_id: str | None = None
    copy_to_clipboard: bool = True

def stage_files_handler(payload: StageFilesPayload, request: Request) -> dict[str, Any]:
    """暂存文件至本地 Windows 剪贴板缓存区，返回规范的绝对路径并直接写入系统剪贴板。"""
    settings = getattr(request.app.state, "settings", None)
    if settings:
        require_browser(request, settings)

    staging_root = (Path(os.environ.get('PUBLIC', 'C:/Users/Public')) / 'Astrorder' / 'clipboard_staging').resolve()
    staging_root.mkdir(parents=True, exist_ok=True)

    resolved_cid = payload.connection_id
    if not resolved_cid and payload.session_id:
        try:
            sess = request.app.state.store.find_session_by_id(payload.session_id)
            if sess and sess.get("connection_id") and sess["connection_id"] != "local":
                resolved_cid = sess["connection_id"]
        except Exception:
            pass

    staged_local_paths: list[str] = []

    # 1. 本地文件：直接规范化为 Windows 反斜杠绝对物理路径
    if not resolved_cid or resolved_cid == "local":
        for p in payload.paths:
            clean = urllib.parse.unquote(p).strip().strip("<>").strip("'\"")
            local_p = Path(clean).resolve()
            if local_p.exists():
                staged_local_paths.append(str(local_p).replace("/", "\\"))
            else:
                raise HTTPException(status_code=404, detail=f"本地文件不存在: {clean}")
        if payload.copy_to_clipboard and staged_local_paths:
            write_windows_filedrop(staged_local_paths)
        return {"ok": True, "local_paths": staged_local_paths, "clipboard_set": bool(payload.copy_to_clipboard)}

    # 2. 远程 SSH 文件：拉取并暂存到本地物理文件
    ssh_conn = request.app.state.store.get_ssh_connection(resolved_cid)
    if not ssh_conn:
        raise HTTPException(status_code=404, detail="SSH 连接不存在")

    from astrorder.ssh_transport import SshNativeRuntime, build_remote_python_command
    runtime = SshNativeRuntime(
        ssh_conn["settings"],
        ssh_conn["id"],
        0,
        None,
        None,
        connector_secret=None,
    )
    argv = [a for a in runtime._base_ssh_argv() if a != "-T"] + ["-o", "BatchMode=yes", runtime._target()]

    for p in payload.paths:
        clean = urllib.parse.unquote(p).strip().strip("<>").strip("'\"")
        name = clean.rstrip("/").split("/")[-1] or "remote_file"
        target_local = (staging_root / name).resolve()

        remote_script = (
            "import os, sys, tarfile, io\n"
            "from pathlib import Path\n"
            f"p = Path({repr(clean)}).expanduser().resolve()\n"
            "if not p.exists():\n"
            "    sys.exit(44)\n"
            "if p.is_file():\n"
            "    sys.stdout.buffer.write(b'FILE\\n' + p.read_bytes())\n"
            "else:\n"
            "    buf = io.BytesIO()\n"
            "    with tarfile.open(fileobj=buf, mode='w:gz') as tar:\n"
            "        tar.add(str(p), arcname=p.name)\n"
            "    sys.stdout.buffer.write(b'TARGZ\\n' + buf.getvalue())\n"
        )
        cmd = build_remote_python_command(remote_script)
        try:
            res = subprocess.run(
                argv + [cmd],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=30,
                check=False,
                creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0),
            )
            if res.returncode == 44:
                raise HTTPException(status_code=404, detail=f"远程文件不存在: {clean}")
            if res.returncode != 0:
                raise HTTPException(status_code=502, detail=f"读取远程文件失败: {clean}")
            raw = res.stdout
            if raw.startswith(b"FILE\n"):
                target_local.write_bytes(raw[5:])
                staged_local_paths.append(str(target_local).replace("/", "\\"))
            elif raw.startswith(b"TARGZ\n"):
                tar_bytes = raw[6:]
                if target_local.exists():
                    if target_local.is_dir():
                        shutil.rmtree(target_local, ignore_errors=True)
                    else:
                        target_local.unlink(missing_ok=True)
                with tarfile.open(fileobj=io.BytesIO(tar_bytes), mode="r:gz") as tar:
                    tar.extractall(path=staging_root)
                staged_local_paths.append(str(target_local).replace("/", "\\"))
            else:
                raise HTTPException(status_code=502, detail=f"未识别的远程响应: {clean}")
        except subprocess.TimeoutExpired:
            raise HTTPException(status_code=504, detail=f"下载远程文件超时: {clean}")
        except HTTPException:
            raise
        except Exception as exc:
            raise HTTPException(status_code=500, detail=f"暂存远程文件失败: {exc}")

    if payload.copy_to_clipboard and staged_local_paths:
        write_windows_filedrop(staged_local_paths)

    return {"ok": True, "local_paths": staged_local_paths, "clipboard_set": bool(payload.copy_to_clipboard)}
