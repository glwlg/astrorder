from __future__ import annotations

import hmac
import ipaddress
from typing import Any
from urllib.parse import urlparse

from fastapi import HTTPException, Request, WebSocket

from .config import Settings

COOKIE_NAME = "astrorder_session"


def is_allowed_origin(origin: str | None, settings: Settings, host: str | None = None, store: Any = None) -> bool:
    if not origin:
        return True
    if origin in settings.allowed_origins:
        return True
    norm_origin = origin.rstrip("/")
    if norm_origin in [o.rstrip("/") for o in settings.allowed_origins]:
        return True

    # Check dynamic origins from database preferences
    try:
        if store is not None:
            from .models import WorkspacePreferenceRow
            with store.session() as db:
                row = db.get(WorkspacePreferenceRow, "network:allowed_origins")
                if row and row.value:
                    val = row.value
                    if isinstance(val, list):
                        if origin in val or norm_origin in [str(o).rstrip("/") for o in val]:
                            return True
                pub_row = db.get(WorkspacePreferenceRow, "network:public_url")
                if pub_row and pub_row.value:
                    pub_val = str(pub_row.value).strip().rstrip("/")
                    if norm_origin == pub_val:
                        return True
        else:
            # Fallback to direct SQLite read from settings.database_url
            db_url = getattr(settings, "database_url", "")
            if db_url.startswith("sqlite:///"):
                db_path = db_url[len("sqlite:///"):]
                import sqlite3, json
                conn = sqlite3.connect(db_path)
                c = conn.cursor()
                c.execute("SELECT key, value FROM workspace_preferences WHERE key IN ('network:allowed_origins', 'network:public_url')")
                rows = dict(c.fetchall())
                conn.close()
                if "network:allowed_origins" in rows:
                    val = json.loads(rows["network:allowed_origins"]) if isinstance(rows["network:allowed_origins"], str) else rows["network:allowed_origins"]
                    if isinstance(val, list) and (origin in val or norm_origin in [str(o).rstrip("/") for o in val]):
                        return True
                if "network:public_url" in rows:
                    pval = json.loads(rows["network:public_url"]) if isinstance(rows["network:public_url"], str) else rows["network:public_url"]
                    if isinstance(pval, str) and norm_origin == pval.strip().rstrip("/"):
                        return True
    except Exception:
        pass

    try:
        parsed = urlparse(origin)
        if host and parsed.netloc.lower() == host.lower():
            return True
        if parsed.hostname:
            ip = ipaddress.ip_address(parsed.hostname)
            if (ip.is_private or ip.is_loopback) and (parsed.port == settings.port or parsed.port is None):
                return True
    except (ValueError, TypeError):
        pass
    return False


def validate_origin(origin: str | None, settings: Settings, host: str | None = None, store: Any = None) -> None:
    if not is_allowed_origin(origin, settings, host, store):
        raise HTTPException(status_code=403, detail="Origin is not allowed")


def validate_websocket_origin(origin: str | None, settings: Settings, host: str | None = None, store: Any = None) -> bool:
    return is_allowed_origin(origin, settings, host, store)


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
    validate_origin(request.headers.get("origin"), settings, request.headers.get("host"), getattr(request.app.state, "store", None))
    token = request.cookies.get(COOKIE_NAME) or _bearer(request.headers) or request.query_params.get("token")
    if not _matches(token, settings.browser_secret):
        raise HTTPException(status_code=401, detail="Authentication required")


def require_agent(request: Request, settings: Settings) -> str:
    token = request.cookies.get(COOKIE_NAME) or _bearer(request.headers) or request.query_params.get("token")
    if _matches(token, settings.connector_secret):
        return "connector"
    if _matches(token, settings.browser_secret):
        origin = request.headers.get("origin")
        if origin:
            validate_origin(origin, settings, request.headers.get("host"), getattr(request.app.state, "store", None))
        return "browser"
    if not settings.browser_secret and not settings.connector_secret:
        raise HTTPException(status_code=503, detail="Authentication is not configured")
    raise HTTPException(status_code=401, detail="Authentication required")


def browser_authenticated(request: Request, settings: Settings) -> bool:
    if not settings.browser_secret:
        return False
    token = request.cookies.get(COOKIE_NAME) or _bearer(request.headers) or request.query_params.get("token")
    return _matches(token, settings.browser_secret)


def authorize_browser_websocket(websocket: WebSocket, settings: Settings) -> bool:
    if not settings.browser_secret:
        return False
    if not validate_websocket_origin(websocket.headers.get("origin"), settings, websocket.headers.get("host"), getattr(websocket.app.state, "store", None)):
        return False
    token = websocket.cookies.get(COOKIE_NAME) or _bearer(websocket.headers)
    return _matches(token, settings.browser_secret)


def authorize_connector_websocket(websocket: WebSocket, settings: Settings) -> bool:
    if not settings.connector_secret:
        return False
    if not validate_websocket_origin(websocket.headers.get("origin"), settings):
        return False
    return _matches(_bearer(websocket.headers), settings.connector_secret)
