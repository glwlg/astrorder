"""Build and start the production Session Daemon from public settings."""
from __future__ import annotations

import os
from collections.abc import Callable, Mapping
from urllib.parse import urlparse

from daemon_service import start_daemon

def production_daemon_spec(
    environment: Mapping[str, str]
) -> int:
    endpoint = environment.get("ASTRORDER_SESSION_DAEMON_ENDPOINT", "ws://127.0.0.1:30009")
    parsed = urlparse(endpoint)
    if parsed.scheme != "ws" or parsed.hostname not in {"127.0.0.1", "localhost", "::1"}:
        raise ValueError("production Session Daemon endpoint must be loopback WebSocket")
    if parsed.path not in {"", "/"} or parsed.query or parsed.fragment or parsed.username:
        raise ValueError("production Session Daemon endpoint must not contain path or credentials")
    port = parsed.port if parsed.port is not None else 80
    if not 1 <= port <= 65535:
        raise ValueError("production Session Daemon port is invalid")

    # Runtime configuration belongs to ASTRORDER_SESSION_DAEMON_CONFIG in Go.
    return port


def ensure_production_daemon(
    environment: Mapping[str, str],
    *,
    starter: Callable[..., int] = start_daemon,
) -> int:
    if not os.environ.get("ASTRORDER_SESSION_DAEMON_SECRET"):
        raise RuntimeError("ASTRORDER_SESSION_DAEMON_SECRET is required")
    return starter(port=production_daemon_spec(environment))
