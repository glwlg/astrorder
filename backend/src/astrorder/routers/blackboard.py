from __future__ import annotations

from typing import Any
from fastapi import APIRouter, Query, Request

from ..agents.gateway import AgentContext, invoke
from ..core.auth import require_browser

router = APIRouter()


def _settings(request: Request):
    return request.app.state.settings


def _agent_context(request: Request) -> AgentContext:
    from ..agents.gateway import AgentContext
    from ..api import _agent_runtime

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


@router.get("/api/v1/blackboard")
def blackboard_list(request: Request, namespace: str = Query(default="global", max_length=128)) -> dict[str, object]:
    require_browser(request, _settings(request))
    return invoke("blackboard.get", {"namespace": namespace}, _agent_context(request))
