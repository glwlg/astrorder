from __future__ import annotations

import os
import re
import shlex
import shutil
import subprocess
from fastapi import APIRouter, HTTPException, Request
from fastapi.responses import Response

router = APIRouter()


def _private(request: Request) -> None:
    from ..auth import require_browser
    require_browser(request)


def _run_git(args: list[str], cwd: str, connection_id: str | None = None, app_state = None) -> tuple[int, str, str]:
    """在本地或远程 SSH 环境中执行 git 命令"""
    if connection_id and connection_id != "local" and app_state:
        ssh_conn = None
        store = getattr(app_state, "store", None)
        if store:
            try:
                ssh_conn = store.get_ssh_connection(connection_id)
            except Exception:
                pass
        if not ssh_conn:
            controller = getattr(app_state, "environments", None) or getattr(app_state, "connections", None)
            if controller:
                for item in getattr(store, "list_ssh_connections", lambda: [])():
                    if str(item.get("id")) == str(connection_id):
                        ssh_conn = item
                        break
        if ssh_conn:
            from ..ssh_transport import SshNativeRuntime
            settings = ssh_conn.get("settings") or ssh_conn
            runtime = SshNativeRuntime(
                settings,
                str(ssh_conn.get("id") or connection_id),
                0,
                None,
                None,
                connector_secret=None,
            )
            inner_cmd = f"cd {shlex.quote(cwd)} && git " + " ".join(shlex.quote(a) for a in args)
            argv = [a for a in runtime._base_ssh_argv() if a != "-T"] + [
                "-o", "BatchMode=yes",
                "-o", "StrictHostKeyChecking=accept-new",
                runtime._target(),
                inner_cmd,
            ]
            from ..connections import _windows_hide_flags, _windows_hide_startupinfo
            try:
                proc = subprocess.run(
                    argv,
                    capture_output=True,
                    text=True,
                    encoding="utf-8",
                    errors="replace",
                    creationflags=_windows_hide_flags(),
                    startupinfo=_windows_hide_startupinfo(),
                    timeout=30,
                )
                return proc.returncode, proc.stdout, proc.stderr
            except Exception as e:
                return 1, "", str(e)

    # 本地执行
    git_bin = shutil.which("git") or "git"
    from ..connections import _windows_hide_flags, _windows_hide_startupinfo
    proc = subprocess.run(
        [git_bin] + args,
        cwd=cwd,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        creationflags=_windows_hide_flags(),
        startupinfo=_windows_hide_startupinfo(),
    )
    return proc.returncode, proc.stdout, proc.stderr


@router.get("/api/v1/git/status")
def get_git_status(request: Request, workspace: str | None = None, session_id: str | None = None, connection_id: str | None = None, base_branch: str | None = None) -> dict[str, object]:
    _private(request)
    cwd = workspace or os.getcwd()
    cid = connection_id

    if session_id and (not workspace or not cid):
        try:
            sess = request.app.state.store.find_session_by_id(session_id)
            if sess:
                if not workspace and sess.get("workspace"):
                    cwd = sess["workspace"]
                if not cid and sess.get("connection_id") and sess["connection_id"] != "local":
                    cid = sess["connection_id"]
        except Exception:
            pass

    # 1. 查询当前分支及所有本地分支
    rc, stdout, stderr = _run_git(["branch", "-a", "--no-color"], cwd=cwd, connection_id=cid, app_state=request.app.state)
    current_branch = "master"
    branches = []
    if rc == 0 and stdout:
        for line in stdout.splitlines():
            clean = line.strip()
            if not clean:
                continue
            if " -> " in clean:
                clean = clean.split(" -> ")[-1].strip()
            if clean.startswith("*"):
                name = clean[1:].strip().replace("(HEAD detached at ", "").replace(")", "")
                current_branch = name
                branches.append(name)
            else:
                branches.append(clean)
    branches = list(dict.fromkeys(branches))

    # 2. 检查工作区是否有修改 / 未暂存 / 未跟踪变更
    rc_status, stdout_status, _ = _run_git(["status", "--porcelain=v1"], cwd=cwd, connection_id=cid, app_state=request.app.state)
    staged_count = 0
    unstaged_count = 0
    untracked_count = 0
    changed_files = []

    if rc_status == 0 and stdout_status:
        for line in stdout_status.splitlines():
            if len(line) < 3:
                continue
            x = line[0]
            y = line[1]
            raw_file = line[3:].strip()
            if " -> " in raw_file:
                raw_file = raw_file.split(" -> ")[-1].strip()
            file_path = raw_file.strip('"')

            if x == "?":
                untracked_count += 1
                status = "untracked"
            elif x in ("M", "A", "D", "R", "C"):
                staged_count += 1
                status = "staged"
            else:
                unstaged_count += 1
                status = "modified"

            changed_files.append({
                "path": file_path,
                "status": status,
                "staged": x in ("M", "A", "D", "R", "C"),
            })

    # 3. 统计超前与落后 commit 数 (ahead / behind)
    ahead = 0
    behind = 0
    target_base = base_branch or f"origin/{current_branch}"
    rc_rev, stdout_rev, _ = _run_git(
        ["rev-list", "--left-right", "--count", f"{target_base}...{current_branch}"],
        cwd=cwd,
        connection_id=cid,
        app_state=request.app.state,
    )
    if rc_rev == 0 and stdout_rev.strip():
        parts = stdout_rev.strip().split()
        if len(parts) >= 2:
            try:
                behind = int(parts[0])
                ahead = int(parts[1])
            except ValueError:
                pass

    return {
        "status": "ok",
        "current_branch": current_branch,
        "branches": branches,
        "ahead": ahead,
        "behind": behind,
        "staged_count": staged_count,
        "unstaged_count": unstaged_count,
        "untracked_count": untracked_count,
        "clean": len(changed_files) == 0,
        "files": changed_files,
    }


