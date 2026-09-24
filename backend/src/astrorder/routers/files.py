from __future__ import annotations

import mimetypes
import os
import subprocess
import urllib.parse
from pathlib import Path
from typing import Any

from fastapi import APIRouter, HTTPException, Query, Request
from fastapi.responses import FileResponse, Response
from pydantic import BaseModel, Field

from ..core.staging_files import StageFilesPayload, stage_files_handler

router = APIRouter()


def _private(request: Request) -> None:
    from ..core.auth import require_browser
    require_browser(request, request.app.state.settings)


@router.get("/api/v1/files/tree")
def get_files_tree(
    request: Request,
    path: str = Query(default=""),
    depth: int = Query(default=6),
    reveal_path: str = Query(default=""),
    session_id: str = Query(default=""),
    connection_id: str = Query(default=""),
) -> dict[str, object]:
    _private(request)
    import json
    cleaned_path = urllib.parse.unquote(path).strip().strip('<>').strip('"\'')
    if cleaned_path.startswith("file:///"):
        cleaned_path = cleaned_path[8:]
    elif cleaned_path.startswith("file://"):
        cleaned_path = cleaned_path[7:]

    cleaned_reveal_path = urllib.parse.unquote(reveal_path).strip().strip('<>').strip('"\'')
    if cleaned_reveal_path.startswith("file:///"):
        cleaned_reveal_path = cleaned_reveal_path[8:]
    elif cleaned_reveal_path.startswith("file://"):
        cleaned_reveal_path = cleaned_reveal_path[7:]

    # 优先根据 session_id 或 connection_id 判断是否为 SSH 远程项目，并补全工作区
    resolved_cid = connection_id
    if session_id:
        try:
            sess = request.app.state.store.find_session_by_id(session_id)
            if sess:
                if not resolved_cid and sess.get("connection_id") and sess["connection_id"] != "local":
                    resolved_cid = sess["connection_id"]
                if not cleaned_path and sess.get("workspace"):
                    cleaned_path = sess["workspace"]
        except Exception:
            pass

    # 如果是远程 SSH 环境，通过 SSH 执行远端 Python 获取目录树
    if resolved_cid and resolved_cid != "local":
        ssh_conn = request.app.state.store.get_ssh_connection(resolved_cid)
        if not ssh_conn:
            raise HTTPException(status_code=404, detail="SSH 连接不存在")
        from ..ssh_transport import SshNativeRuntime, build_remote_python_command
        runtime = SshNativeRuntime(
            ssh_conn["settings"],
            ssh_conn["id"],
            0,
            None,
            None,
            connector_secret=None,
        )
        argv = [a for a in runtime._base_ssh_argv() if a != "-T"] + ["-o", "BatchMode=yes", runtime._target()]

        remote_script = f"""
import json, os
from pathlib import Path

raw_reveal = {repr(cleaned_reveal_path)}
reveal = Path(raw_reveal).expanduser().resolve() if raw_reveal else None

def walk(p, depth={max(1, min(depth, 8))}):
    if depth <= 0 and not (reveal and (p == reveal or p in reveal.parents)): return []
    items = []
    ignored = {{'.git', '.venv', 'node_modules', '__pycache__', '.pytest_cache', '.ruff_cache', 'dist'}}
    try:
        entries = sorted(list(p.iterdir()), key=lambda e: (not e.is_dir(), e.name.lower()))
        for x in entries:
            if x.name in ignored: continue
            is_dir = x.is_dir()
            node = {{'name': x.name, 'path': str(x), 'is_dir': is_dir}}
            if is_dir:
                node['children'] = walk(x, depth - 1)
            else:
                try: node['size'] = x.stat().st_size
                except: node['size'] = 0
            items.append(node)
    except: pass
    return items

raw_path = {repr(cleaned_path)}
root = Path(raw_path).expanduser().resolve() if raw_path else Path.cwd()
print(json.dumps({{
    'root': str(root),
    'name': root.name or str(root),
    'parent': str(root.parent) if root.parent != root else None,
    'drives': [],
    'items': walk(root),
}}))
"""
        cmd = build_remote_python_command(remote_script)
        try:
            res = subprocess.run(
                argv + [cmd],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                encoding="utf-8",
                errors="replace",
                timeout=15,
                check=False,
            )
            if res.returncode == 0 and res.stdout.strip():
                return json.loads(res.stdout.strip())
            raise HTTPException(status_code=502, detail=f"远端目录读取失败: {res.stderr}")
        except subprocess.TimeoutExpired:
            raise HTTPException(status_code=504, detail="读取远程文件树超时")
        except Exception as exc:
            if isinstance(exc, HTTPException): raise
            raise HTTPException(status_code=500, detail=f"远程文件树错误: {exc}")

    # 本地环境
    if not cleaned_path:
        root = Path.cwd()
    else:
        root = Path(cleaned_path).expanduser().resolve()

    if not root.exists() or not root.is_dir():
        if root.exists() and root.is_file():
            if not cleaned_reveal_path:
                cleaned_reveal_path = str(root)
            root = root.parent
        else:
            fallback_root = None
            if session_id:
                try:
                    s = request.app.state.store.find_session_by_id(session_id)
                    if s and s.get("workspace"):
                        ws = Path(s["workspace"]).expanduser().resolve()
                        if ws.exists() and ws.is_dir():
                            fallback_root = ws
                except Exception:
                    pass
            if not fallback_root:
                settings = request.app.state.settings
                for w in (getattr(settings, "allowed_workspaces", None) or []):
                    wp = Path(w).resolve()
                    if wp.exists() and wp.is_dir():
                        fallback_root = wp
                        break
            if fallback_root:
                root = fallback_root
            elif not root.exists() or not root.is_dir():
                raise HTTPException(status_code=404, detail="Path does not exist")

    reveal = Path(cleaned_reveal_path).expanduser().resolve() if cleaned_reveal_path else None
    ignored = {".git", ".venv", "node_modules", "__pycache__", ".pytest_cache", ".ruff_cache", "dist"}

    def walk_tree(current_dir: Path, current_depth: int) -> list[dict[str, object]]:
        if current_depth <= 0 and not (reveal and (current_dir == reveal or current_dir in reveal.parents)):
            return []
        items = []
        try:
            entries = sorted(
                list(current_dir.iterdir()),
                key=lambda e: (not e.is_dir(), e.name.lower()),
            )
            for entry in entries:
                if entry.name in ignored:
                    continue
                is_dir = entry.is_dir()
                node: dict[str, object] = {
                    "name": entry.name,
                    "path": str(entry),
                    "is_dir": is_dir,
                }
                if is_dir:
                    node["children"] = walk_tree(entry, current_depth - 1)
                else:
                    try:
                        node["size"] = entry.stat().st_size
                    except Exception:
                        node["size"] = 0
                items.append(node)
        except Exception:
            pass
        return items

    drives = []
    if (not resolved_cid or resolved_cid == "local") and os.name == "nt":
        import string
        drives = [f"{d}:\\" for d in string.ascii_uppercase if os.path.exists(f"{d}:\\")]

    return {
        "root": str(root),
        "name": root.name or str(root),
        "parent": str(root.parent) if root.parent != root else None,
        "drives": drives,
        "items": walk_tree(root, depth),
    }


