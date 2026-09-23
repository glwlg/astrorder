from __future__ import annotations

import asyncio
from datetime import datetime
import json
import logging
import os
import sqlite3
import subprocess
from pathlib import Path
from typing import Any
from uuid import uuid4

from fastapi import APIRouter, File, Form, HTTPException, Query, Request, UploadFile
from fastapi.responses import FileResponse, JSONResponse, Response
from pydantic import BaseModel, Field

from .attachments import AttachmentError
from .agent_gateway import AgentApiError, AgentContext, CAPABILITIES, invoke
from .auth import COOKIE_NAME, browser_authenticated, require_agent, require_browser, validate_origin
from .connections import ConnectionError, _windows_hide_startupinfo
from .daemon.bridge import DaemonBridgeError
from .handoff import HANDOFF_CONTEXT_MESSAGE_ID, SUMMARY_PROMPT, build_handoff_prompt
from .schemas import AuthRequest, CommandSubmission, RuntimeLaunch, SshConnectionSettings
from .service import CommandRejected
from .workspace_preferences import router as preferences_router
from .routers.agents import router as agents_router
from .routers.auth import router as auth_router
from .routers.blackboard import router as blackboard_router
from .routers.browser import router as browser_router
from .routers.system import router as system_router
from .routers.attachments import router as attachments_router
from .routers.commands import router as commands_router
from .routers.files import router as files_router
from .routers.git import router as git_router

router = APIRouter()
router.include_router(preferences_router)
router.include_router(agents_router)
router.include_router(auth_router)
router.include_router(blackboard_router)
router.include_router(browser_router)
router.include_router(system_router)
router.include_router(attachments_router)
router.include_router(commands_router)
router.include_router(files_router)
router.include_router(git_router)
logger = logging.getLogger(__name__)


class AgentInvokeRequest(BaseModel):
    capability: str = Field(min_length=1, max_length=128)
    input: dict[str, object] = Field(default_factory=dict)


class QueuedCommandRef(BaseModel):
    agent_id: str = Field(min_length=1, max_length=256)
    session_id: str = Field(min_length=1, max_length=256)


class QueuedCommandEdit(QueuedCommandRef):
    text: str = Field(min_length=1, max_length=200_000)


class BrowserNavigateRequest(BaseModel):
    agent_id: str = Field(min_length=1, max_length=256)
    session_id: str = Field(min_length=1, max_length=256)
    url: str = Field(min_length=1, max_length=8_192)


class BrowserTabRequest(BaseModel):
    agent_id: str = Field(min_length=1, max_length=256)
    session_id: str = Field(min_length=1, max_length=256)
    target_id: str | None = Field(default=None, max_length=256)
    url: str | None = Field(default=None, max_length=8_192)


class BrowserInteractRequest(BaseModel):
    agent_id: str = Field(min_length=1, max_length=256)
    session_id: str = Field(min_length=1, max_length=256)
    action: str = Field(pattern="^(click|wheel|text|back|forward)$")
    target_id: str | None = Field(default=None, max_length=256)
    x: float | None = None
    y: float | None = None
    ratio_x: float | None = None
    ratio_y: float | None = None
    delta_y: float | None = None
    text: str | None = None


def _settings(request: Request):
    return request.app.state.settings


def _private(request: Request) -> None:
    require_browser(request, _settings(request))


def _agent_context(request: Request) -> AgentContext:
    def read_messages(agent_id: str, session_id: str, before: str | None, limit: int) -> dict:
        runtime = _agent_runtime(request, agent_id)
        reader = getattr(runtime, "messages", None)
        if callable(reader):
            try:
                page = reader(session_id, before, limit)
                if isinstance(page, dict):
                    return page
            except (ConnectionError, ValueError, TypeError, OSError):
                pass
        items, cursor = request.app.state.store.list_messages(agent_id, session_id, before, limit)
        return {"items": items, "next": cursor}

    return AgentContext(
        store=request.app.state.store,
        read_messages=read_messages,
        service=getattr(request.app.state, "service", None),
        runtime_resolver=lambda aid: _agent_runtime(request, aid),
    )


def _codex(request: Request, agent_id: str):
    environments = getattr(request.app.state, 'environments', None)
    if environments:
        return environments.for_agent(agent_id)
    connection = getattr(request.app.state, 'codex', None)
    return connection if connection and connection.agent_id == agent_id else None


def _agent_runtime(request: Request, agent_id: str):
    environments = getattr(request.app.state, "environments", None)
    resolver = getattr(environments, "runtime_for_agent", None)
    if callable(resolver):
        runtime = resolver(agent_id)
        if runtime is not None:
            return runtime
    connections = getattr(request.app.state, "connections", None)
    resolver = getattr(connections, "get_runtime_by_agent_id", None)
    runtime = resolver(agent_id) if callable(resolver) else None
    return runtime if runtime is not None else _codex(request, agent_id)


def _mutate_agent_session(request: Request, agent_id: str, session_id: str, updates):
    runtime = _agent_runtime(request, agent_id)
    for name in ("mutate", "mutate_session"):
        mutate = getattr(runtime, name, None)
        if callable(mutate):
            return mutate(session_id, updates)
    return request.app.state.connections.mutate_session_for_agent(agent_id, session_id, updates)


def _local_hermes_database(runtime):
    plugins = getattr(runtime, '_profile_plugins_dir', None)
    if plugins is not None:
        return Path(plugins).parent / 'state.db'
    local = Path.home() / 'AppData' / 'Local' / 'hermes' / 'state.db'
    if local.is_file():
        return local
    return Path.home() / '.hermes' / 'state.db'


def _collect_presence(request: Request) -> dict[str, list]:
    from .native_user_activity import presence
    controller = request.app.state.connections
    store = request.app.state.store
    items: list[dict[str, str]] = []
    open_rows: list[dict[str, str]] = []
    live_rows: list[dict[str, str]] = []
    sources: list[tuple[str, object, bool]] = []
    local_id = controller.local.snapshot().get('agent_id')
    if isinstance(local_id, str) and local_id:
        sources.append((local_id, controller.local, True))
    for runtime in list(getattr(controller, '_ssh_runtimes', {}).values()):
        agent_id = getattr(runtime, 'agent_id', None)
        if isinstance(agent_id, str) and agent_id:
            sources.append((agent_id, runtime, False))
    for agent_id, runtime, is_local in sources:
        ids = [row['id'] for row in store.list_sessions(agent_id)]
        try:
            if is_local:
                data = presence(_local_hermes_database(runtime), ids)
            elif hasattr(runtime, 'read_presence'):
                data = runtime.read_presence(ids)
            else:
                data = {'items': runtime.read_user_activity(ids), 'open_ids': [], 'live_ids': []}
        except (OSError, ValueError, sqlite3.Error, ConnectionError, subprocess.TimeoutExpired, AttributeError, TypeError):
            continue
        known = set(ids)
        for row in data.get('items') or []:
            if isinstance(row, dict) and row.get('id') in known:
                items.append({'agent_id': agent_id, 'id': row['id'], 'last_user_at': row['last_user_at']})
        for sid in data.get('open_ids') or []:
            if sid in known:
                open_rows.append({'agent_id': agent_id, 'id': sid})
        for sid in data.get('live_ids') or []:
            if sid in known:
                live_rows.append({'agent_id': agent_id, 'id': sid})
    return {'items': items, 'open': open_rows, 'live': live_rows}


@router.get("/api/v1/bootstrap")
def bootstrap(request: Request) -> dict[str, object]:
    _private(request)
    from .agent_registry import current_agents
    store = request.app.state.store
    sessions = store.sessions_for_bootstrap()
    return {
        "protocol_version": 1,
        "agents": [request.app.state.service.effective_agent(a) for a in current_agents(store)],
        "projects": store.list_projects(),
        "sessions": sessions,
        "cursor": store.latest_cursor(),
        "approvals": request.app.state.service.approval_snapshot(),
    }


@router.get("/api/v1/agent/catalog")
def agent_catalog(request: Request) -> dict[str, object]:
    require_agent(request, _settings(request))
    return {"items": CAPABILITIES}


@router.post("/api/v1/agent/invoke")
def agent_invoke(request: Request, body: AgentInvokeRequest) -> dict[str, object]:
    require_agent(request, _settings(request))
    payload_dict = dict(body.input)
    client_agent_id = request.headers.get("x-astrorder-agent-id")
    if client_agent_id and "caller_agent_id" not in payload_dict:
        payload_dict["caller_agent_id"] = client_agent_id
    try:
        return invoke(body.capability, payload_dict, _agent_context(request))
    except AgentApiError as exc:
        status = 404 if exc.code == "not_found" else 400
        raise HTTPException(status_code=status, detail=exc.message) from None


@router.post("/api/v1/agent/mcp")
async def agent_mcp(request: Request) -> Response:
    require_agent(request, _settings(request))
    client_agent_id = request.headers.get("x-astrorder-agent-id")
    if client_agent_id:
        from .models import WorkspacePreferenceRow
        with request.app.state.store.session() as db:
            row = db.get(WorkspacePreferenceRow, f"agent_mcp_enabled:{client_agent_id}")
            if row and row.value is False:
                raise HTTPException(status_code=403, detail=f"Astrorder MCP has been disabled for agent '{client_agent_id}'.")
    from .agent_mcp import handle_rpc

    try:
        body = await request.json()
    except Exception:
        raise HTTPException(status_code=400, detail="Invalid JSON") from None
    ctx = _agent_context(request)

    def invoker(capability: str, payload: dict) -> dict:
        if client_agent_id and "caller_agent_id" not in payload:
            payload["caller_agent_id"] = client_agent_id
        return invoke(capability, payload, ctx)

    if isinstance(body, list):
        replies = await asyncio.to_thread(
            lambda: [handle_rpc(item, invoker) for item in body if isinstance(item, dict)]
        )
        return JSONResponse([item for item in replies if item is not None])
    if not isinstance(body, dict):
        raise HTTPException(status_code=400, detail="Invalid JSON-RPC")
    reply = await asyncio.to_thread(handle_rpc, body, invoker)
    if reply is None:
        return Response(status_code=202)
    return JSONResponse(reply)


@router.get("/api/v1/presence")
async def presence(request: Request) -> dict[str, list]:
    _private(request)
    return await asyncio.to_thread(_collect_presence, request)


@router.get('/api/v1/user-activity')
async def user_activity(request: Request):
    _private(request)
    data = await asyncio.to_thread(_collect_presence, request)
    return {'items': data['items'], 'live': data['live']}


