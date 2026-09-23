from __future__ import annotations

from typing import Any
from fastapi import APIRouter, HTTPException, Request

from ..daemon.bridge import DaemonBridgeError

router = APIRouter()


def _private(request: Request) -> None:
    from ..core.auth import require_browser
    require_browser(request, request.app.state.settings)


@router.get("/api/v1/codex-desktop/status")
async def codex_desktop_status(request: Request) -> dict[str, object]:
    _private(request)
    bridge = request.app.state.daemon_bridge
    if bridge is None:
        raise HTTPException(status_code=503, detail="Session Daemon is unavailable")
    try:
        await bridge.refresh_status()
    except DaemonBridgeError as exc:
        raise HTTPException(status_code=503, detail=str(exc)) from None
    status = bridge.runtime_status.get("codex", {}).get("desktop_cdp")
    if not isinstance(status, dict):
        return {"available": False, "detail": "Codex Desktop control is not configured"}
    return status
