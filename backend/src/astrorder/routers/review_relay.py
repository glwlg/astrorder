from __future__ import annotations

from fastapi import APIRouter, Depends, HTTPException, Query, Request, Response
from pydantic import BaseModel, ConfigDict, Field

from astrorder.core.auth import require_browser
from astrorder.core.review_relay import WorkspaceSnapshot, capture_remote_workspace_snapshot, capture_workspace_snapshot, snapshots_match


class ReviewRelayBinding(BaseModel):
    model_config = ConfigDict(extra="forbid")

    review_agent_id: str = Field(min_length=1, max_length=256)
    review_session_id: str = Field(min_length=1, max_length=256)
    workspace: str = Field(min_length=1, max_length=4096)
    enabled: bool = True


class ReviewRelayRunCreate(BaseModel):
    model_config = ConfigDict(extra="forbid")

    source_agent_id: str = Field(min_length=1, max_length=256)
    source_session_id: str = Field(min_length=1, max_length=256)
    review_agent_id: str = Field(min_length=1, max_length=256)
    review_session_id: str = Field(min_length=1, max_length=256)
    command_id: str = Field(min_length=1, max_length=256)
    baseline_ids: list[str] = Field(default_factory=list, max_length=1000)
    status: str = Field(pattern="^(validating|reviewing|forwarding|draft_ready|empty|failed)$")


class ReviewRelayRunPatch(BaseModel):
    model_config = ConfigDict(extra="forbid")

    status: str | None = Field(default=None, pattern="^(validating|reviewing|forwarding|draft_ready|empty|failed)$")
    comment_text: str | None = Field(default=None, max_length=200_000)
    error: str | None = Field(default=None, max_length=2000)


class ReviewRelayValidation(BaseModel):
    model_config = ConfigDict(extra="forbid")

    source_workspace: str = Field(min_length=1, max_length=4096)
    review_workspace: str = Field(min_length=1, max_length=4096)
    source_agent_id: str | None = None
    source_session_id: str | None = None
    review_agent_id: str | None = None
    review_session_id: str | None = None


def authenticated(request: Request, response: Response) -> None:
    require_browser(request, request.app.state.settings)
    response.headers["Cache-Control"] = "no-store"


router = APIRouter(prefix="/api/v1/review-relay", dependencies=[Depends(authenticated)])


@router.get("/bindings/{source_agent_id}/{source_session_id}")
def get_binding(source_agent_id: str, source_session_id: str, request: Request):
    return request.app.state.store.get_review_relay_binding(source_agent_id, source_session_id)


@router.put("/bindings/{source_agent_id}/{source_session_id}")
def put_binding(source_agent_id: str, source_session_id: str, payload: ReviewRelayBinding, request: Request):
    return request.app.state.store.upsert_review_relay_binding({
        "source_agent_id": source_agent_id,
        "source_session_id": source_session_id,
        **payload.model_dump(),
    })


@router.delete("/bindings/{source_agent_id}/{source_session_id}", status_code=204)
def delete_binding(source_agent_id: str, source_session_id: str, request: Request):
    request.app.state.store.delete_review_relay_binding(source_agent_id, source_session_id)


def _snapshot_wire(snapshot: WorkspaceSnapshot) -> dict[str, str]:
    return {
        "workspace": str(snapshot.workspace),
        "repo_root": str(snapshot.repo_root),
        "branch": snapshot.branch,
        "head": snapshot.head,
        "change_fingerprint": snapshot.change_fingerprint,
    }


@router.post("/validate")
def validate_workspaces(payload: ReviewRelayValidation, request: Request):
    ids = (payload.source_agent_id, payload.source_session_id, payload.review_agent_id, payload.review_session_id)
    if any(ids) and not all(ids):
        raise HTTPException(status_code=422, detail="必须同时指定开发与审查会话")
    connection_id = None
    if all(ids):
        store = request.app.state.store
        source_session = store.get_session(payload.source_agent_id, payload.source_session_id)
        review_session = store.get_session(payload.review_agent_id, payload.review_session_id)
        source_agent = store.get_agent(payload.source_agent_id)
        review_agent = store.get_agent(payload.review_agent_id)
        if not source_session or not review_session or not source_agent or not review_agent:
            raise HTTPException(status_code=404, detail="开发或审查会话不存在")
        if source_session.get("workspace") != payload.source_workspace or review_session.get("workspace") != payload.review_workspace:
            raise HTTPException(status_code=409, detail="会话工作区已变化，请刷新后重试")
        source_connection = source_session.get("connection_id") or source_agent.get("connection_id") or "local"
        review_connection = review_session.get("connection_id") or review_agent.get("connection_id") or "local"
        if source_connection != review_connection:
            raise HTTPException(status_code=409, detail="开发与审查会话不在同一个运行环境")
        connection_id = source_connection
    try:
        if connection_id and connection_id != "local":
            connection = request.app.state.store.get_ssh_connection(connection_id)
            if not connection:
                raise HTTPException(status_code=422, detail="审查环境的 SSH 连接不存在")
            source = capture_remote_workspace_snapshot(payload.source_workspace, connection["settings"], connection_id)
            review = capture_remote_workspace_snapshot(payload.review_workspace, connection["settings"], connection_id)
        else:
            source = capture_workspace_snapshot(payload.source_workspace)
            review = capture_workspace_snapshot(payload.review_workspace)
    except ValueError as error:
        raise HTTPException(status_code=422, detail=str(error)) from error
    if not snapshots_match(source, review):
        raise HTTPException(status_code=409, detail={"source": _snapshot_wire(source), "review": _snapshot_wire(review)})
    return {"valid": True, "source": _snapshot_wire(source), "review": _snapshot_wire(review)}


@router.post("/runs")
def create_run(payload: ReviewRelayRunCreate, request: Request):
    return request.app.state.store.create_review_relay_run(payload.model_dump())


@router.get("/runs/{run_id}")
def get_run(run_id: str, request: Request):
    result = request.app.state.store.get_review_relay_run(run_id)
    if result is None:
        raise HTTPException(status_code=404, detail="review run not found")
    return result


@router.get("/runs")
def list_runs(
    request: Request,
    source_agent_id: str = Query(min_length=1, max_length=256),
    source_session_id: str = Query(min_length=1, max_length=256),
    active_only: bool = False,
):
    return {"items": request.app.state.store.list_review_relay_runs(source_agent_id, source_session_id, active_only=active_only)}


@router.patch("/runs/{run_id}")
def patch_run(run_id: str, payload: ReviewRelayRunPatch, request: Request):
    result = request.app.state.store.update_review_relay_run(
        run_id,
        {key: value for key, value in payload.model_dump().items() if value is not None},
    )
    if result is None:
        raise HTTPException(status_code=404, detail="review run not found")
    return result