@router.get("/api/v1/sessions")
def sessions(request: Request, agent_id: str | None = None) -> dict[str, object]:
    _private(request)
    return {"items": request.app.state.store.list_sessions(agent_id)}


@router.post("/api/v1/sessions/{session_id}/sync")
async def sync_session(
    session_id: str,
    request: Request,
    agent_id: str = Query(..., min_length=1, max_length=256),
) -> dict[str, object]:
    _private(request)
    session = request.app.state.store.get_session(agent_id, session_id)
    if session is None:
        raise HTTPException(status_code=404, detail="Session was not found")
    bridge = request.app.state.daemon_bridge
    if bridge is not None and session.get("control_state") == "owned":
        try:
            await bridge.refresh_status()
        except DaemonBridgeError as exc:
            raise HTTPException(status_code=503, detail=str(exc)) from None
    latest_session = request.app.state.store.get_session(agent_id, session_id) or session
    if latest_session.get("status") == "running":
        active_cmds = [
            c for c in request.app.state.store.list_commands(agent_id, session_id)
            if c.get("state") in {"running", "accepted"}
        ]
        if not active_cmds:
            runtime = _agent_runtime(request, agent_id)
            active_turn = getattr(runtime, "_active", {}).get(session_id) if runtime else None
            if not active_turn:
                updated = request.app.state.store.update_session(agent_id, session_id, {"status": "idle"})
                if updated is not None:
                    request.app.state.service._server_event(
                        "session.upsert",
                        agent_id=agent_id,
                        session_id=session_id,
                        data=updated,
                    )
                    latest_session = updated
    return latest_session


class CreateSessionPayload(BaseModel):
    agent_id: str = Field(min_length=1, max_length=256)
    workspace: str | None = Field(default=None, max_length=2000)
    title: str | None = Field(default=None, max_length=512)
    project_id: str | None = Field(default=None, max_length=256)
    project_name: str | None = Field(default=None, max_length=256)
    parent_session_id: str | None = Field(default=None, max_length=256)
    ephemeral: bool = Field(default=False)
    provider: str | None = Field(default=None, max_length=256)
    model: str | None = Field(default=None, max_length=256)
    effort: str | None = Field(default=None, max_length=32)
    blackboard_scope: str = Field(default="session", pattern="^(session|swarm|group)$")
    blackboard_scope_id: str | None = Field(default=None, max_length=256)


@router.get("/api/v1/open-sessions")
async def open_sessions(request: Request) -> dict[str, object]:
    _private(request)
    from .native_controls import open_native_session_ids, runtime_rpc
    from .jev_client import get_jev_key, evaluate_session_swipe_worthiness
    store = request.app.state.store

    def read(agent_id):
        try:
            runtime = _agent_runtime(request, agent_id)
            read_open_ids = getattr(runtime, "open_ids", None)
            if callable(read_open_ids):
                ids = read_open_ids()
            elif getattr(runtime, "daemon_owned", False):
                return agent_id, None
            else:
                ids = open_native_session_ids(runtime_rpc(request.app.state.connections, agent_id))
            return agent_id, ids
        except (ConnectionError, OSError, TimeoutError, RuntimeError):
            return agent_id, None

    results = await asyncio.gather(
        *(asyncio.to_thread(read, agent['id']) for agent in store.list_agents())
    )
    known = [agent_id for agent_id, ids in results if ids is not None]
    items = {}
    for agent_id, ids in results:
        if ids is None:
            continue
        for session_id in ids:
            items[(agent_id, session_id)] = {'agent_id': agent_id, 'id': session_id}

    # Jev 智能移动端滑动判定层：
    jev_key = get_jev_key(store)
    if jev_key:
        try:
            recent_candidates = store.list_sessions()[:12]
            for s in recent_candidates:
                aid = s.get("agent_id")
                sid = s.get("id")
                if not aid or not sid:
                    continue
                if s.get("status") in {"running", "waiting_approval"}:
                    items[(aid, sid)] = {'agent_id': aid, 'id': sid}
                    continue
                if (aid, sid) in items:
                    continue
                up_at = s.get("updated_at")
                if not up_at:
                    continue
                try:
                    from datetime import datetime, timezone
                    diff_sec = (datetime.now(timezone.utc) - datetime.fromisoformat(up_at.replace('Z', '+00:00'))).total_seconds()
                    if diff_sec > 3600:
                        continue
                except Exception:
                    continue

                msgs = store.list_messages(sid, limit=5)
                eval_res = await asyncio.to_thread(evaluate_session_swipe_worthiness, s, msgs or [], store=store)
                if eval_res.get("worthy") is True:
                    items[(aid, sid)] = {'agent_id': aid, 'id': sid}
        except Exception as e:
            logger.warning("Jev open_sessions evaluation skipped: %s", e)

    return {
        'known_agent_ids': list(dict.fromkeys(known)),
        'items': list(items.values()),
        'live': [],
    }


@router.post("/api/v1/sessions")
def create_session(payload: CreateSessionPayload, request: Request) -> dict[str, object]:
    _private(request)
    # 自动解析默认工作区，防止未显式提供 workspace 时落入 server 目录
    if not (payload.workspace or "").strip():
        store = request.app.state.store
        ws = None
        for s in store.list_sessions()[:30]:
            sws = (s.get("workspace") or "").strip()
            if sws and not sws.lower().endswith("server") and "devapp" not in sws.lower():
                ws = sws
                break
        if not ws:
            default_p = Path("P:/workspace/glwlg/ai/astrorder")
            ws = str(default_p) if default_p.is_dir() else str(Path.home())
        payload.workspace = ws

    try:
        runtime = _agent_runtime(request, payload.agent_id)
        create = getattr(runtime, "create", None)
        if callable(create):
            data = create(
                payload.workspace,
                payload.title,
                ephemeral=payload.ephemeral,
                parent_session_id=payload.parent_session_id,
            )
        else:
            if runtime is None:
                raise ConnectionError("会话所属运行时未连接；不会转到其他 Agent 创建。", 503)
            if payload.parent_session_id:
                branch = getattr(runtime, "branch_session", None)
                if not callable(branch):
                    raise ConnectionError("会话所属运行时不支持原生分叉。", 409)
                data = branch(payload.parent_session_id, payload.title)
            else:
                create = getattr(runtime, "create_session", None)
                if not callable(create):
                    data = request.app.state.connections.create_session_for_agent(
                        agent_id=payload.agent_id,
                        workspace=payload.workspace,
                        title=payload.title,
                        provider=payload.provider,
                        model=payload.model,
                        effort=payload.effort,
                    )
                else:
                    data = create(
                        workspace=payload.workspace,
                        title=payload.title,
                        provider=payload.provider,
                        model=payload.model,
                        effort=payload.effort,
                    )
        if payload.ephemeral:
            data["ephemeral"] = True
        # 关联 project_id 与 project_name
        store = request.app.state.store
        project_id = payload.project_id
        project_name = payload.project_name
        # 如果未显式传 project_id，根据 workspace 或 agent_id 在现有 projects 表中寻找最佳匹配
        if not project_id:
            projects = store.list_projects()
            ws = (payload.workspace or data.get("workspace") or "").strip().replace("\\", "/").rstrip("/").lower()
            for p in projects:
                if p.get('agent_id') != payload.agent_id:
                    continue
                p_ws = (p.get("workspace") or "").strip().replace("\\", "/").rstrip("/").lower()
                if ws and p_ws and ws == p_ws:
                    project_id = p.get("project_id")
                    project_name = project_name or p.get("project_name")
                    break
        if project_id:
            data["project_id"] = project_id
        if project_name:
            data["project_name"] = project_name

        canonical = request.app.state.store.upsert_session(data)
        session_key = f"{canonical['agent_id']}::{canonical['id']}"
        if payload.blackboard_scope == "group":
            if not payload.blackboard_scope_id:
                raise ValueError("blackboard_scope_id is required for group sessions")
            blackboard_namespace = f"group:{payload.blackboard_scope_id}"
        elif payload.blackboard_scope == "swarm":
            blackboard_namespace = f"swarm:{payload.blackboard_scope_id or session_key}"
        else:
            blackboard_namespace = f"session:{session_key}"
        request.app.state.store.set_session_blackboard_namespace(
            canonical["agent_id"], canonical["id"], blackboard_namespace
        )
        request.app.state.service._server_event(
            "session.upsert",
            agent_id=canonical["agent_id"],
            session_id=canonical["id"],
            data=canonical,
        )
        return canonical
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None
    except Exception as exc:
        logger.exception("Failed to create session")
        raise HTTPException(status_code=500, detail=str(exc)) from exc


class ForkSessionPayload(BaseModel):
    agent_id: str = Field(min_length=1, max_length=256)
    title: str | None = Field(default=None, max_length=512)
    worktree: bool = Field(default=False)
    branch_name: str | None = Field(default=None, max_length=256)
    worktree_path: str | None = Field(default=None, max_length=2000)
    target_message_id: str | None = Field(default=None, max_length=256)
    turn_index: int | None = Field(default=None)


def _create_local_git_worktree(
    workspace: str, branch_name: str | None, worktree_path: str | None
) -> tuple[str, Path]:
    ws_path = Path(workspace).expanduser().resolve()
    if not ws_path.is_dir():
        raise HTTPException(status_code=400, detail="工作区目录不存在，无法创建工作树分支。")
    try:
        proc = subprocess.run(
            ["git", "-C", str(ws_path), "rev-parse", "--show-toplevel"],
            capture_output=True,
            text=True,
            check=False,
            encoding="utf-8",
        )
    except Exception as exc:
        raise HTTPException(status_code=400, detail=f"检查 Git 仓库失败: {exc}") from exc
    if proc.returncode != 0:
        raise HTTPException(status_code=400, detail="当前工作区不是 Git 仓库，无法创建工作树分支。")

    repo_root = Path(proc.stdout.strip()).resolve()
    branch = (branch_name or "").strip()
    if not branch:
        branch = f"branch-{datetime.now().strftime('%Y%m%d-%H%M%S')}"

    if worktree_path and worktree_path.strip():
        target_path = Path(worktree_path.strip()).expanduser().resolve()
    else:
        target_path = (repo_root.parent / f"{repo_root.name}-worktrees" / branch).resolve()

    target_path.parent.mkdir(parents=True, exist_ok=True)

    add_proc = subprocess.run(
        ["git", "-C", str(repo_root), "worktree", "add", "-b", branch, str(target_path)],
        capture_output=True,
        text=True,
        check=False,
        encoding="utf-8",
    )
    if add_proc.returncode != 0:
        err = add_proc.stderr.strip() or add_proc.stdout.strip()
        if "already exists" in err.lower():
            retry_proc = subprocess.run(
                ["git", "-C", str(repo_root), "worktree", "add", str(target_path), branch],
                capture_output=True,
                text=True,
                check=False,
                encoding="utf-8",
            )
            if retry_proc.returncode != 0:
                retry_err = retry_proc.stderr.strip() or retry_proc.stdout.strip()
                raise HTTPException(status_code=400, detail=f"创建 Git 工作树失败: {retry_err}")
        else:
            raise HTTPException(status_code=400, detail=f"创建 Git 工作树失败: {err}")

    return branch, target_path


