from __future__ import annotations

import asyncio
from fastapi import APIRouter, HTTPException, Request
from fastapi.responses import JSONResponse

from ..schemas import CommandSubmission
from ..service import CommandRejected

router = APIRouter()


def _private(request: Request) -> None:
    from ..core.auth import require_browser
    require_browser(request, request.app.state.settings)


@router.post("/api/v1/commands")
async def create_command(payload: CommandSubmission, request: Request) -> JSONResponse:
    _private(request)
    try:
        command = await request.app.state.service.submit_browser_command(payload.model_dump())
    except CommandRejected as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from None
    return JSONResponse(command)
