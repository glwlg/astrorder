from __future__ import annotations

import asyncio
import logging
import os
import sqlite3
import subprocess
from pathlib import Path

from fastapi import APIRouter, File, HTTPException, Query, Request, UploadFile
from fastapi.responses import FileResponse, JSONResponse, Response
from pydantic import BaseModel, Field

from .attachments import AttachmentError
from .auth import COOKIE_NAME, browser_authenticated, require_browser, validate_origin
from .connections import ConnectionError, _windows_hide_startupinfo
from .schemas import AuthRequest, CommandSubmission, RuntimeLaunch, SshConnectionSettings
from .service import CommandRejected

router = APIRouter()
logger = logging.getLogger(__name__)


def _settings(request: Request):
    return request.app.state.settings


def _private(request: Request) -> None:
    require_browser(request, _settings(request))


def _codex(request: Request, agent_id: str):
    environments = getattr(request.app.state, 'environments', None)
    if environments:
        return environments.for_agent(agent_id)
    connection = getattr(request.app.state, 'codex', None)
    return connection if connection and connection.agent_id == agent_id else None


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


@router.post("/api/v1/auth/session")
async def create_auth_session(payload: AuthRequest, request: Request) -> JSONResponse:
    settings = _settings(request)
    if not settings.browser_secret:
        raise HTTPException(status_code=503, detail="Authentication is not configured")
    validate_origin(request.headers.get("origin"), settings)
    import hmac

    if not hmac.compare_digest(payload.token, settings.browser_secret):
        raise HTTPException(status_code=401, detail="Invalid authentication token")
    response = JSONResponse({"authenticated": True})
    response.set_cookie(
        COOKIE_NAME,
        payload.token,
        httponly=True,
        samesite="lax",
        secure=False,
        max_age=60 * 60 * 24 * 30,
        path="/",
    )
    return response


@router.get("/api/v1/auth/session")
def get_auth_session(request: Request) -> dict[str, bool]:
    return {"authenticated": browser_authenticated(request, _settings(request))}


@router.delete("/api/v1/auth/session", status_code=204)
def delete_auth_session(request: Request) -> Response:
    settings = _settings(request)
    validate_origin(request.headers.get("origin"), settings)
    response = Response(status_code=204)
    response.delete_cookie(COOKIE_NAME, path="/")
    return response


@router.get("/api/v1/bootstrap")
def bootstrap(request: Request) -> dict[str, object]:
    _private(request)
    from .agent_registry import current_agents
    store = request.app.state.store
    sessions = store.sessions_for_bootstrap()
    presence_rows = _collect_presence(request)
    by_key = {(row['agent_id'], row['id']): row for row in presence_rows.get('items', [])}
    live = {(row['agent_id'], row['id']) for row in presence_rows.get('live', [])}
    for session in sessions:
        key = (session['agent_id'], session['id'])
        if key in by_key:
            session['last_user_at'] = by_key[key]['last_user_at']
        session['live'] = key in live
    return {
        "protocol_version": 1,
        "agents": [request.app.state.service.effective_agent(a) for a in current_agents(store)],
        "projects": store.list_projects(),
        "sessions": sessions,
        "cursor": store.latest_cursor(),
        "approvals": request.app.state.service.hermes_approvals.snapshot() if request.app.state.service.hermes_approvals else [],
    }


@router.get("/api/v1/agents")
def agents(request: Request) -> dict[str, object]:
    _private(request)
    from .agent_registry import current_agents
    return {"items": [request.app.state.service.effective_agent(a) for a in current_agents(request.app.state.store)]}


@router.get('/api/v1/agents/{agent_id}/observations')
def observations(agent_id: str, request: Request, session_id: str | None = None):
    _private(request)
    if request.app.state.store.get_agent(agent_id) is None:
        raise HTTPException(status_code=404, detail='Agent not found')
    observer=request.app.state.observers
    return {'status':observer.status(agent_id),'items':observer.recent(agent_id,session_id)}


@router.post('/api/v1/agents/{agent_id}/observer')
def install_observer(agent_id: str, request: Request):
    _private(request)
    try:
        return request.app.state.observers.install(agent_id)
    except ValueError:
        raise HTTPException(status_code=409, detail='请先连接 Codex；无效的既有 hooks 配置不会被覆盖。') from None
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.get('/api/v1/user-activity')
async def user_activity(request: Request):
    _private(request)
    data = await asyncio.to_thread(_collect_presence, request)
    return {'items': data['items'], 'live': data['live']}