def _create_remote_git_worktree(
    request: Request, connection_id: str, workspace: str, branch_name: str | None, worktree_path: str | None
) -> tuple[str, str]:
    ssh_conn = request.app.state.store.get_ssh_connection(connection_id)
    if not ssh_conn:
        raise HTTPException(status_code=404, detail="SSH 连接不存在")
    from .ssh_transport import SshNativeRuntime, build_remote_python_command
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
import subprocess, sys, os
from pathlib import Path
from datetime import datetime

ws = {json.dumps(workspace)}
branch = {json.dumps(branch_name or "")}.strip() or f"branch-{datetime.now().strftime('%Y%m%d-%H%M%S')}"
custom_path = {json.dumps(worktree_path or "")}.strip()

try:
    proc = subprocess.run(["git", "-C", ws, "rev-parse", "--show-toplevel"], capture_output=True, text=True, check=False)
    if proc.returncode != 0:
        print("ERROR:NOT_GIT_REPO:" + (proc.stderr.strip() or "not a git repo"))
        sys.exit(1)
    repo_root = Path(proc.stdout.strip())
    target = Path(custom_path) if custom_path else (repo_root.parent / f"{repo_root.name}-worktrees" / branch)
    target.parent.mkdir(parents=True, exist_ok=True)
    add_proc = subprocess.run(["git", "-C", str(repo_root), "worktree", "add", "-b", branch, str(target)], capture_output=True, text=True, check=False)
    if add_proc.returncode != 0:
        err = add_proc.stderr.strip()
        if "already exists" in err.lower():
            retry = subprocess.run(["git", "-C", str(repo_root), "worktree", "add", str(target), branch], capture_output=True, text=True, check=False)
            if retry.returncode != 0:
                print("ERROR:WORKTREE_FAILED:" + retry.stderr.strip())
                sys.exit(1)
        else:
            print("ERROR:WORKTREE_FAILED:" + err)
            sys.exit(1)
    print("SUCCESS:" + branch + "\t" + str(target))
except Exception as e:
    print("ERROR:" + str(e))
    sys.exit(1)
