from __future__ import annotations

import hmac
from typing import Any

from fastapi import HTTPException, Request, WebSocket

from .config import Settings

COOKIE_NAME = "astrorder_session"


def validate_origin(origin: str | None, settings: Settings) -> None:
    if origin and origin not in settings.allowed_origins:
        raise HTTPException(status_code=403, detail="Origin is not allowed")


def validate_websocket_origin(origin: str | None, settings: Settings) -> bool:
    return not origin or origin in settings.allowed_origins


def _bearer(headers: Any) -> str | None:
    value = headers.get("authorization")
    if not value or not value.startswith("Bearer "):
        return None
    token = value[7:]
    return token if token else None


def _matches(token: str | None, secret: str | None) -> bool:
    return bool(secret and token and hmac.compare_digest(token, secret))


def require_browser(request: Request, settings: Settings) -> None:
    if not settings.browser_secret:
        raise HTTPException(status_code=503, detail="Authentication is not configured")
    validate_origin(request.headers.get("origin"), settings)
    token = request.cookies.get(COOKIE_NAME) or _bearer(request.headers)
    if not _matches(token, settings.browser_secret):
        raise HTTPException(status_code=401, detail="Authentication required")


def browser_authenticated(request: Request, settings: Settings) -> bool:
    if not settings.browser_secret:
        return False
    token = request.cookies.get(COOKIE_NAME) or _bearer(request.headers)
    return _matches(token, settings.browser_secret)


def authorize_browser_websocket(websocket: WebSocket, settings: Settings) -> bool:
    if not settings.browser_secret:
        return False
    if not validate_websocket_origin(websocket.headers.get("origin"), settings):
        return False
    token = websocket.cookies.get(COOKIE_NAME) or _bearer(websocket.headers)
    return _matches(token, settings.browser_secret)


def authorize_connector_websocket(websocket: WebSocket, settings: Settings) -> bool:
    if not settings.connector_secret:
        return False
    if not validate_websocket_origin(websocket.headers.get("origin"), settings):
        return False
    return _matches(_bearer(websocket.headers), settings.connector_secret)