@router.get("/api/v1/sessions")
def sessions(request: Request, agent_id: str | None = None) -> dict[str, object]:
    _private(request)
    return {"items": request.app.state.store.list_sessions(agent_id)}


class CreateSessionPayload(BaseModel):
    agent_id: str = Field(min_length=1, max_length=256)
    workspace: str | None = Field(default=None, max_length=2000)
    title: str | None = Field(default=None, max_length=512)
    project_id: str | None = Field(default=None, max_length=256)
    project_name: str | None = Field(default=None, max_length=256)
    parent_session_id: str | None = Field(default=None, max_length=256)
    ephemeral: bool = Field(default=False)


@router.get("/api/v1/open-sessions")
async def open_sessions(request: Request) -> dict[str, object]:
    _private(request)
    from .native_controls import open_native_session_ids, runtime_rpc
    store = request.app.state.store
    def read(agent_id):
        try:
            codex = _codex(request, agent_id)
            ids = codex.open_ids() if codex else open_native_session_ids(runtime_rpc(request.app.state.connections, agent_id))
            return agent_id, ids
        except (ConnectionError, OSError, TimeoutError, RuntimeError):
            return agent_id, None
    results = await asyncio.gather(*(asyncio.to_thread(read, agent['id']) for agent in store.list_agents() if agent['kind'] == 'hermes' or _codex(request, agent['id'])))
    presence_rows = await asyncio.to_thread(_collect_presence, request)
    known = [agent_id for agent_id, ids in results if ids is not None]
    items = {(row['agent_id'], row['id']): row for row in presence_rows['open']}
    for agent_id, ids in results:
        if ids is None:
            continue
        for session_id in ids:
            items[(agent_id, session_id)] = {'agent_id': agent_id, 'id': session_id}
    live = {(row['agent_id'], row['id']) for row in presence_rows['live']}
    return {
        'known_agent_ids': list(dict.fromkeys([*known, *[row['agent_id'] for row in presence_rows['open']]])),
        'items': list(items.values()),
        'live': [{'agent_id': agent_id, 'id': session_id} for agent_id, session_id in live],
    }


