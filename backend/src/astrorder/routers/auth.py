from __future__ import annotations

import hmac
from fastapi import APIRouter, HTTPException, Request
from fastapi.responses import JSONResponse, Response

from ..core.auth import COOKIE_NAME, browser_authenticated, validate_origin
from ..config import Settings
from ..schemas import AuthRequest

router = APIRouter()


def _settings(request: Request) -> Settings:
    return request.app.state.settings


def _set_browser_cookie(response: Response, token: str) -> None:
    response.set_cookie(
        COOKIE_NAME,
        token,
        httponly=True,
        samesite="lax",
        secure=False,
        max_age=60 * 60 * 24 * 30,
        path="/",
    )


@router.post("/api/v1/auth/session")
async def create_auth_session(payload: AuthRequest, request: Request) -> JSONResponse:
    settings = _settings(request)
    if not settings.browser_secret:
        raise HTTPException(status_code=503, detail="Authentication is not configured")
    validate_origin(request.headers.get("origin"), settings, request.headers.get("host"), getattr(request.app.state, "store", None))

    if not hmac.compare_digest(payload.token, settings.browser_secret):
        raise HTTPException(status_code=401, detail="Invalid authentication token")
    response = JSONResponse({"authenticated": True})
    _set_browser_cookie(response, payload.token)
    return response


@router.get("/api/v1/auth/session")
def get_auth_session(request: Request, response: Response) -> dict[str, bool]:
    settings = _settings(request)
    bearer = request.headers.get("authorization", "")
    if bearer.startswith("Bearer ") and settings.browser_secret and hmac.compare_digest(bearer[7:], settings.browser_secret):
        _set_browser_cookie(response, bearer[7:])
        return {"authenticated": True}
    return {"authenticated": browser_authenticated(request, settings)}


@router.delete("/api/v1/auth/session", status_code=204)
def delete_auth_session(request: Request) -> Response:
    settings = _settings(request)
    validate_origin(request.headers.get("origin"), settings, request.headers.get("host"), getattr(request.app.state, "store", None))
    response = Response(status_code=204)
    response.delete_cookie(COOKIE_NAME, path="/")
    return response
