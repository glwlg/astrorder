from __future__ import annotations

import asyncio
import sqlite3
import subprocess

from fastapi import APIRouter, File, HTTPException, Query, Request, UploadFile
from fastapi.responses import FileResponse, JSONResponse, Response
from pydantic import BaseModel, Field

from .attachments import AttachmentError
from .auth import COOKIE_NAME, browser_authenticated, require_browser, validate_origin
from .connections import ConnectionError
from .schemas import AuthRequest, CommandSubmission, RuntimeLaunch, SshConnectionSettings
from .service import CommandRejected
from pathlib import Path

router = APIRouter()


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
        data = codex.create(payload.workspace, payload.title) if codex else request.app.state.connections.create_session_for_agent(
            agent_id=payload.agent_id,
            workspace=payload.workspace,
            title=payload.title,
        )
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
        logger.exception("Failed to create session: %s", exc)
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
    limit: int = Query(default=2, ge=1, le=200),
) -> dict[str, object]:
    _private(request)
    if request.app.state.store.get_session(agent_id, session_id) is None:
        raise HTTPException(status_code=404, detail="Session was not found")
    session = request.app.state.store.get_session(agent_id, session_id)
    if session is None:
        raise HTTPException(status_code=404, detail="Session was not found")
    codex = _codex(request, agent_id)
    if codex and (before is None or before.startswith('codex:')):
        try:
            return await asyncio.to_thread(codex.messages, session_id, before, limit)
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
                    else:
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
        return set_session_reasoning(runtime_rpc(request.app.state.connections, payload.agent_id), session_id, payload.effort)
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


@router.get("/api/v1/files/raw")
def get_raw_file(request: Request, path: str = Query(...), download: bool = Query(default=False)) -> FileResponse:
    _private(request)
    import mimetypes, urllib.parse
    cleaned_path = urllib.parse.unquote(path).strip().strip('<>').strip('"\'')
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