@router.get("/api/v1/files/search")
def search_files(
    request: Request,
    q: str = Query(default="", max_length=256),
    limit: int = Query(default=30, ge=1, le=100),
    session_id: str = Query(default=""),
    connection_id: str = Query(default=""),
) -> dict[str, object]:
    """模糊搜索工作区文件（Quick Open）。只匹配文件名，跳过常见缓存目录。"""
    _private(request)
    query = q.strip().lower()
    resolved_cid = connection_id
    cleaned_path = ""
    if not resolved_cid and session_id:
        try:
            sess = request.app.state.store.find_session_by_id(session_id)
            if sess and sess.get("connection_id") and sess["connection_id"] != "local":
                resolved_cid = sess["connection_id"]
            if sess and sess.get("workspace"):
                cleaned_path = sess["workspace"]
        except Exception:
            pass

    ignored_dirs = {
        ".git", ".venv", "venv", "node_modules", "__pycache__",
        ".pytest_cache", ".ruff_cache", "dist", "build", ".next", ".turbo",
        "target", ".cargo", "coverage", ".idea", ".vscode",
    }

    # 远程环境通过 SSH 执行 Python 快速查找
    if resolved_cid and resolved_cid != "local":
        ssh_conn = request.app.state.store.get_ssh_connection(resolved_cid)
        if not ssh_conn:
            raise HTTPException(status_code=404, detail="SSH 连接不存在")
        from ..ssh_transport import SshNativeRuntime, build_remote_python_command
        runtime = SshNativeRuntime(
            ssh_conn["settings"],
            ssh_conn["id"],
            0,
            None,
            None,
            connector_secret=None,
        )
        argv = [a for a in runtime._base_ssh_argv() if a != "-T"] + ["-o", "BatchMode=yes", runtime._target()]

        remote_script = f"""
import json, os
from pathlib import Path

raw_path = {repr(cleaned_path)}
root = Path(raw_path).expanduser().resolve() if raw_path else Path.cwd()
query = {repr(query)}
limit = {limit}
ignored = {repr(ignored_dirs)}

results = []
try:
    for dirpath, dirnames, filenames in os.walk(str(root)):
        dirnames[:] = [d for d in dirnames if d not in ignored and not d.startswith('.')]
        for fname in filenames:
            if fname.startswith('.'): continue
            if query and query not in fname.lower(): continue
            full = os.path.join(dirpath, fname)
            rel = os.path.relpath(full, str(root))
            results.append({{'name': fname, 'path': full.replace('\\\\', '/'), 'relative_path': rel.replace('\\\\', '/')}})
            if len(results) >= limit: break
        if len(results) >= limit: break
except: pass
print(json.dumps({{'items': results, 'root': str(root)}}))
"""
        cmd = build_remote_python_command(remote_script)
        try:
            res = subprocess.run(
                argv + [cmd],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                encoding="utf-8",
                errors="replace",
                timeout=12,
                check=False,
            )
            if res.returncode == 0 and res.stdout.strip():
                return json.loads(res.stdout.strip())
            return {"items": [], "root": cleaned_path}
        except Exception:
            return {"items": [], "root": cleaned_path}

    # 本地环境
    if cleaned_path:
        root = Path(cleaned_path).expanduser().resolve()
    else:
        root = Path.cwd()

    if not root.exists() or not root.is_dir():
        return {"items": [], "root": str(root)}

    results: list[dict[str, str]] = []
    root_str = str(root)
    try:
        for dirpath, dirnames, filenames in os.walk(root_str):
            dirnames[:] = [d for d in dirnames if d not in ignored_dirs and not d.startswith(".")]
            for fname in filenames:
                if fname.startswith("."):
                    continue
                if query and query not in fname.lower():
                    continue
                full_path = os.path.join(dirpath, fname)
                try:
                    rel_path = os.path.relpath(full_path, root_str)
                except ValueError:
                    rel_path = full_path
                results.append({
                    "name": fname,
                    "path": full_path.replace("\\", "/"),
                    "relative_path": rel_path.replace("\\", "/"),
                })
                if len(results) >= limit:
                    break
            if len(results) >= limit:
                break
    except Exception:
        pass

    return {"items": results, "root": root_str.replace("\\", "/")}