@router.post("/api/v1/git/branch")
async def switch_or_create_branch(request: Request) -> dict[str, object]:
    _private(request)
    try:
        payload = await request.json()
    except Exception:
        raise HTTPException(status_code=400, detail="Invalid JSON")
    branch_name = str(payload.get("branch") or "").strip()
    create_new = bool(payload.get("create", False))
    workspace = payload.get("workspace")
    session_id = payload.get("session_id")
    connection_id = payload.get("connection_id")

    if not branch_name:
        raise HTTPException(status_code=400, detail="branch name is required")

    cwd = workspace or os.getcwd()
    cid = connection_id
    if session_id and (not workspace or not cid):
        try:
            sess = request.app.state.store.find_session_by_id(session_id)
            if sess:
                if not workspace and sess.get("workspace"):
                    cwd = sess["workspace"]
                if not cid and sess.get("connection_id") and sess["connection_id"] != "local":
                    cid = sess["connection_id"]
        except Exception:
            pass

    args = ["checkout", "-b", branch_name] if create_new else ["checkout", branch_name]
    rc, stdout, stderr = _run_git(args, cwd=cwd, connection_id=cid, app_state=request.app.state)
    if rc != 0:
        detail = stderr.strip() or stdout.strip() or f"Git checkout exited with code {rc}"
        raise HTTPException(status_code=400, detail=detail)

    return {"status": "ok", "current_branch": branch_name}


@router.get("/api/v1/git/diff-raw")
def get_git_diff_raw(request: Request, path: str | None = None, base_branch: str | None = None, workspace: str | None = None, session_id: str | None = None, connection_id: str | None = None) -> Response:
    _private(request)
    cwd = workspace or os.getcwd()
    cid = connection_id

    if session_id and (not workspace or not cid):
        try:
            sess = request.app.state.store.find_session_by_id(session_id)
            if sess:
                if not workspace and sess.get("workspace"):
                    cwd = sess["workspace"]
                if not cid and sess.get("connection_id") and sess["connection_id"] != "local":
                    cid = sess["connection_id"]
        except Exception:
            pass

    if base_branch:
        args = ["diff", f"{base_branch}...HEAD"]
        if path:
            args.extend(["--", path])
        rc, stdout, _ = _run_git(args, cwd=cwd, connection_id=cid, app_state=request.app.state)
        return Response(content=stdout or "No changes detected.", media_type="text/plain; charset=utf-8")

    args = ["diff"]
    if path:
        args.extend(["--", path])

    rc, stdout, _ = _run_git(args, cwd=cwd, connection_id=cid, app_state=request.app.state)
    if not stdout.strip():
        args_cached = ["diff", "--cached"]
        if path:
            args_cached.extend(["--", path])
        rc_c, stdout_c, _ = _run_git(args_cached, cwd=cwd, connection_id=cid, app_state=request.app.state)
        if stdout_c.strip():
            stdout = stdout_c

    if not stdout.strip() and path:
        args_untracked = ["status", "--porcelain", "--", path]
        rc_u, stdout_u, _ = _run_git(args_untracked, cwd=cwd, connection_id=cid, app_state=request.app.state)
        if stdout_u.strip().startswith("??"):
            full_path = os.path.join(cwd, path)
            try:
                content = ""
                if os.path.isfile(full_path):
                    with open(full_path, "r", encoding="utf-8", errors="replace") as f:
                        content = f.read()
                lines = content.splitlines()
                simulated = [
                    f"diff --git a/{path} b/{path}",
                    "new file mode 100644",
                    "--- /dev/null",
                    f"+++ b/{path}",
                    f"@@ -0,0 +1,{len(lines)} @@",
                ] + [f"+{line}" for line in lines]
                stdout = "\n".join(simulated)
            except Exception:
                pass

    return Response(content=stdout or "No changes detected.", media_type="text/plain; charset=utf-8")
