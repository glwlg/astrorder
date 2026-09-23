from __future__ import annotations

import hmac
from fastapi import APIRouter, HTTPException, Request
from fastapi.responses import JSONResponse, Response

from ..auth import COOKIE_NAME, browser_authenticated, validate_origin
from ..config import Settings
from ..schemas import AuthRequest

router = APIRouter()


def _settings(request: Request) -> Settings:
    return request.app.state.settings


@router.post("/api/v1/auth/session")
async def create_auth_session(payload: AuthRequest, request: Request) -> JSONResponse:
    settings = _settings(request)
    if not settings.browser_secret:
        raise HTTPException(status_code=503, detail="Authentication is not configured")
    validate_origin(request.headers.get("origin"), settings, request.headers.get("host"), getattr(request.app.state, "store", None))

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
    validate_origin(request.headers.get("origin"), settings, request.headers.get("host"), getattr(request.app.state, "store", None))
    response = Response(status_code=204)
    response.delete_cookie(COOKIE_NAME, path="/")
    return response