@router.post("/api/v1/files/stage")
def stage_files_endpoint(payload: StageFilesPayload, request: Request) -> dict[str, object]:
    _private(request)
    return stage_files_handler(payload, request)


@router.get("/api/v1/files/raw")
def get_raw_file(
    request: Request,
    path: str = Query(...),
    download: bool = Query(default=False),
    session_id: str = Query(default=""),
    connection_id: str = Query(default=""),
) -> Response:
    _private(request)
    cleaned_path = urllib.parse.unquote(path).strip().strip('<>').strip('"\'')

    resolved_cid = connection_id
    if not resolved_cid and session_id:
        try:
            sess = request.app.state.store.find_session_by_id(session_id)
            if sess and sess.get("connection_id") and sess["connection_id"] != "local":
                resolved_cid = sess["connection_id"]
        except Exception:
            pass

    # 如果是远程 SSH 路径，通过 SSH 读取内容
    if resolved_cid and resolved_cid != "local":
        ssh_conn = request.app.state.store.get_ssh_connection(resolved_cid)
        if not ssh_conn:
            raise HTTPException(status_code=404, detail="SSH 连接不存在")
        from ..ssh_transport import SshNativeRuntime, build_remote_python_command
        runtime = SshNativeRuntime(
            ssh_conn["settings"],
            ssh_conn["id"],
            0,
            None,
            None,
            connector_secret=None,
        )
        argv = [a for a in runtime._base_ssh_argv() if a != "-T"] + ["-o", "BatchMode=yes", runtime._target()]
        remote_script = f"""
import os, sys
from pathlib import Path
target = Path({repr(cleaned_path)}).expanduser().resolve()
if not target.exists() or not target.is_file():
    sys.exit(44)
sys.stdout.buffer.write(target.read_bytes())
"""
        cmd = build_remote_python_command(remote_script)
        try:
            res = subprocess.run(
                argv + [cmd],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=20,
                check=False,
            )
            if res.returncode == 44:
                raise HTTPException(status_code=404, detail="远程文件不存在")
            if res.returncode != 0:
                raise HTTPException(status_code=502, detail=f"读取远程文件失败: {res.stderr.decode('utf-8', errors='replace')}")
            content = res.stdout
            filename = Path(cleaned_path).name
            media_type, _ = mimetypes.guess_type(filename)
            headers = {}
            if download:
                headers["Content-Disposition"] = f'attachment; filename="{filename}"'
            return Response(
                content=content,
                media_type=media_type or "application/octet-stream",
                headers=headers,
            )
        except subprocess.TimeoutExpired:
            raise HTTPException(status_code=504, detail="读取远程文件超时")
        except HTTPException:
            raise
        except Exception as exc:
            raise HTTPException(status_code=500, detail=f"读取远程文件失败: {exc}")

    # 本地文件读取
    try:
        resolved = Path(cleaned_path).resolve()
    except (OSError, ValueError):
        raise HTTPException(status_code=400, detail="Invalid file path")
    if not resolved.is_file():
        raise HTTPException(status_code=404, detail="File was not found")
    media_type, _ = mimetypes.guess_type(str(resolved))
    if not media_type:
        media_type = "application/octet-stream"
    return FileResponse(
        str(resolved),
        media_type=media_type,
        filename=resolved.name if download else None,
    )
