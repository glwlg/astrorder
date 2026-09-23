from __future__ import annotations

import asyncio
from fastapi import APIRouter, HTTPException, Query, Request
from pydantic import BaseModel, Field

router = APIRouter()


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


def _private(request: Request) -> None:
    from ..core.auth import require_browser
    require_browser(request, request.app.state.settings)


@router.get("/api/v1/browser/screenshot")
async def browser_screenshot(
    request: Request,
    agent_id: str = Query(..., min_length=1, max_length=256),
    session_id: str = Query(..., min_length=1, max_length=256),
    refresh: bool = False,
    target_id: str | None = Query(None),
) -> dict[str, object]:
    _private(request)
    from ..jev.browser import capture_browser_screenshot, latest_browser_screenshot

    if request.app.state.store.get_session(agent_id, session_id) is None:
        raise HTTPException(status_code=404, detail="Session not found")
    session_key = f"{agent_id}::{session_id}"
    try:
        snapshot = latest_browser_screenshot(session_key, target_id=target_id)
        if refresh:
            try:
                snapshot = await asyncio.to_thread(capture_browser_screenshot, session_key, require_owner=True, target_id=target_id)
            except RuntimeError:
                if snapshot is None:
                    raise
        if snapshot is None:
            raise HTTPException(status_code=404, detail="该会话还没有浏览器画面")
        return snapshot
    except RuntimeError as exc:
        raise HTTPException(status_code=409, detail=str(exc)) from None


@router.post("/api/v1/browser/navigate")
async def browser_navigate(payload: BrowserNavigateRequest, request: Request) -> dict[str, object]:
    _private(request)
    if request.app.state.store.get_session(payload.agent_id, payload.session_id) is None:
        raise HTTPException(status_code=404, detail="Session not found")
    from ..jev.browser import navigate_browser

    session_key = f"{payload.agent_id}::{payload.session_id}"
    try:
        snapshot = await asyncio.to_thread(navigate_browser, session_key=session_key, url=payload.url)
    except (RuntimeError, ValueError) as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from None
    request.app.state.service._server_event(
        "browser.mirror.updated",
        agent_id=payload.agent_id,
        session_id=payload.session_id,
        data={
            "revision": snapshot["revision"],
            "url": snapshot["url"],
            "title": snapshot["title"],
            "session_key": session_key,
        },
    )
    return snapshot


@router.post("/api/v1/browser/interact")
async def browser_interact(payload: BrowserInteractRequest, request: Request) -> dict[str, object]:
    _private(request)
    if request.app.state.store.get_session(payload.agent_id, payload.session_id) is None:
        raise HTTPException(status_code=404, detail="Session not found")
    from ..jev.browser import interact_browser

    session_key = f"{payload.agent_id}::{payload.session_id}"
    try:
        snapshot = await asyncio.to_thread(
            interact_browser,
            session_key=session_key,
            action=payload.action,
            target_id=payload.target_id,
            x=payload.x,
            y=payload.y,
            ratio_x=payload.ratio_x,
            ratio_y=payload.ratio_y,
            delta_y=payload.delta_y,
            text=payload.text,
        )
    except (RuntimeError, ValueError) as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from None
    request.app.state.service._server_event(
        "browser.mirror.updated",
        agent_id=payload.agent_id,
        session_id=payload.session_id,
        data={
            "revision": snapshot["revision"],
            "url": snapshot["url"],
            "title": snapshot["title"],
            "session_key": session_key,
        },
    )
    return snapshot


@router.get("/api/v1/browser/page-content")
async def browser_page_content(
    request: Request,
    agent_id: str = Query(..., min_length=1, max_length=256),
    session_id: str = Query(..., min_length=1, max_length=256),
    target_id: str | None = Query(None),
) -> dict[str, object]:
    _private(request)
    if request.app.state.store.get_session(agent_id, session_id) is None:
        raise HTTPException(status_code=404, detail="Session not found")
    from ..jev.browser import get_browser_page_content

    session_key = f"{agent_id}::{session_id}"
    try:
        return await asyncio.to_thread(get_browser_page_content, session_key, target_id=target_id)
    except RuntimeError as exc:
        raise HTTPException(status_code=409, detail=str(exc)) from None


@router.get("/api/v1/browser/diagnostics")
async def browser_diagnostics(
    request: Request,
    agent_id: str = Query(..., min_length=1, max_length=256),
    session_id: str = Query(..., min_length=1, max_length=256),
) -> dict[str, object]:
    _private(request)
    if request.app.state.store.get_session(agent_id, session_id) is None:
        raise HTTPException(status_code=404, detail="Session not found")
    from ..jev.browser import get_browser_diagnostics

    session_key = f"{agent_id}::{session_id}"
    return await asyncio.to_thread(get_browser_diagnostics, session_key)


@router.post("/api/v1/browser/tabs/select")
async def browser_tab_select(payload: BrowserTabRequest, request: Request) -> dict[str, object]:
    _private(request)
    if not payload.target_id:
        raise HTTPException(status_code=400, detail="target_id is required")
    if request.app.state.store.get_session(payload.agent_id, payload.session_id) is None:
        raise HTTPException(status_code=404, detail="Session not found")
    from ..jev.browser import select_browser_tab

    session_key = f"{payload.agent_id}::{payload.session_id}"
    try:
        snapshot = await asyncio.to_thread(select_browser_tab, session_key=session_key, target_id=payload.target_id)
    except (RuntimeError, ValueError) as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from None
    request.app.state.service._server_event(
        "browser.mirror.updated",
        agent_id=payload.agent_id,
        session_id=payload.session_id,
        data={
            "revision": snapshot["revision"],
            "url": snapshot["url"],
            "title": snapshot["title"],
            "session_key": session_key,
        },
    )
    return snapshot


@router.post("/api/v1/browser/tabs/new")
async def browser_tab_new(payload: BrowserTabRequest, request: Request) -> dict[str, object]:
    _private(request)
    if request.app.state.store.get_session(payload.agent_id, payload.session_id) is None:
        raise HTTPException(status_code=404, detail="Session not found")
    from ..jev.browser import new_browser_tab

    session_key = f"{payload.agent_id}::{payload.session_id}"
    try:
        snapshot = await asyncio.to_thread(new_browser_tab, session_key=session_key, url=payload.url or "about:blank")
    except (RuntimeError, ValueError) as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from None
    request.app.state.service._server_event(
        "browser.mirror.updated",
        agent_id=payload.agent_id,
        session_id=payload.session_id,
        data={
            "revision": snapshot["revision"],
            "url": snapshot["url"],
            "title": snapshot["title"],
            "session_key": session_key,
        },
    )
    return snapshot


@router.post("/api/v1/browser/tabs/close")
async def browser_tab_close(payload: BrowserTabRequest, request: Request) -> dict[str, object]:
    _private(request)
    if not payload.target_id:
        raise HTTPException(status_code=400, detail="target_id is required")
    if request.app.state.store.get_session(payload.agent_id, payload.session_id) is None:
        raise HTTPException(status_code=404, detail="Session not found")
    from ..jev.browser import close_browser_tab

    session_key = f"{payload.agent_id}::{payload.session_id}"
    try:
        snapshot = await asyncio.to_thread(close_browser_tab, session_key=session_key, target_id=payload.target_id)
    except (RuntimeError, ValueError) as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from None
    request.app.state.service._server_event(
        "browser.mirror.updated",
        agent_id=payload.agent_id,
        session_id=payload.session_id,
        data={
            "revision": snapshot["revision"],
            "url": snapshot["url"],
            "title": snapshot["title"],
            "session_key": session_key,
        },
    )
    return snapshot