@router.post("/api/v1/sessions")
def create_session(payload: CreateSessionPayload, request: Request) -> dict[str, object]:
    _private(request)
    try:
        codex = _codex(request, payload.agent_id)
        if codex:
            data = codex.create(
                payload.workspace,
                payload.title,
                ephemeral=payload.ephemeral,
                parent_session_id=payload.parent_session_id,
            )
        else:
            data = request.app.state.connections.create_session_for_agent(
                agent_id=payload.agent_id,
                workspace=payload.workspace,
                title=payload.title,
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
        codex = _codex(request, agent_id)
        if codex:
            codex.mutate(session_id, updates)
        else:
            request.app.state.connections.mutate_session_for_agent(agent_id, session_id, updates)
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
        codex = _codex(request, agent_id)
        if codex:
            codex.mutate(session_id, None)
        else:
            request.app.state.connections.mutate_session_for_agent(agent_id, session_id, None)
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None
    success = request.app.state.service.delete_session(agent_id, session_id)
    if not success:
        raise HTTPException(status_code=404, detail="Session was not found")
    return {"ok": True, "id": session_id}


class DeleteProjectPayload(BaseModel):
    project_key: str | None = None
    project_id: str | None = None
    source_id: str | None = None
    workspace: str | None = None
    session_keys: list[dict[str, str]] | None = None
    delete_sessions: bool = True


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
                codex = _codex(request, agent_id)
                if codex:
                    codex.mutate(session_id, None)
                else:
                    request.app.state.connections.mutate_session_for_agent(agent_id, session_id, None)
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
    if request.app.state.store.get_session(agent_id, session_id) is None:
        raise HTTPException(status_code=404, detail="Session was not found")
    session = request.app.state.store.get_session(agent_id, session_id)
    if session is None:
        raise HTTPException(status_code=404, detail="Session was not found")
    codex = _codex(request, agent_id)
    if codex and (before is None or before.startswith('codex:') or not before.startswith('native:')):
        try:
            page = await asyncio.to_thread(codex.messages, session_id, before, limit)
            if page.get('items') or not request.app.state.store.list_messages(agent_id, session_id, None, 1)[0]:
                return page
        except ConnectionError as exc:
            if before or not request.app.state.store.list_messages(agent_id, session_id, None, 1)[0]:
                raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None
    if before is None or before.startswith('native:'):
        def read_page():
            runtime = request.app.state.connections.get_runtime_by_agent_id(agent_id)
            reader = getattr(runtime, 'load_native_history_page', None)
            return reader(session_id, before, limit) if callable(reader) else None
        try:
            page = await asyncio.to_thread(read_page)
            if page is not None:
                from .native_sessions import project_history_messages
                from .attachments import AttachmentManager
                from .native_attachments import bind_hermes_refs, hermes_roots
                items = project_history_messages(page['items'], durable_session_id=session_id, native_session_id=session_id, source_id=session.get('source_id') or agent_id, agent_id=agent_id)
                manager = AttachmentManager(request.app.state.settings, request.app.state.store)
                roots = hermes_roots()
                for item in items:
                    cached = request.app.state.store.get_message(agent_id, session_id, item['id'])
                    if cached and cached.get('attachments'):
                        item['attachments'] = cached['attachments']
                    bind_hermes_refs(item, manager, roots)
                    request.app.state.store.upsert_message(item)
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
        codex = _codex(request, agent_id)
        if codex:
            return {'items': codex.models(session_id)}
        runtime = request.app.state.connections.get_runtime_by_agent_id(agent_id)
        if getattr(runtime, "daemon_owned", False):
            return {"items": runtime.models(session_id)}
        return {"items": model_choices(runtime_rpc(request.app.state.connections, agent_id))}
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.post("/api/v1/sessions/{session_id}/model")
def session_model(session_id: str, payload: SessionModelSelection, request: Request) -> dict[str, object]:
    _private(request)
    from .native_controls import runtime_rpc, set_session_model
    if request.app.state.store.get_session(payload.agent_id, session_id) is None:
        raise HTTPException(status_code=404, detail="Session was not found")
    try:
        codex = _codex(request, payload.agent_id)
        if codex:
            return codex.set_model(session_id, payload.provider, payload.model)
        runtime = request.app.state.connections.get_runtime_by_agent_id(payload.agent_id)
        if getattr(runtime, "daemon_owned", False):
            return runtime.set_model(session_id, payload.provider, payload.model)
        return set_session_model(runtime_rpc(request.app.state.connections, payload.agent_id), session_id, payload.provider, payload.model)
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.get("/api/v1/sessions/{session_id}/model")
def read_session_model(session_id: str, request: Request, agent_id: str = Query(..., min_length=1, max_length=256)) -> dict[str, object]:
    _private(request)
    from .native_controls import current_session_model, runtime_rpc
    if request.app.state.store.get_session(agent_id, session_id) is None:
        raise HTTPException(status_code=404, detail="Session was not found")
    try:
        codex = _codex(request, agent_id)
        if codex:
            binding = dict(codex.model(session_id))
            try:
                binding['effort'] = codex.current_effort(session_id)
            except ConnectionError:
                binding['effort'] = None
            return binding
        runtime = request.app.state.connections.get_runtime_by_agent_id(agent_id)
        if getattr(runtime, "daemon_owned", False):
            return runtime.model(session_id)
        rpc = runtime_rpc(request.app.state.connections, agent_id)
        binding = current_session_model(rpc, session_id)
        from .native_controls import current_session_reasoning
        binding['effort'] = current_session_reasoning(rpc, session_id)
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
        codex = _codex(request, payload.agent_id)
        if codex:
            return codex.set_effort(session_id, payload.effort)
        runtime = request.app.state.connections.get_runtime_by_agent_id(payload.agent_id)
        if getattr(runtime, "daemon_owned", False):
            return runtime.set_effort(session_id, payload.effort)
        return set_session_reasoning(runtime_rpc(request.app.state.connections, payload.agent_id), session_id, payload.effort)
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.get("/api/v1/sessions/{session_id}/approval-mode")
def session_approval_mode(session_id: str, request: Request, agent_id: str = Query(..., min_length=1)) -> dict[str, object]:
    _private(request)
    if request.app.state.store.get_session(agent_id, session_id) is None:
        raise HTTPException(status_code=404, detail="Session was not found")
    try:
        codex = _codex(request, agent_id)
        if codex:
            return {"mode": codex.get_approval_mode(session_id)}
        runtime = request.app.state.connections.get_runtime_by_agent_id(agent_id)
        if getattr(runtime, "daemon_owned", False):
            return {"mode": runtime.get_approval_mode(session_id)}
        from .native_controls import current_session_approval_mode, runtime_rpc
        return {"mode": current_session_approval_mode(runtime_rpc(request.app.state.connections, agent_id), session_id)}
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None


@router.post("/api/v1/sessions/{session_id}/approval-mode")
def session_approval_mode_select(session_id: str, payload: SessionApprovalModeSelection, request: Request) -> dict[str, object]:
    _private(request)
    if request.app.state.store.get_session(payload.agent_id, session_id) is None:
        raise HTTPException(status_code=404, detail="Session was not found")
    try:
        codex = _codex(request, payload.agent_id)
        if codex:
            return codex.set_approval_mode(session_id, payload.mode)
        runtime = request.app.state.connections.get_runtime_by_agent_id(payload.agent_id)
        if getattr(runtime, "daemon_owned", False):
            return runtime.set_approval_mode(session_id, payload.mode)
        from .native_controls import runtime_rpc, set_session_approval_mode
        return set_session_approval_mode(runtime_rpc(request.app.state.connections, payload.agent_id), session_id, payload.mode)
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


@router.post("/api/v1/commands")
async def create_command(payload: CommandSubmission, request: Request) -> JSONResponse:
    _private(request)
    try:
        command = await request.app.state.service.submit_browser_command(payload.model_dump())
    except CommandRejected as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None
    return JSONResponse(command)


@router.post("/api/v1/attachments", status_code=201)
async def upload_attachment(request: Request, file: UploadFile = File(...)) -> dict[str, str]:  # noqa: B008
    _private(request)
    try:
        return await request.app.state.attachments.save(file)
    except AttachmentError as exc:
        raise HTTPException(status_code=exc.status_code, detail=str(exc)) from None


@router.get("/api/v1/attachments/{attachment_id}")
def download_attachment(attachment_id: str, request: Request) -> FileResponse:
    _private(request)
    result = request.app.state.attachments.path_for(attachment_id)
    if result is None:
        raise HTTPException(status_code=404, detail="Attachment was not found")
    path, metadata = result
    return FileResponse(
        path,
        media_type=str(metadata["media_type"]),
        filename=str(metadata["name"]),
    )


@router.get("/api/v1/files/tree")
def get_files_tree(
    request: Request,
    path: str = Query(default=""),
    depth: int = Query(default=3),
    session_id: str = Query(default=""),
    connection_id: str = Query(default=""),
) -> dict[str, object]:
    _private(request)
    import json, urllib.parse
    cleaned_path = urllib.parse.unquote(path).strip().strip('<>').strip('"\'')

    # 优先根据 session_id 或 connection_id 判断是否为 SSH 远程项目
    resolved_cid = connection_id
    if not resolved_cid and session_id:
        try:
            sess = request.app.state.store.find_session_by_id(session_id)
            if sess and sess.get("connection_id") and sess["connection_id"] != "local":
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
import json, os
from pathlib import Path

def walk(p, depth={max(1, min(depth, 5))}):
    if depth <= 0: return []
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
    except Exception:
        pass
    return items

raw_target = {repr(cleaned_path)} or os.path.expanduser('~')
target = Path(raw_target).expanduser().resolve()
if not target.exists() or not target.is_dir():
    import sys
    sys.exit(44)

print(json.dumps({{'root': str(target), 'name': target.name or str(target), 'items': walk(target, {depth})}}))
"""
        cmd = build_remote_python_command(remote_script)
        try:
            res = subprocess.run(
                argv + [cmd],
                capture_output=True,
                text=True,
                timeout=15,
                check=False,
                creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0),
            )
            if res.returncode == 44:
                raise HTTPException(status_code=404, detail="远程工作区目录不存在")
            if res.returncode != 0:
                raise HTTPException(status_code=502, detail="远程执行目录树提取失败")
            output_lines = [line.strip() for line in res.stdout.strip().splitlines() if line.strip()]
            for line in reversed(output_lines):
                try:
                    return json.loads(line)
                except Exception:
                    continue
            raise HTTPException(status_code=502, detail="远程目录树解析失败")
        except subprocess.TimeoutExpired:
            raise HTTPException(status_code=504, detail="读取远程目录树超时")
        except HTTPException:
            raise
        except Exception as exc:
            raise HTTPException(status_code=500, detail=f"获取远程文件树失败: {exc}")

    if not cleaned_path:
        cleaned_path = os.getcwd()

    try:
        root = Path(cleaned_path).resolve()
    except (OSError, ValueError):
        raise HTTPException(status_code=400, detail="Invalid directory path")

    if not root.exists() or not root.is_dir():
        raise HTTPException(status_code=404, detail="Directory not found")

    def walk_tree(current_dir: Path, current_depth: int) -> list[dict[str, object]]:
        if current_depth <= 0:
            return []
        items = []
        try:
            # 过滤掉常见大体积或缓存目录
            ignored = {".git", ".venv", "node_modules", "__pycache__", ".pytest_cache", ".ruff_cache", "dist"}
            entries = sorted(list(current_dir.iterdir()), key=lambda x: (not x.is_dir(), x.name.lower()))
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

    return {
        "root": str(root),
        "name": root.name or str(root),
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
    import urllib.parse

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

    ignored = {".git", ".venv", "node_modules", "__pycache__", ".pytest_cache", ".ruff_cache", "dist", ".idea", ".vscode"}

    if resolved_cid and resolved_cid != "local":
        ssh_conn = request.app.state.store.get_ssh_connection(resolved_cid)
        if not ssh_conn:
            raise HTTPException(status_code=404, detail="SSH 连接不存在")
        from .ssh_transport import SshNativeRuntime, build_remote_python_command
        import json as _json
        runtime = SshNativeRuntime(ssh_conn["settings"], ssh_conn["id"], 0, None, None, connector_secret=None)
        argv = [a for a in runtime._base_ssh_argv() if a != "-T"] + ["-o", "BatchMode=yes", runtime._target()]
        remote_script = f"""
import json, os
from pathlib import Path
query = {_json.dumps(query)}
limit = {limit}
ignored = {{"{ '", "'.join(sorted(ignored)) }"}}
results = []
root = Path({_json.dumps(cleaned_path) or "os.path.expanduser('~')"}).expanduser().resolve()
for dirpath, dirnames, filenames in os.walk(root):
    dirnames[:] = [d for d in dirnames if d not in ignored]
    for name in filenames:
        if len(results) >= limit:
            break
        if not query or query in name.lower():
            full = Path(dirpath) / name
            try:
                results.append({{"path": str(full), "name": name, "size": full.stat().st_size}})
            except Exception:
                results.append({{"path": str(full), "name": name, "size": 0}})
    if len(results) >= limit:
        break
print(json.dumps({{"root": str(root), "items": results}}))
"""
        cmd = build_remote_python_command(remote_script)
        try:
            res = subprocess.run(argv + [cmd], capture_output=True, text=True, timeout=15, check=False, creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0))
            output_lines = [line.strip() for line in res.stdout.strip().splitlines() if line.strip()]
            for line in reversed(output_lines):
                try:
                    return _json.loads(line)
                except Exception:
                    continue
            raise HTTPException(status_code=502, detail="远程文件搜索解析失败")
        except subprocess.TimeoutExpired:
            raise HTTPException(status_code=504, detail="远程文件搜索超时")

    if not cleaned_path:
        cleaned_path = os.getcwd()
    try:
        root = Path(cleaned_path).resolve()
    except (OSError, ValueError):
        raise HTTPException(status_code=400, detail="Invalid directory path")
    if not root.is_dir():
        raise HTTPException(status_code=404, detail="Directory not found")

    results: list[dict[str, object]] = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in ignored]
        for name in filenames:
            if len(results) >= limit:
                break
            if not query or query in name.lower():
                full = Path(dirpath) / name
                try:
                    results.append({"path": str(full), "name": name, "size": full.stat().st_size})
                except Exception:
                    results.append({"path": str(full), "name": name, "size": 0})
        if len(results) >= limit:
            break
    return {"root": str(root), "items": results}


@router.get("/api/v1/files/raw")
def get_raw_file(
    request: Request,
    path: str = Query(...),
    download: bool = Query(default=False),
    session_id: str = Query(default=""),
    connection_id: str = Query(default=""),
) -> Response:
    _private(request)
    import mimetypes, urllib.parse
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
                timeout=15,
                check=False,
                creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0),
                startupinfo=_windows_hide_startupinfo(),
            )
            if res.returncode == 44:
                raise HTTPException(status_code=404, detail="远程文件不存在")
            if res.returncode != 0:
                raise HTTPException(status_code=502, detail="读取远程文件失败")
            file_bytes = res.stdout
            file_name = cleaned_path.rstrip("/").split("/")[-1] or "file"
            media_type, _ = mimetypes.guess_type(file_name)
            headers = {}
            if download:
                headers["Content-Disposition"] = f'attachment; filename="{file_name}"'
            return Response(
                content=file_bytes,
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


def _run_git(args: list[str], cwd: str, connection_id: str | None = None, app_state = None) -> tuple[int, str, str]:
    """在本地或远程 SSH 环境中执行 git 命令"""
    import subprocess, shutil, shlex
    if connection_id and connection_id != "local" and app_state:
        controller = getattr(app_state, "environments", None) or getattr(app_state, "connections", None)
        conn = None
        if controller:
            all_conns = controller.snapshot().get("connections", [])
            for c in all_conns:
                if str(c.get("id")) == str(connection_id):
                    conn = c
                    break
        if conn and conn.get("kind") == "ssh":
            host = conn.get("host") or "127.0.0.1"
            port = int(conn.get("port") or 22)
            user = conn.get("user") or "root"
            from .ssh_transport import _resolve_best_ssh_executable
            ssh_bin = _resolve_best_ssh_executable() or "ssh"
            inner_cmd = f"cd {shlex.quote(cwd)} && git " + " ".join(shlex.quote(a) for a in args)
            cmd = [
                ssh_bin,
                "-o", "BatchMode=yes",
                "-o", "StrictHostKeyChecking=accept-new",
                "-p", str(port),
                "-l", user,
                host,
                inner_cmd,
            ]
            from .connections import _windows_hide_flags, _windows_hide_startupinfo
            proc = subprocess.run(
                cmd,
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace",
                creationflags=_windows_hide_flags(),
                startupinfo=_windows_hide_startupinfo(),
            )
            return proc.returncode, proc.stdout, proc.stderr

    # 本地执行
    git_bin = shutil.which("git") or "git"
    from .connections import _windows_hide_flags, _windows_hide_startupinfo
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
async def get_git_status(request: Request, workspace: str | None = None, session_id: str | None = None, connection_id: str | None = None) -> dict[str, object]:
    _private(request)
    import os, re
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
    rc, stdout, stderr = _run_git(["branch", "--no-color"], cwd=cwd, connection_id=cid, app_state=request.app.state)
    current_branch = "master"
    branches = []
    if rc == 0 and stdout:
        for line in stdout.splitlines():
            clean = line.strip()
            if not clean:
                continue
            if clean.startswith("*"):
                name = clean[1:].strip().replace("(HEAD detached at ", "").replace(")", "")
                current_branch = name
                branches.append(name)
            else:
                branches.append(clean)
    else:
        # 尝试 symbolic-ref
        rc_sym, stdout_sym, _ = _run_git(["symbolic-ref", "--short", "HEAD"], cwd=cwd, connection_id=cid, app_state=request.app.state)
        if rc_sym == 0 and stdout_sym.strip():
            current_branch = stdout_sym.strip()
            branches = [current_branch]

    # 2. 查询短 diff 统计：git diff --stat HEAD 或 git diff --stat (工作区 + 暂存区)
    rc_diff, stdout_diff, _ = _run_git(["diff", "--stat"], cwd=cwd, connection_id=cid, app_state=request.app.state)
    # 也把暂存区的一并加总
    rc_staged, stdout_staged, _ = _run_git(["diff", "--cached", "--stat"], cwd=cwd, connection_id=cid, app_state=request.app.state)
    
    combined_diff_stat = (stdout_diff or "") + "\n" + (stdout_staged or "")
    insertions = 0
    deletions = 0
    changed_files = 0
    for line in combined_diff_stat.splitlines():
        # 匹配: 45 files changed, 4141 insertions(+), 321 deletions(-)
        m_ins = re.search(r"(\d+)\s+insertion", line)
        if m_ins:
            insertions += int(m_ins.group(1))
        m_del = re.search(r"(\d+)\s+deletion", line)
        if m_del:
            deletions += int(m_del.group(1))
        m_files = re.search(r"(\d+)\s+file", line)
        if m_files:
            changed_files += int(m_files.group(1))

    # 3. 获取未跟踪文件数与修改列表
    rc_status, stdout_status, _ = _run_git(["status", "--porcelain"], cwd=cwd, connection_id=cid, app_state=request.app.state)
    modified_files = []
    if rc_status == 0 and stdout_status:
        for line in stdout_status.splitlines():
            line_str = line.strip()
            if not line_str:
                continue
            status_code = line[:2].strip()
            path_str = line[3:].strip()
            modified_files.append({"status": status_code, "path": path_str})

    return {
        "branch": current_branch,
        "branches": branches if branches else [current_branch],
        "insertions": insertions,
        "deletions": deletions,
        "changed_files": len(modified_files),
        "files": modified_files,
        "workspace": cwd,
    }


@router.post("/api/v1/git/branch")
async def switch_or_create_branch(request: Request) -> dict[str, object]:
    _private(request)
    import os
    try:
        payload = await request.json()
    except Exception:
        raise HTTPException(status_code=400, detail="Invalid JSON")

    branch_name = str(payload.get("branch") or "").strip()
    create_new = bool(payload.get("create", False))
    workspace = payload.get("workspace") or os.getcwd()
    session_id = payload.get("session_id")
    connection_id = payload.get("connection_id")

    if not branch_name:
        raise HTTPException(status_code=400, detail="Branch name is required")

    cid = connection_id
    if session_id and (not workspace or not cid):
        try:
            sess = request.app.state.store.find_session_by_id(session_id)
            if sess:
                if not workspace and sess.get("workspace"):
                    workspace = sess["workspace"]
                if not cid and sess.get("connection_id") and sess["connection_id"] != "local":
                    cid = sess["connection_id"]
        except Exception:
            pass

    args = ["checkout", "-b", branch_name] if create_new else ["checkout", branch_name]
    rc, stdout, stderr = _run_git(args, cwd=workspace, connection_id=cid, app_state=request.app.state)
    if rc != 0:
        detail = stderr.strip() or stdout.strip() or f"Git checkout exited with code {rc}"
        raise HTTPException(status_code=400, detail=detail)

    return {"status": "ok", "current_branch": branch_name}


@router.get("/api/v1/git/diff-raw")
async def get_git_diff_raw(request: Request, path: str | None = None, workspace: str | None = None, session_id: str | None = None, connection_id: str | None = None) -> Response:
    _private(request)
    import os
    from fastapi.responses import Response
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

    args = ["diff"]
    if path:
        args.extend(["--", path])

    rc, stdout, _ = _run_git(args, cwd=cwd, connection_id=cid, app_state=request.app.state)
    # 如果工作区没有未暂存 diff，尝试 git diff --cached
    if not stdout.strip():
        args_cached = ["diff", "--cached"]
        if path:
            args_cached.extend(["--", path])
        rc_c, stdout_c, _ = _run_git(args_cached, cwd=cwd, connection_id=cid, app_state=request.app.state)
        if stdout_c.strip():
            stdout = stdout_c

    # 如果是新建未跟踪文件，输出模拟的全文添加 diff
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
        if sys.platform == "win32":
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


@router.get("/api/v1/model-routing")
def model_routing_status(request: Request) -> dict[str, object]:
    _private(request)
    from .timeutil import utc_now

    settings = request.app.state.settings
    now = utc_now()
    day_start = now.replace(hour=0, minute=0, second=0, microsecond=0)
    return {
        "enabled": settings.model_routing_enabled,
        "daily_budget_units": settings.model_routing_daily_budget_units,
        "small_cost_units": settings.model_routing_small_cost_units,
        "large_cost_units": settings.model_routing_large_cost_units,
        "large_text_threshold": settings.model_routing_large_text_threshold,
        "usage": request.app.state.store.model_routing_usage(day_start),
    }


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
