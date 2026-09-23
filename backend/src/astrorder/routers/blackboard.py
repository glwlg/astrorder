from __future__ import annotations

from typing import Any
from fastapi import APIRouter, Query, Request

from ..agent_gateway import AgentContext, invoke
from ..auth import require_browser

router = APIRouter()


def _settings(request: Request):
    return request.app.state.settings


def _agent_context(request: Request) -> AgentContext:
    from ..agent_gateway import AgentContext
    return AgentContext(
        settings=_settings(request),
        store=request.app.state.store,
        service=request.app.state.service,
        connections=getattr(request.app.state, "connections", None),
        daemon_bridge=getattr(request.app.state, "daemon_bridge", None),
    )


@router.get("/api/v1/blackboard")
def blackboard_list(request: Request, namespace: str = Query(default="global", max_length=128)) -> dict[str, object]:
    require_browser(request, _settings(request))
    return invoke("blackboard.get", {"namespace": namespace}, _agent_context(request))
