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
    store = request.app.state.store
    return {
        "protocol_version": 1,
        "agents": store.list_agents(),
        "projects": store.list_projects(),
        "sessions": store.sessions_for_bootstrap(),
        "cursor": store.latest_cursor(),
    }


@router.get("/api/v1/agents")
def agents(request: Request) -> dict[str, object]:
    _private(request)
    return {"items": request.app.state.store.list_agents()}


@router.get('/api/v1/user-activity')
async def user_activity(request: Request):
    _private(request)
    from .native_user_activity import read_user_activity
    controller = request.app.state.connections
    store = request.app.state.store
    sources = [(controller.local.snapshot().get('agent_id'), controller.local)]
    sources += [(runtime.agent_id, runtime) for runtime in controller._ssh_runtimes.values()]
    def read(source):
        agent_id, runtime = source
        if not agent_id:
            return []
        ids = [s['id'] for s in store.list_sessions(agent_id)]
        try:
            if runtime is controller.local:
                rows = read_user_activity(runtime._profile_plugins_dir.parent / 'state.db', ids)
            else:
                rows = runtime.read_user_activity(ids)
            return [{**row, 'agent_id': agent_id} for row in rows]
        except (OSError, ValueError, sqlite3.Error, ConnectionError, subprocess.TimeoutExpired):
            return []
    batches = await asyncio.gather(*(asyncio.to_thread(read, source) for source in sources))
    return {'items': [item for batch in batches for item in batch]}


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
    return {
        "known_agent_ids": [agent_id for agent_id, ids in results if ids is not None],
        "items": [{"agent_id": agent_id, "id": session_id} for agent_id, ids in results if ids is not None for session_id in ids],
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
                items = project_history_messages(page['items'], durable_session_id=session_id, native_session_id=session_id, source_id=session.get('source_id') or agent_id, agent_id=agent_id)
                for item in items:
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
            return codex.model(session_id)
        return current_session_model(runtime_rpc(request.app.state.connections, agent_id), session_id)
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