"""
    cmd = build_remote_python_command(remote_script)
    full_argv = argv + [cmd]
    res = subprocess.run(full_argv, capture_output=True, text=True, timeout=15)
    out = res.stdout.strip()
    if res.returncode != 0 or not out.startswith("SUCCESS:"):
        err = out.replace("ERROR:", "").strip() or res.stderr.strip()
        raise HTTPException(status_code=400, detail=f"远端创建 Git 工作树失败: {err}")
    _, data = out.split("SUCCESS:", 1)
    b_name, w_path = data.split("	", 1)
    return b_name.strip(), w_path.strip()


@router.post("/api/v1/sessions/{session_id}/fork")
def fork_session(session_id: str, payload: ForkSessionPayload, request: Request) -> dict[str, object]:
    _private(request)
    store = request.app.state.store
    source = store.get_session(payload.agent_id, session_id)
    if source is None:
        raise HTTPException(status_code=404, detail="原会话不存在。")

    workspace = source.get("workspace")
    branch_name = None
    if payload.worktree:
        if not workspace:
            raise HTTPException(status_code=400, detail="当前会话没有关联工作区，无法创建工作树分支。")
        agent = store.get_agent(payload.agent_id)
        connection_id = agent.get("connection_id") if agent else None
        if connection_id and connection_id != "local":
            branch_name, workspace = _create_remote_git_worktree(
                request, connection_id, workspace, payload.branch_name, payload.worktree_path
            )
        else:
            branch_name, target_path = _create_local_git_worktree(
                workspace, payload.branch_name, payload.worktree_path
            )
            workspace = str(target_path)

    default_title = (
        f"{source.get('title') or '新会话'} ({branch_name})"
        if branch_name
        else (f"{source.get('title') or '新会话'} (分叉)")
    )
    title = payload.title.strip() if payload.title and payload.title.strip() else default_title

    # 1. 尝试调用底层运行时的精准截断分叉 (如 Codex thread/fork, Hermes session.branch, Grok 截断)
    try:
        runtime = _agent_runtime(request, payload.agent_id)
        fork_fn = getattr(runtime, "fork_session", None)
        if callable(fork_fn):
            forked = fork_fn(
                session_id=session_id,
                title=title,
                workspace=workspace,
                target_message_id=payload.target_message_id,
                turn_index=payload.turn_index,
            )
            if isinstance(forked, dict) and forked.get("id"):
                return forked
    except Exception:
        pass

    create_payload = CreateSessionPayload(
        agent_id=payload.agent_id,
        workspace=workspace,
        title=title,
        project_id=source.get("project_id"),
        project_name=source.get("project_name"),
        parent_session_id=session_id,
    )
    return create_session(create_payload, request)



class HandoffSessionPayload(BaseModel):
    operation_id: str = Field(min_length=1, max_length=256)
    source_agent_id: str = Field(min_length=1, max_length=256)
    target_agent_id: str = Field(min_length=1, max_length=256)
    provider: str | None = Field(default=None, min_length=1, max_length=160)
    model: str | None = Field(default=None, min_length=1, max_length=160)
    effort: str | None = Field(default=None, min_length=1, max_length=32)


async def _handoff_messages(
    request: Request, agent: dict[str, object], session: dict[str, object]
) -> list[dict[str, object]]:
    runtime = _agent_runtime(request, agent["id"])
    reader = getattr(runtime, "messages", None)
    if callable(reader):
        page = await asyncio.to_thread(reader, session["id"], None, 100)
        items = page.get("items") if isinstance(page, dict) else None
    else:
        reader = getattr(runtime, "load_native_history_page", None)
        if not callable(reader):
            raise ConnectionError("源 Agent 暂不支持读取分叉摘要。", 503)
        page = await asyncio.to_thread(reader, session["id"], None, 100)
        raw_items = page.get("items") if isinstance(page, dict) else None
        if not isinstance(raw_items, list):
            raise ConnectionError("源 Agent 返回了无效的分叉消息。", 502)
        from .native_sessions import project_history_messages

        items = project_history_messages(
            raw_items,
            durable_session_id=session["id"],
            native_session_id=session["id"],
            source_id=session.get("source_id") or agent["id"],
            agent_id=agent["id"],
        )
    if not isinstance(items, list):
        raise ConnectionError("源 Agent 返回了无效的分叉消息。", 502)
    return items


async def _wait_handoff_summary(
    request: Request, agent_id: str, session_id: str, command_id: str, cancel_event: asyncio.Event
) -> None:
    deadline = asyncio.get_running_loop().time() + 300
    while True:
        if cancel_event.is_set():
            raise ConnectionError("转交已取消。", 409)
        command = request.app.state.store.get_command(agent_id, session_id, command_id)
        state = command.get("state") if command else None
        if state == "completed":
            return
        if state in {"failed", "cancelled", "unknown"}:
            raise ConnectionError(
                (command or {}).get("error") or "源 Agent 未能完成交接摘要。", 502
            )
        if asyncio.get_running_loop().time() >= deadline:
            raise ConnectionError("等待源 Agent 生成交接摘要超时。", 504)
        await asyncio.sleep(0.1)


async def _wait_handoff_result(
    request: Request,
    source_agent: dict[str, object],
    fork: dict[str, object],
    known_assistant_ids: set[str],
    cancel_event: asyncio.Event,
) -> str:
    deadline = asyncio.get_running_loop().time() + 10
    while True:
        if cancel_event.is_set():
            raise ConnectionError("转交已取消。", 409)
        messages = await _handoff_messages(request, source_agent, fork)
        summaries = [
            item["text"].strip()
            for item in messages
            if item.get("role") == "assistant"
            and item.get("kind") == "message"
            and item.get("id") not in known_assistant_ids
            and isinstance(item.get("text"), str)
            and item["text"].strip()
        ]
        if summaries:
            return summaries[-1]
        if asyncio.get_running_loop().time() >= deadline:
            raise ConnectionError("源 Agent 已完成总结，但未返回可用的交接摘要。", 502)
        await asyncio.sleep(0.2)


def _delete_handoff_fork(request: Request, agent_id: str, session_id: str) -> None:
    _mutate_agent_session(request, agent_id, session_id, None)
    if not request.app.state.service.delete_session(agent_id, session_id):
        raise ConnectionError("临时分叉已从原生运行时删除，但本地记录删除失败。", 500)


async def _summarize_handoff(
    request: Request,
    source_agent: dict[str, object],
    source: dict[str, object],
    cancel_event: asyncio.Event,
) -> str:
    if cancel_event.is_set():
        raise ConnectionError("转交已取消。", 409)

    # 1. 优先尝试 Jev 快速提炼交接包（秒级响应、节省大模型 Token）
    from .jev_client import get_jev_key, curate_handoff_summary
    if get_jev_key(request.app.state.store):
        try:
            source_msgs = request.app.state.store.list_messages(source["id"], limit=20)
            curated = curate_handoff_summary(source_msgs or [], source, store=request.app.state.store)
            if curated.get("ok") and curated.get("summary"):
                logger.info("Handoff summary curated via Jev model successfully.")
                return curated["summary"]
        except Exception as e:
            logger.warning("Jev handoff curation fallback to standard fork summary: %s", e)

    fork = await asyncio.to_thread(
        create_session,
        CreateSessionPayload(
            agent_id=source_agent["id"],
            workspace=source.get("workspace"),
            title="转交摘要",
            parent_session_id=source["id"],
            ephemeral=True,
        ),
        request,
    )
    command_id: str | None = None
    try:
        before = await _handoff_messages(request, source_agent, fork)
        known_assistant_ids = {
            item.get("id")
            for item in before
            if item.get("role") == "assistant" and isinstance(item.get("id"), str)
        }
        command_id = f"handoff-summary-{uuid4().hex}"
        await request.app.state.service.submit_browser_command(
            {
                "id": command_id,
                "agent_id": source_agent["id"],
                "session_id": fork["id"],
                "action": "send",
                "text": SUMMARY_PROMPT,
                "attachment_ids": [],
                "target_id": None,
            }
        )
        await _wait_handoff_summary(
            request, source_agent["id"], fork["id"], command_id, cancel_event
        )
        return await _wait_handoff_result(
            request, source_agent, fork, known_assistant_ids, cancel_event
        )
    except ConnectionError:
        if cancel_event.is_set() and command_id:
            try:
                await request.app.state.service.submit_browser_command(
                    {
                        "id": f"handoff-stop-{uuid4().hex}",
                        "agent_id": source_agent["id"],
                        "session_id": fork["id"],
                        "action": "stop",
                        "text": "",
                        "attachment_ids": [],
                        "target_id": fork["id"],
                    }
                )
            except Exception:
                logger.exception("Failed to stop cancelled handoff summary")
        raise
    finally:
        try:
            await asyncio.to_thread(
                _delete_handoff_fork, request, source_agent["id"], fork["id"]
            )
        except Exception as exc:
            raise ConnectionError("临时交接分叉删除失败。", 502) from exc


@router.post("/api/v1/sessions/{session_id}/handoff")
async def handoff_session(
    session_id: str, payload: HandoffSessionPayload, request: Request
) -> dict[str, object]:
    _private(request)
    store = request.app.state.store
    source_agent = store.get_agent(payload.source_agent_id)
    target_agent = store.get_agent(payload.target_agent_id)
    source = store.get_session(payload.source_agent_id, session_id)
    if source_agent is None or target_agent is None or source is None:
        raise HTTPException(status_code=404, detail="源会话或目标 Agent 不存在。")
    if (
        source_agent["kind"] == target_agent["kind"]
        or {source_agent["kind"], target_agent["kind"]} != {"codex", "hermes"}
    ):
        raise HTTPException(status_code=422, detail="转交仅支持 Codex 与 Hermes 之间进行。")
    if source_agent.get("connection_id") != target_agent.get("connection_id"):
        raise HTTPException(status_code=422, detail="转交仅支持在同一台机器的 Agent 之间进行。")
    if target_agent.get("status") != "ready":
        raise HTTPException(status_code=409, detail="目标 Agent 当前未就绪。")
    if bool(payload.provider) != bool(payload.model):
        raise HTTPException(status_code=422, detail="目标模型的 provider 与 model 必须同时提供。")
    bridge = request.app.state.daemon_bridge
    if bridge is not None and source.get("control_state") == "owned":
        try:
            await bridge.refresh_status()
        except DaemonBridgeError as exc:
            raise HTTPException(status_code=503, detail=str(exc)) from None
        source = store.get_session(payload.source_agent_id, session_id) or source
    if source.get("status") != "idle":
        raise HTTPException(status_code=409, detail="源会话仍在运行或等待审批，暂不能转交。")

    try:
        cancel_event = request.app.state.background_tasks.start(payload.operation_id)
    except ValueError:
        raise HTTPException(status_code=409, detail="转交任务 ID 已在使用。")
    target = None
    try:
        summary = await _summarize_handoff(request, source_agent, source, cancel_event)
        if cancel_event.is_set():
            raise ConnectionError("转交已取消。", 409)
        prompt = build_handoff_prompt(
            source,
            "Codex" if source_agent["kind"] == "codex" else "Hermes",
            summary,
        )
        created = await asyncio.to_thread(
            create_session,
            CreateSessionPayload(
                agent_id=payload.target_agent_id,
                workspace=source.get("workspace"),
                title=source.get("title") or "转交会话",
                project_name=source.get("project_name"),
                provider=payload.provider if target_agent["kind"] == "hermes" else None,
                model=payload.model if target_agent["kind"] == "hermes" else None,
                effort=payload.effort if target_agent["kind"] == "hermes" else None,
            ),
            request,
        )
        if cancel_event.is_set():
            raise ConnectionError("转交已取消。", 409)
        target = store.upsert_session(
            {
                **created,
                "handoff_from_agent_id": payload.source_agent_id,
                "handoff_from_session_id": session_id,
            }
        )
        request.app.state.service._server_event(
            "session.upsert",
            agent_id=target["agent_id"],
            session_id=target["id"],
            data=target,
        )
        if payload.provider and payload.model and target_agent["kind"] != "hermes":
            await asyncio.to_thread(
                session_model,
                target["id"],
                SessionModelSelection(
                    agent_id=target["agent_id"], provider=payload.provider, model=payload.model
                ),
                request,
            )
        if payload.effort and target_agent["kind"] != "hermes":
            await asyncio.to_thread(
                session_reasoning,
                target["id"],
                SessionReasoningSelection(agent_id=target["agent_id"], effort=payload.effort),
                request,
            )
        if cancel_event.is_set():
            raise ConnectionError("转交已取消。", 409)
        store.set_session_handoff_context(target["agent_id"], target["id"], prompt)
        context_message = store.upsert_message(
            {
                "id": HANDOFF_CONTEXT_MESSAGE_ID,
                "agent_id": target["agent_id"],
                "session_id": target["id"],
                "role": "system",
                "kind": "message",
                "text": f"交接摘要\n\n{summary}",
                "attachments": [],
                "created_at": target["updated_at"],
                "command_id": None,
                "tool": {"handoff_context": True},
            }
        )
        request.app.state.service._server_event(
            "message.upsert",
            agent_id=target["agent_id"],
            session_id=target["id"],
            data=context_message,
        )
        if target_agent["kind"] == "hermes":
            if payload.provider and payload.model:
                store.set_session_model_binding(
                    target["agent_id"], target["id"], payload.provider, payload.model
                )
            if payload.effort:
                store.set_session_reasoning_binding(
                    target["agent_id"], target["id"], payload.effort
                )
        if cancel_event.is_set():
            raise ConnectionError("转交已取消。", 409)
        return store.get_session(target["agent_id"], target["id"]) or target
    except Exception as exc:
        if target is not None:
            try:
                await asyncio.to_thread(
                    _delete_handoff_fork, request, target["agent_id"], target["id"]
                )
            except Exception as cleanup_exc:
                raise HTTPException(
                    status_code=502, detail="转交失败，且目标会话清理失败。"
                ) from cleanup_exc
        if isinstance(exc, (ConnectionError, CommandRejected)):
            raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None
        raise
    finally:
        request.app.state.background_tasks.finish(payload.operation_id, cancel_event)


@router.post("/api/v1/background-tasks/{operation_id}/cancel")
@router.post("/api/v1/handoffs/{operation_id}/cancel")
async def cancel_handoff(operation_id: str, request: Request) -> dict[str, bool]:
    _private(request)
    return {"cancelled": request.app.state.background_tasks.cancel(operation_id)}


class UpdateSessionPayload(BaseModel):
    agent_id: str = Field(min_length=1, max_length=256)
    title: str | None = Field(default=None, max_length=512)
    status: str | None = Field(default=None, max_length=32)
    workspace: str | None = Field(default=None, max_length=2000)


@router.patch("/api/v1/sessions/{session_id}")
def update_session(session_id: str, payload: UpdateSessionPayload, request: Request) -> dict[str, object]:
    _private(request)
    updates = payload.model_dump(exclude_unset=True)
    agent_id = updates.pop("agent_id")
    if request.app.state.store.get_session(agent_id, session_id) is None:
        raise HTTPException(status_code=404, detail="Session was not found")
    try:
        _mutate_agent_session(request, agent_id, session_id, updates)
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None
    updated = request.app.state.store.update_session(agent_id, session_id, updates)
    if updated is None:
        raise HTTPException(status_code=404, detail="Session was not found")
    request.app.state.service._server_event(
        "session.upsert",
        agent_id=agent_id,
        session_id=session_id,
        data=updated,
    )
    return updated


@router.delete("/api/v1/sessions/{session_id}")
def delete_session(session_id: str, agent_id: str = Query(..., min_length=1), request: Request = None) -> dict[str, object]:
    _private(request)
    if request.app.state.store.get_session(agent_id, session_id) is None:
        raise HTTPException(status_code=404, detail="Session was not found")
    try:
        _mutate_agent_session(request, agent_id, session_id, None)
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None
    request.app.state.service.delete_session(agent_id, session_id)
    return {"ok": True, "id": session_id}


class BatchDeleteSessionsPayload(BaseModel):
    sessions: list[dict[str, str]]


@router.post("/api/v1/sessions/batch-delete")
def batch_delete_sessions_endpoint(
    payload: BatchDeleteSessionsPayload,
    request: Request = None,
) -> dict[str, object]:
    _private(request)
    deleted: list[dict[str, str]] = []
    failed: list[dict[str, str]] = []
    for item in payload.sessions:
        agent_id = str(item.get("agent_id") or "")
        session_id = str(item.get("session_id") or item.get("id") or "")
        if not agent_id or not session_id:
            continue
        if request.app.state.store.get_session(agent_id, session_id) is None:
            continue
        try:
            _mutate_agent_session(request, agent_id, session_id, None)
        except Exception:
            pass
        if agent_id == "local-codex":
            try:
                codex_home = Path.home() / ".codex"
                for sp in codex_home.glob("state_*.sqlite"):
                    with sqlite3.connect(sp) as db:
                        db.execute("DELETE FROM threads WHERE id=?", (session_id,))
                        db.commit()
            except Exception:
                pass
        try:
            success = request.app.state.service.delete_session(agent_id, session_id)
            if success:
                deleted.append({"agent_id": agent_id, "id": session_id})
            else:
                failed.append({"agent_id": agent_id, "id": session_id, "error": "删除未生效"})
        except Exception as exc:
            failed.append({"agent_id": agent_id, "id": session_id, "error": str(exc)})
    return {"ok": True, "deleted": deleted, "failed": failed}


class DeleteProjectPayload(BaseModel):
    project_key: str | None = None
    project_id: str | None = None
    source_id: str | None = None
    workspace: str | None = None
    session_keys: list[dict[str, str]] | None = None
    delete_sessions: bool = True


class CreateProjectPayload(BaseModel):
    workspace: str
    name: str | None = None
    connection_id: str | None = None
    agent_id: str | None = None


@router.post("/api/v1/projects")
def create_project_endpoint(payload: CreateProjectPayload, request: Request) -> dict[str, object]:
    _private(request)
    store = request.app.state.store
    ws = payload.workspace.strip()
    norm_ws = ws.replace("\\", "/").rstrip("/")
    folder_name = norm_ws.split("/")[-1] if "/" in norm_ws else (Path(ws).name or ws)
    p_name = (payload.name or "").strip() or folder_name
    conn_id = payload.connection_id if payload.connection_id and payload.connection_id != "local" else None
    source_id = conn_id or "local"
    project_id = str(uuid4())
    project_data = {
        "project_id": project_id,
        "project_name": p_name,
        "workspace": ws,
        "source_id": source_id,
        "connection_id": conn_id,
        "agent_id": payload.agent_id or "codex",
        "session_count": 0,
    }
    canonical = store.upsert_projects([project_data])
    return {"project": canonical[0] if canonical else project_data}


@router.post("/api/v1/projects/delete")
@router.delete("/api/v1/projects")
def delete_project_endpoint(
    payload: DeleteProjectPayload | None = None,
    project_key: str | None = Query(default=None),
    project_id: str | None = Query(default=None),
    source_id: str | None = Query(default=None),
    workspace: str | None = Query(default=None),
    delete_sessions: bool = Query(default=True),
    request: Request = None,
) -> dict[str, object]:
    _private(request)
    body = payload or DeleteProjectPayload(
        project_key=project_key,
        project_id=project_id,
        source_id=source_id,
        workspace=workspace,
        delete_sessions=delete_sessions,
    )
    raw_session_keys = body.session_keys or []
    session_tuples: list[tuple[str, str]] = [
        (str(item.get("agent_id")), str(item.get("id")))
        for item in raw_session_keys
        if item.get("agent_id") and item.get("id")
    ]

    if body.delete_sessions and session_tuples:
        for agent_id, session_id in session_tuples:
            try:
                _mutate_agent_session(request, agent_id, session_id, None)
            except Exception:
                pass

    res = request.app.state.service.delete_project(
        project_key=body.project_key,
        project_id=body.project_id,
        source_id=body.source_id,
        workspace=body.workspace,
        session_keys=session_tuples if session_tuples else None,
        delete_sessions=body.delete_sessions,
    )
    return res


@router.get("/api/v1/sessions/{session_id}/messages")
async def messages(
    session_id: str,
    request: Request,
    agent_id: str = Query(..., min_length=1, max_length=256),
    before: str | None = Query(default=None, max_length=512),
    limit: int = Query(default=50, ge=1, le=200),
) -> dict[str, object]:
    _private(request)
    session = request.app.state.store.get_session(agent_id, session_id)
    if session is None:
        raise HTTPException(status_code=404, detail="Session was not found")
    runtime = _agent_runtime(request, agent_id)
    reader = getattr(runtime, "messages", None)
    if callable(reader) and (before is None or before.startswith('codex:') or not before.startswith('native:')):
        try:
            page = await asyncio.to_thread(reader, session_id, before, limit)
            context = request.app.state.store.get_message(agent_id, session_id, HANDOFF_CONTEXT_MESSAGE_ID)
            if before is None and context and all(item.get('id') != context['id'] for item in page.get('items', [])):
                page['items'] = [context, *page.get('items', [])]
            if page.get('items') or not request.app.state.store.list_messages(agent_id, session_id, None, 1)[0]:
                return page
        except ConnectionError as exc:
            if before or not request.app.state.store.list_messages(agent_id, session_id, None, 1)[0]:
                raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None
    if before is None or before.startswith('native:'):
        def read_page():
            reader = getattr(runtime, 'load_native_history_page', None)
            return reader(session_id, before, limit) if callable(reader) else None
        try:
            page = await asyncio.to_thread(read_page)
            if page is not None:
                from .attachments import AttachmentManager
                from .native_attachments import bind_hermes_refs, hermes_roots
                from .native_sessions import project_history_messages
                items = project_history_messages(page['items'], durable_session_id=session_id, native_session_id=session_id, source_id=session.get('source_id') or agent_id, agent_id=agent_id)
                manager = AttachmentManager(request.app.state.settings, request.app.state.store)
                roots = hermes_roots()
                cached = {
                    item['id']: item
                    for item in request.app.state.store.get_messages(
                        agent_id, session_id, [item['id'] for item in items]
                    )
                }
                for item in items:
                    previous = cached.get(item['id'])
                    if previous and previous.get('attachments'):
                        item['attachments'] = previous['attachments']
                    bind_hermes_refs(item, manager, roots)
                request.app.state.store.upsert_messages(items)
                context = request.app.state.store.get_message(agent_id, session_id, HANDOFF_CONTEXT_MESSAGE_ID)
                if before is None and context and all(item.get('id') != context['id'] for item in items):
                    items.insert(0, context)
                # Import just this page, without broadcasting it as fresh live messages.
                return {'items': items, 'next_cursor': page['next_cursor']}
        except ValueError:
            if before is not None:
                raise HTTPException(status_code=400, detail='Invalid native history cursor') from None
        except (ConnectionError, OSError, TimeoutError, sqlite3.Error, subprocess.TimeoutExpired):
            if before is not None:
                raise HTTPException(status_code=503, detail='原生消息分页暂不可用，请重试。') from None
        if before is not None:
            raise HTTPException(status_code=503, detail='原生消息分页尚未连接。')
    try:
        items, next_cursor = request.app.state.store.list_messages(agent_id, session_id, before, limit)
    except ValueError:
        raise HTTPException(status_code=400, detail="Invalid history cursor") from None
    return {"items": items, "next_cursor": next_cursor}


class SessionModelSelection(BaseModel):
    agent_id: str = Field(min_length=1, max_length=256)
    provider: str = Field(min_length=1, max_length=160)
    model: str = Field(min_length=1, max_length=160)


@router.get("/api/v1/sessions/{session_id}/models")
def session_models(session_id: str, request: Request, agent_id: str = Query(..., min_length=1)) -> dict[str, object]:
    _private(request)
    from .native_controls import model_choices, runtime_rpc
    if request.app.state.store.get_session(agent_id, session_id) is None:
        raise HTTPException(status_code=404, detail="Session was not found")
    try:
        runtime = _agent_runtime(request, agent_id)
        models = getattr(runtime, "models", None)
        if callable(models):
            return {"items": models(session_id)}
        if getattr(runtime, "daemon_owned", False):
            raise ConnectionError("会话所属运行时未提供模型目录。", 503)
        return {"items": model_choices(runtime_rpc(request.app.state.connections, agent_id))}
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.get("/api/v1/sessions/{session_id}/agent-commands")
def session_agent_commands(
    session_id: str, request: Request, agent_id: str = Query(..., min_length=1)
) -> dict[str, object]:
    _private(request)
    if request.app.state.store.get_session(agent_id, session_id) is None:
        raise HTTPException(status_code=404, detail="Session was not found")
    try:
        runtime = _agent_runtime(request, agent_id)
        commands = getattr(runtime, "commands", None)
        if not callable(commands):
            raise ConnectionError("当前 Agent 未提供原生命令目录。", 503)
        return {"items": commands(session_id)}
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.get("/api/v1/sessions/{session_id}/agent-mentions")
def session_agent_mentions(
    session_id: str, request: Request, agent_id: str = Query(..., min_length=1)
) -> dict[str, object]:
    _private(request)
    if request.app.state.store.get_session(agent_id, session_id) is None:
        raise HTTPException(status_code=404, detail="Session was not found")
    try:
        runtime = _agent_runtime(request, agent_id)
        mentions = getattr(runtime, "mentions", None)
        return {"items": mentions(session_id) if callable(mentions) else []}
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.post("/api/v1/sessions/{session_id}/model")
def session_model(session_id: str, payload: SessionModelSelection, request: Request) -> dict[str, object]:
    _private(request)
    from .native_controls import runtime_rpc, set_session_model
    session = request.app.state.store.get_session(payload.agent_id, session_id)
    if session is None:
        raise HTTPException(status_code=404, detail="Session was not found")
    try:
        runtime = _agent_runtime(request, payload.agent_id)
        set_model = getattr(runtime, "set_model", None)
        if callable(set_model):
            binding = set_model(session_id, payload.provider, payload.model)
        elif getattr(runtime, "daemon_owned", False):
            raise ConnectionError("会话所属运行时不支持模型切换。", 503)
        else:
            binding = set_session_model(runtime_rpc(request.app.state.connections, payload.agent_id), session_id, payload.provider, payload.model)
        agent = request.app.state.store.get_agent(payload.agent_id)
        if agent and request.app.state.service.preserves_model_binding(agent["kind"]):
            if binding.get("provider") != payload.provider or binding.get("model") != payload.model:
                raise ConnectionError('模型切换尚未通过原生状态读回确认。', 502)
            request.app.state.store.set_session_model_binding(
                payload.agent_id, session_id, payload.provider, payload.model
            )
        return binding
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.get("/api/v1/sessions/{session_id}/model")
def read_session_model(session_id: str, request: Request, agent_id: str = Query(..., min_length=1, max_length=256)) -> dict[str, object]:
    _private(request)
    from .native_controls import current_session_model, runtime_rpc
    if request.app.state.store.get_session(agent_id, session_id) is None:
        raise HTTPException(status_code=404, detail="Session was not found")
    try:
        saved = request.app.state.store.get_session_model_binding(agent_id, session_id)
        saved_effort = request.app.state.store.get_session_reasoning_binding(agent_id, session_id)
        agent = request.app.state.store.get_agent(agent_id)
        if saved and agent and request.app.state.service.preserves_model_binding(agent["kind"]):
            return {**saved, 'effort': saved.get('effort')}
        runtime = _agent_runtime(request, agent_id)
        model = getattr(runtime, "model", None)
        if callable(model):
            binding = dict(model(session_id))
            try:
                current_effort = getattr(runtime, "current_effort", None)
                native_effort = current_effort(session_id) if callable(current_effort) else binding.get('effort')
                binding['effort'] = saved_effort or native_effort
            except ConnectionError:
                binding['effort'] = saved_effort
            return binding
        if getattr(runtime, "daemon_owned", False):
            raise ConnectionError("会话所属运行时未提供模型状态。", 503)
        rpc = runtime_rpc(request.app.state.connections, agent_id)
        binding = current_session_model(rpc, session_id)
        from .native_controls import current_session_reasoning
        binding['effort'] = saved_effort or current_session_reasoning(rpc, session_id)
        return binding
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


class SessionReasoningSelection(BaseModel):
    agent_id: str = Field(min_length=1, max_length=256)
    effort: str = Field(min_length=1, max_length=32)


class SessionApprovalModeSelection(BaseModel):
    agent_id: str = Field(min_length=1, max_length=256)
    mode: str = Field(min_length=1, max_length=32)


@router.post("/api/v1/sessions/{session_id}/reasoning")
def session_reasoning(session_id: str, payload: SessionReasoningSelection, request: Request) -> dict[str, object]:
    _private(request)
    from .native_controls import runtime_rpc, set_session_reasoning
    if request.app.state.store.get_session(payload.agent_id, session_id) is None:
        raise HTTPException(status_code=404, detail="Session was not found")
    try:
        runtime = _agent_runtime(request, payload.agent_id)
        set_effort = getattr(runtime, "set_effort", None)
        if callable(set_effort):
            result = set_effort(session_id, payload.effort)
        elif getattr(runtime, "daemon_owned", False):
            raise ConnectionError("会话所属运行时不支持思考强度设置。", 503)
        else:
            result = set_session_reasoning(runtime_rpc(request.app.state.connections, payload.agent_id), session_id, payload.effort)
        if result.get("effort") != payload.effort:
            raise ConnectionError('思考强度尚未通过原生状态读回确认。', 502)
        request.app.state.store.set_session_reasoning_binding(
            payload.agent_id, session_id, payload.effort
        )
        return result
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.get("/api/v1/sessions/{session_id}/approval-mode")
def session_approval_mode(session_id: str, request: Request, agent_id: str = Query(..., min_length=1)) -> dict[str, object]:
    _private(request)
    if request.app.state.store.get_session(agent_id, session_id) is None:
        raise HTTPException(status_code=404, detail="Session was not found")
    saved = request.app.state.store.get_session_approval_mode_binding(agent_id, session_id)
    try:
        runtime = _agent_runtime(request, agent_id)
        get_mode = getattr(runtime, "get_approval_mode", None)
        if callable(get_mode):
            mode = get_mode(session_id)
            if saved is not None and mode != saved:
                set_mode = getattr(runtime, "set_approval_mode", None)
                if callable(set_mode):
                    with suppress(Exception):
                        set_mode(session_id, saved)
                        mode = saved
            return {"mode": mode}
        if getattr(runtime, "daemon_owned", False):
            if saved is not None:
                return {"mode": saved}
            raise ConnectionError("会话所属运行时不支持审批模式读取。", 503)
        from .native_controls import current_session_approval_mode, runtime_rpc
        mode = current_session_approval_mode(runtime_rpc(request.app.state.connections, agent_id), session_id)
        if saved is not None and mode != saved:
            from .native_controls import set_session_approval_mode
            with suppress(Exception):
                set_session_approval_mode(runtime_rpc(request.app.state.connections, agent_id), session_id, saved)
                mode = saved
        return {"mode": mode}
    except ConnectionError as exc:
        if saved is not None:
            return {"mode": saved}
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.post("/api/v1/sessions/{session_id}/approval-mode")
def session_approval_mode_select(session_id: str, payload: SessionApprovalModeSelection, request: Request) -> dict[str, object]:
    _private(request)
    if request.app.state.store.get_session(payload.agent_id, session_id) is None:
        raise HTTPException(status_code=404, detail="Session was not found")
    try:
        runtime = _agent_runtime(request, payload.agent_id)
        set_mode = getattr(runtime, "set_approval_mode", None)
        if callable(set_mode):
            result = set_mode(session_id, payload.mode)
            request.app.state.store.set_session_approval_mode_binding(payload.agent_id, session_id, payload.mode)
            return result
        if getattr(runtime, "daemon_owned", False):
            raise ConnectionError("会话所属运行时不支持审批模式设置。", 503)
        from .native_controls import runtime_rpc, set_session_approval_mode
        result = set_session_approval_mode(runtime_rpc(request.app.state.connections, payload.agent_id), session_id, payload.mode)
        request.app.state.store.set_session_approval_mode_binding(payload.agent_id, session_id, payload.mode)
        return result
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.get("/api/v1/sessions/{session_id}/commands")
def commands(
    session_id: str,
    request: Request,
    agent_id: str = Query(..., min_length=1, max_length=256),
) -> dict[str, object]:
    _private(request)
    if request.app.state.store.get_session(agent_id, session_id) is None:
        raise HTTPException(status_code=404, detail="Session was not found")
    return {"items": request.app.state.store.list_commands(agent_id, session_id)}


@router.patch("/api/v1/commands/{command_id}")
def edit_queued_command(command_id: str, payload: QueuedCommandEdit, request: Request) -> dict[str, object]:
    _private(request)
    try:
        return request.app.state.service.update_queued_command(payload.agent_id, payload.session_id, command_id, payload.text)
    except (CommandRejected, ConnectionError) as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.delete("/api/v1/commands/{command_id}")
def delete_queued_command(command_id: str, request: Request, agent_id: str = Query(...), session_id: str = Query(...)) -> dict[str, object]:
    _private(request)
    try:
        return request.app.state.service.cancel_queued_command(agent_id, session_id, command_id)
    except (CommandRejected, ConnectionError) as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.post("/api/v1/commands/{command_id}/send")
async def send_queued_command(command_id: str, payload: QueuedCommandRef, request: Request) -> dict[str, object]:
    _private(request)
    try:
        return await request.app.state.service.send_queued_command(payload.agent_id, payload.session_id, command_id)
    except (CommandRejected, ConnectionError) as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.get("/api/v1/sessions/{session_id}/tasks")
def tasks(
    session_id: str,
    request: Request,
    agent_id: str = Query(..., min_length=1, max_length=256),
) -> dict[str, object]:
    _private(request)
    if request.app.state.store.get_session(agent_id, session_id) is None:
        raise HTTPException(status_code=404, detail="Session was not found")
    return {"items": request.app.state.store.list_tasks(agent_id, session_id)}


@router.post("/api/v1/system/open-browser")
async def open_system_browser(request: Request) -> dict[str, object]:
    _private(request)
    import webbrowser, sys, subprocess
    try:
        payload = await request.json()
    except Exception:
        raise HTTPException(status_code=400, detail="Invalid JSON")
    url = str(payload.get("url") or "").strip()
    if not url:
        raise HTTPException(status_code=400, detail="URL is required")
    if not url.startswith("http://") and not url.startswith("https://"):
        url = f"http://{url}"
    try:
        if sys.platform == "win32":
            import os
            # On Windows, os.startfile(url) or subprocess rundll32/cmd start
            try:
                os.startfile(url)
            except Exception:
                subprocess.Popen(["cmd.exe", "/c", "start", "", url], shell=False)
        elif sys.platform == "darwin":
            subprocess.Popen(["open", url])
        else:
            subprocess.Popen(["xdg-open", url])
        return {"status": "ok", "opened": url}
    except Exception:
        try:
            webbrowser.open(url)
            return {"status": "ok", "opened": url}
        except Exception as exc:
            raise HTTPException(status_code=500, detail=f"Failed to open browser: {exc}")


@router.get("/api/v1/browser/proxy")
@router.post("/api/v1/browser/proxy")
async def proxy_browser(request: Request, url: str | None = None):
    _private(request)
    import httpx
    import re
    from fastapi.responses import Response

    clean_url = (url or "").strip()
    if not clean_url and request.method == "POST":
        try:
            payload = await request.json()
            clean_url = str(payload.get("url") or "").strip()
        except Exception:
            pass

    if not clean_url:
        target_param = request.query_params.get("url")
        if target_param:
            clean_url = target_param.strip()

    if not clean_url:
        raise HTTPException(status_code=400, detail="URL is required")
    if not clean_url.startswith("http://") and not clean_url.startswith("https://"):
        clean_url = f"http://{clean_url}"

    headers = {
        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
        "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
        "Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
    }

    try:
        async with httpx.AsyncClient(follow_redirects=True, timeout=15.0, verify=False) as client:
            resp = await client.get(clean_url, headers=headers)
    except Exception as exc:
        err_html = f"""
        <!DOCTYPE html>
        <html>
        <head><meta charset="utf-8"><title>代理访问异常</title>
        <style>body {{ font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; padding: 32px; background: #0b0f19; color: #f3f4f6; }} .card {{ background: #1f2937; border-radius: 8px; padding: 24px; max-width: 600px; margin: 40px auto; border: 1px solid #374151; }} h3 {{ margin-top: 0; color: #ef4444; }} code {{ background: #111827; padding: 2px 6px; border-radius: 4px; color: #93c5fd; }}</style>
        </head>
        <body>
            <div class="card">
                <h3>无法直接在内嵌代理中加载该网址</h3>
                <p>请求地址: <code>{clean_url}</code></p>
                <p>错误详情: {str(exc)}</p>
                <p style="color: #9ca3af; font-size: 14px; margin-top: 16px;">建议：点击右上角的 🖥️ 设备浏览器图标，直接在系统的原生 Edge / Chrome 中打开该页面。</p>
            </div>
        </body>
        </html>
        """
        return Response(content=err_html, media_type="text/html", status_code=502)

    content_type = resp.headers.get("content-type", "text/html")
    body = resp.content

    # 如果是 HTML，注入 <base href="..."> 使得页面内部的相对路径（图片、脚本、样式）正确加载
    if "text/html" in content_type.lower():
        try:
            html_text = resp.text
            final_url = str(resp.url)
            # 在 <head> 后注入 <base> 标签
            if "<head>" in html_text or "<HEAD>" in html_text:
                html_text = re.sub(r"(<head[^>]*>)", rf'\1\n  <base href="{final_url}">', html_text, count=1, flags=re.IGNORECASE)
            else:
                html_text = f'<base href="{final_url}">' + html_text
            body = html_text.encode("utf-8", errors="replace")
        except Exception:
            body = resp.content

    response_headers = dict(resp.headers)
    # 彻底剥离跨域与嵌入限制头，允许 iframe 正常渲染
    for h in ["x-frame-options", "content-security-policy", "content-security-policy-report-only", "content-encoding", "content-length"]:
        response_headers.pop(h, None)
        response_headers.pop(h.title(), None)
        response_headers.pop(h.upper(), None)

    # 允许当前源以任何形式嵌入
    response_headers["Access-Control-Allow-Origin"] = "*"
    response_headers["X-Content-Type-Options"] = "nosniff"

    return Response(
        content=body,
        status_code=resp.status_code,
        headers=response_headers,
        media_type=content_type,
    )


@router.post("/api/v1/system/open-file")
async def open_system_file(request: Request) -> dict[str, object]:
    _private(request)
    import os, urllib.parse, sys
    try:
        payload = await request.json()
    except Exception:
        raise HTTPException(status_code=400, detail="Invalid JSON")
    raw_path = str(payload.get("path") or "").strip().strip('<>').strip('"\'')
    action = str(payload.get("action") or "open")
    if not raw_path:
        raise HTTPException(status_code=400, detail="Path is required")
    cleaned_path = urllib.parse.unquote(raw_path)
    try:
        resolved = Path(cleaned_path).resolve()
    except (OSError, ValueError):
        raise HTTPException(status_code=400, detail="Invalid file path")
    if not resolved.exists():
        raise HTTPException(status_code=404, detail="File does not exist")
    try:
        if action == "reveal" and sys.platform == "win32":
            args = ["explorer.exe", str(resolved)] if resolved.is_dir() else ["explorer.exe", "/select,", str(resolved)]
            subprocess.Popen(args)
        elif action == "reveal" and sys.platform == "darwin":
            subprocess.Popen(["open", str(resolved)] if resolved.is_dir() else ["open", "-R", str(resolved)])
        elif action == "reveal":
            subprocess.Popen(["xdg-open", str(resolved if resolved.is_dir() else resolved.parent)])
        elif sys.platform == "win32":
            os.startfile(str(resolved))
        elif sys.platform == "darwin":
            subprocess.Popen(["open", str(resolved)])
        else:
            subprocess.Popen(["xdg-open", str(resolved)])
        return {"status": "ok", "opened": str(resolved)}
    except Exception as exc:
        raise HTTPException(status_code=500, detail=f"Failed to open file: {exc}")


@router.post("/api/v1/system/write-file")
async def write_system_file(request: Request) -> dict[str, object]:
    _private(request)
    import urllib.parse
    try:
        payload = await request.json()
    except Exception:
        raise HTTPException(status_code=400, detail="Invalid JSON")
    raw_path = str(payload.get("path") or "").strip().strip('<>').strip('"\'')
    content = payload.get("content")
    session_id = payload.get("session_id") or ""
    connection_id = payload.get("connection_id") or ""

    if not raw_path or content is None:
        raise HTTPException(status_code=400, detail="Path and content are required")
    cleaned_path = urllib.parse.unquote(raw_path)

    # 允许保存代码、脚本、配置、设计图与文本文件
    allowed_exts = {
        ".drawio", ".xml", ".svg", ".json", ".md", ".txt",
        ".py", ".ts", ".tsx", ".js", ".jsx", ".rs", ".go", ".c", ".cpp", ".h",
        ".css", ".scss", ".less", ".html", ".sql", ".yaml", ".yml",
        ".sh", ".bash", ".bat", ".ps1", ".toml", ".ini", ".env", ".dockerfile",
        ".mmd", ".excalidraw"
    }

    resolved_cid = connection_id
    if not resolved_cid and session_id:
        try:
            sess = request.app.state.store.find_session_by_id(session_id)
            if sess and sess.get("connection_id") and sess["connection_id"] != "local":
                resolved_cid = sess["connection_id"]
        except Exception:
            pass

    # 如果是远程 SSH 路径，通过 SSH 远程原子写入
    if resolved_cid and resolved_cid != "local":
        suffix = ("." + cleaned_path.split(".")[-1].lower()) if "." in cleaned_path else ""
        if not suffix and cleaned_path.rstrip("/").split("/")[-1].lower().startswith("dockerfile"):
            suffix = ".dockerfile"
        if suffix not in allowed_exts:
            raise HTTPException(status_code=403, detail=f"Extension {suffix} is not allowed for write")

        ssh_conn = request.app.state.store.get_ssh_connection(resolved_cid)
        if not ssh_conn:
            raise HTTPException(status_code=404, detail="SSH 连接不存在")
        from .ssh_transport import SshNativeRuntime, build_remote_python_command
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
import sys
from pathlib import Path
target = Path({repr(cleaned_path)}).expanduser().resolve()
target.parent.mkdir(parents=True, exist_ok=True)
tmp_file = target.with_suffix(target.suffix + '.tmp')
tmp_file.write_bytes(sys.stdin.buffer.read())
tmp_file.replace(target)
"""
        cmd = build_remote_python_command(remote_script)
        try:
            content_bytes = str(content).encode("utf-8")
            res = subprocess.run(
                argv + [cmd],
                input=content_bytes,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=15,
                check=False,
                creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0),
            )
            if res.returncode != 0:
                raise HTTPException(status_code=500, detail="远程写入文件失败")
            return {"status": "ok", "path": cleaned_path}
        except subprocess.TimeoutExpired:
            raise HTTPException(status_code=504, detail="写入远程文件超时")
        except HTTPException:
            raise
        except Exception as exc:
            raise HTTPException(status_code=500, detail=f"远程写入文件失败: {exc}")

    # 本地写入
    try:
        resolved = Path(cleaned_path).resolve()
    except (OSError, ValueError):
        raise HTTPException(status_code=400, detail="Invalid file path")

    suffix = resolved.suffix.lower()
    if not suffix and resolved.name.lower().startswith("dockerfile"):
        suffix = ".dockerfile"
    if suffix not in allowed_exts:
        raise HTTPException(status_code=403, detail=f"Extension {resolved.suffix} is not allowed for write")

    try:
        resolved.parent.mkdir(parents=True, exist_ok=True)
        tmp_file = resolved.with_suffix(resolved.suffix + ".tmp")
        tmp_file.write_text(str(content), encoding="utf-8")
        tmp_file.replace(resolved)
        return {"status": "ok", "path": str(resolved)}
    except Exception as exc:
        raise HTTPException(status_code=500, detail=f"Failed to write file: {exc}")


@router.get("/api/v1/runtime")
def runtime(request: Request) -> dict[str, object]:
    _private(request)
    return {"items": request.app.state.supervisor.items()}


@router.get("/api/v1/connections")
def connections(request: Request) -> dict[str, object]:
    _private(request)
    return request.app.state.connections.snapshot(request.app.state.service)


@router.get('/api/v1/environments')
async def environments(request: Request):
    _private(request)
    return await asyncio.to_thread(request.app.state.environments.snapshot)


@router.post('/api/v1/environments/{connection_id}/discover')
async def discover_environment(connection_id: str, request: Request):
    _private(request)
    try:
        return await asyncio.to_thread(request.app.state.environments.discover, connection_id)
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.post('/api/v1/environments/{connection_id}/agents/{kind}/{action}')
async def environment_agent(connection_id: str, kind: str, action: str, request: Request):
    _private(request)
    if action not in {'connect', 'disconnect'}:
        raise HTTPException(status_code=422, detail='不支持的连接操作。')
    try:
        return await asyncio.to_thread(request.app.state.environments.change, connection_id, kind, action == 'connect')
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.get('/api/v1/connections/codex')
def codex_connection(request: Request):
    _private(request)
    return request.app.state.codex.snapshot()


@router.post('/api/v1/connections/codex/connect')
async def connect_codex(request: Request):
    _private(request)
    try:
        return await asyncio.to_thread(request.app.state.codex.connect)
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.post('/api/v1/connections/codex/disconnect')
async def disconnect_codex(request: Request):
    _private(request)
    return await asyncio.to_thread(request.app.state.codex.disconnect)


@router.get("/api/v1/connections/{connection_id}/history")
def connection_history(
    connection_id: str,
    request: Request,
    before: str | None = Query(default=None, max_length=512),
    limit: int = Query(default=50, ge=1, le=200),
) -> dict[str, object]:
    _private(request)
    if request.app.state.store.get_ssh_connection(connection_id) is None:
        raise HTTPException(status_code=404, detail="Connection was not found")
    try:
        items, next_cursor = request.app.state.store.list_connection_history(connection_id, before, limit)
    except ValueError:
        raise HTTPException(status_code=400, detail="Invalid history cursor") from None
    return {"items": items, "next_cursor": next_cursor}


@router.post("/api/v1/connections/local/connect")
async def connect_local_hermes(request: Request) -> dict[str, object]:
    _private(request)
    try:
        return await asyncio.to_thread(
            request.app.state.connections.connect_local, request.app.state.service
        )
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.post("/api/v1/connections/local/disconnect")
async def disconnect_local_hermes(request: Request) -> dict[str, object]:
    _private(request)
    return await asyncio.to_thread(
        request.app.state.connections.disconnect_local, request.app.state.service
    )


@router.put("/api/v1/connections/ssh")
def save_ssh_connection(payload: SshConnectionSettings, request: Request) -> dict[str, object]:
    _private(request)
    try:
        return request.app.state.connections.save_ssh(payload.model_dump())
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.post("/api/v1/connections/ssh/test")
def test_ssh_connection(request: Request) -> dict[str, object]:
    _private(request)
    try:
        return request.app.state.connections.test_ssh()
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.post("/api/v1/connections/ssh/connect")
async def connect_ssh_connection(request: Request) -> dict[str, object]:
    _private(request)
    try:
        return await asyncio.to_thread(
            request.app.state.connections.connect_ssh, request.app.state.service
        )
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.post("/api/v1/connections/ssh/disconnect")
async def disconnect_ssh_connection(request: Request) -> dict[str, object]:
    _private(request)
    return await asyncio.to_thread(
        request.app.state.connections.disconnect_ssh, request.app.state.service
    )


@router.put("/api/v1/connections/ssh/{connection_id}")
def update_ssh_connection(
    connection_id: str, payload: SshConnectionSettings, request: Request
) -> dict[str, object]:
    _private(request)
    try:
        return request.app.state.connections.save_ssh(payload.model_dump(), connection_id)
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.post("/api/v1/connections/ssh/{connection_id}/test")
def test_saved_ssh_connection(connection_id: str, request: Request) -> dict[str, object]:
    _private(request)
    try:
        return request.app.state.connections.test_ssh(connection_id)
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.post("/api/v1/connections/ssh/{connection_id}/connect")
async def connect_saved_ssh_connection(connection_id: str, request: Request) -> dict[str, object]:
    _private(request)
    try:
        return await asyncio.to_thread(
            request.app.state.connections.connect_ssh,
            request.app.state.service,
            connection_id,
        )
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.post("/api/v1/connections/ssh/{connection_id}/disconnect")
async def disconnect_saved_ssh_connection(connection_id: str, request: Request) -> dict[str, object]:
    _private(request)
    try:
        return await asyncio.to_thread(
            request.app.state.connections.disconnect_ssh,
            request.app.state.service,
            connection_id,
        )
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.delete("/api/v1/connections/ssh/{connection_id}")
def delete_saved_ssh_connection(connection_id: str, request: Request) -> dict[str, object]:
    _private(request)
    try:
        return request.app.state.connections.delete_ssh(request.app.state.service, connection_id)
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.post("/api/v1/runtime/launch")
def launch_runtime(payload: RuntimeLaunch, request: Request) -> dict[str, str]:
    _private(request)
    try:
        return request.app.state.supervisor.launch(payload.kind, payload.workspace)
    except RuntimeError as exc:
        raise HTTPException(status_code=409, detail=str(exc)) from None
class AgentMcpTogglePayload(BaseModel):
    agent_id: str = Field(min_length=1, max_length=256)
    enabled: bool = True


@router.get("/api/v1/agents/{agent_id}/mcp")
def get_agent_mcp_status(agent_id: str, request: Request) -> dict[str, object]:
    _private(request)
    from sqlalchemy import select
    from .models import WorkspacePreferenceRow
    with request.app.state.store.session() as db:
        row = db.get(WorkspacePreferenceRow, f"agent_mcp_enabled:{agent_id}")
        enabled = row.value if row and isinstance(row.value, bool) else True
    return {"agent_id": agent_id, "enabled": enabled}


@router.post("/api/v1/agents/{agent_id}/mcp")
def toggle_agent_mcp_status(agent_id: str, payload: AgentMcpTogglePayload, request: Request) -> dict[str, object]:
    _private(request)
    from sqlalchemy.dialects.sqlite import insert
    from .models import WorkspacePreferenceRow
    with request.app.state.store.session() as db:
        stmt = insert(WorkspacePreferenceRow).values(key=f"agent_mcp_enabled:{agent_id}", value=payload.enabled)
        stmt = stmt.on_conflict_do_update(index_elements=["key"], set_={"value": payload.enabled})
        db.execute(stmt)
    return {"agent_id": agent_id, "enabled": payload.enabled}


class NetworkConfigPatch(BaseModel):
    public_url: str | None = None
    allowed_origins: list[str] | None = None


def _get_local_ip() -> str:
    import socket
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        s.connect(("8.8.8.8", 80))
        ip = s.getsockname()[0]
        s.close()
        return ip
    except Exception:
        return "127.0.0.1"


@router.get("/api/v1/network/config")
def get_network_config(request: Request) -> dict[str, Any]:
    _private(request)
    settings = _settings(request)
    import json
    from .models import WorkspacePreferenceRow
    store = request.app.state.store
    with store.session() as db:
        origins_row = db.get(WorkspacePreferenceRow, "network:allowed_origins")
        pub_row = db.get(WorkspacePreferenceRow, "network:public_url")
        
        origins_val = origins_row.value if origins_row else None
        allowed_list = list(origins_val) if isinstance(origins_val, list) else list(settings.allowed_origins)
        
        pub_val = pub_row.value if pub_row else None
        pub_url = str(pub_val).strip() if pub_val else "https://ao.651971564.xyz"
        if pub_url and pub_url not in allowed_list:
            allowed_list.append(pub_url)
            
        return {
            "public_url": pub_url,
            "allowed_origins": allowed_list,
            "local_ip": _get_local_ip(),
            "port": settings.port,
            "token": settings.browser_secret,
        }


@router.post("/api/v1/network/config")
def update_network_config(payload: NetworkConfigPatch, request: Request) -> dict[str, Any]:
    _private(request)
    settings = _settings(request)
    import json
    from .models import WorkspacePreferenceRow
    from sqlalchemy.dialects.sqlite import insert
    store = request.app.state.store

    with store.session() as db:
        if payload.public_url is not None:
            clean_pub = payload.public_url.strip()
            stmt = insert(WorkspacePreferenceRow).values(key="network:public_url", value=clean_pub)
            stmt = stmt.on_conflict_do_update(index_elements=["key"], set_={"value": clean_pub})
            db.execute(stmt)

        if payload.allowed_origins is not None:
            cleaned = [str(o).strip().rstrip("/") for o in payload.allowed_origins if str(o).strip()]
            cleaned = list(dict.fromkeys(cleaned))
            stmt = insert(WorkspacePreferenceRow).values(key="network:allowed_origins", value=cleaned)
            stmt = stmt.on_conflict_do_update(index_elements=["key"], set_={"value": cleaned})
            db.execute(stmt)

            try:
                object.__setattr__(settings, "allowed_origins", tuple(cleaned))
            except Exception:
                pass

            from pathlib import Path
            p = Path(os.environ["LOCALAPPDATA"]) / "Astrorder/production.json"
            if p.is_file():
                try:
                    cfg = json.loads(p.read_text(encoding="utf-8"))
                    cfg["environment"]["ASTRORDER_ALLOWED_ORIGINS"] = ",".join(cleaned)
                    if payload.public_url:
                        cfg["environment"]["ASTRORDER_PUBLIC_URL"] = payload.public_url.strip()
                    p.write_text(json.dumps(cfg, indent=2, ensure_ascii=False), encoding="utf-8")
                except Exception:
                    pass

    return get_network_config(request)


class JevConfigPatch(BaseModel):
    api_key: str | None = None


class LlmConfigPatch(BaseModel):
    base_url: str = Field(min_length=1, max_length=2048)
    api_key: str | None = Field(default=None, max_length=4096)
    model: str = Field(min_length=1, max_length=256)
    reasoning: str = Field(pattern="^(none|low|medium|high)$")


def _llm_config_response(config: dict[str, str]) -> dict[str, Any]:
    key = config["api_key"]
    return {
        "configured": bool(key),
        "masked_key": f"{key[:6]}...{key[-4:]}" if len(key) > 12 else ("已配置" if key else ""),
        "base_url": config["base_url"],
        "model": config["model"],
        "reasoning": config["reasoning"],
    }


@router.get("/api/v1/services/jev/config")
def get_jev_config(request: Request) -> dict[str, Any]:
    _private(request)
    from .jev_client import get_jev_key
    key = get_jev_key(request.app.state.store)
    masked = f"{key[:10]}...{key[-8:]}" if key and len(key) > 20 else ("已配置" if key else "")
    return {
        "configured": bool(key),
        "masked_key": masked,
    }


@router.post("/api/v1/services/jev/config")
def update_jev_config(payload: JevConfigPatch, request: Request) -> dict[str, Any]:
    _private(request)
    from .jev_client import set_jev_key
    set_jev_key(request.app.state.store, payload.api_key)
    return get_jev_config(request)


@router.post("/api/v1/services/jev/test")
def test_jev_connection(payload: JevConfigPatch, request: Request) -> dict[str, Any]:
    _private(request)
    from .jev_client import evaluate_blackboard_component
    key = (payload.api_key or "").strip() or None
    try:
        res = evaluate_blackboard_component("Ping health check probe: system metrics and status.", store=request.app.state.store, api_key=key)
        return {"ok": True, "details": res}
    except Exception as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc


@router.get("/api/v1/services/llm/config")
def get_llm_service_config(request: Request) -> dict[str, Any]:
    _private(request)
    from .llm_config import get_llm_config
    return _llm_config_response(get_llm_config(request.app.state.store))


@router.post("/api/v1/services/llm/config")
def update_llm_service_config(payload: LlmConfigPatch, request: Request) -> dict[str, Any]:
    _private(request)
    from .llm_config import get_llm_config, set_llm_config
    current = get_llm_config(request.app.state.store)
    config = payload.model_dump()
    if payload.api_key is None:
        config["api_key"] = current["api_key"]
    try:
        return _llm_config_response(set_llm_config(request.app.state.store, config))
    except ValueError as exc:
        raise HTTPException(status_code=422, detail=str(exc)) from exc


@router.post("/api/v1/services/llm/test")
def test_llm_service_connection(payload: LlmConfigPatch, request: Request) -> dict[str, Any]:
    _private(request)
    import httpx
    from .llm_config import get_llm_config, reasoning_payload
    current = get_llm_config(request.app.state.store)
    key = (payload.api_key or current["api_key"]).strip()
    if not key:
        raise HTTPException(status_code=400, detail="请先填写 API Key")
    body: dict[str, Any] = {
        "model": payload.model.strip(),
        "max_tokens": 16,
        "messages": [{"role": "user", "content": "Reply with OK only."}],
    }
    body.update(reasoning_payload(payload.base_url, payload.reasoning))
    try:
        response = httpx.post(
            payload.base_url.rstrip("/") + "/chat/completions",
            headers={"Authorization": f"Bearer {key}"},
            json=body,
            timeout=20,
        )
        response.raise_for_status()
        data = response.json()
        text = data["choices"][0]["message"]["content"]
        return {"ok": True, "model": data.get("model") or payload.model, "reply": text}
    except (httpx.HTTPError, KeyError, TypeError, ValueError) as exc:
        raise HTTPException(status_code=400, detail=f"LLM 连接测试失败：{exc}") from exc


class JevFilterPayload(BaseModel):
    command: str = ""
    output: str = ""


@router.post("/api/v1/tools/jev-filter")
def jev_filter_endpoint(payload: JevFilterPayload, request: Request) -> dict[str, Any]:
    _private(request)
    from .jev_client import filter_terminal_output
    return filter_terminal_output(payload.command, payload.output, store=request.app.state.store)


class JevHandoffCuratePayload(BaseModel):
    session_id: str
    agent_id: str


@router.post("/api/v1/sessions/handoff-curate")
def jev_handoff_curate_endpoint(payload: JevHandoffCuratePayload, request: Request) -> dict[str, Any]:
    _private(request)
    from .jev_client import curate_handoff_summary
    store = request.app.state.store
    session_row = store.get_session(payload.agent_id, payload.session_id)
    if not session_row:
        raise HTTPException(status_code=404, detail="会话不存在")
    
    all_msgs = []
    try:
        all_msgs = store.list_messages(payload.session_id, limit=20)
    except Exception:
        pass
    return curate_handoff_summary(all_msgs, session_row, store=store)
