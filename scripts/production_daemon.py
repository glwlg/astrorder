"""Build and start the production Session Daemon from public settings."""
from __future__ import annotations

import os
from collections.abc import Callable, Mapping
from pathlib import Path
from urllib.parse import urlparse

from daemon_service import start_daemon

ROOT = Path(__file__).resolve().parent.parent


def _enabled(value: str | None) -> bool:
    return bool(value and value.strip().casefold() in {"1", "true", "yes", "on"})


def _csv(value: str | None) -> list[str]:
    return [item.strip() for item in (value or "").split(",") if item.strip()]


def production_daemon_spec(
    environment: Mapping[str, str], *, root: Path = ROOT
) -> tuple[int, list[str]]:
    endpoint = environment.get("ASTRORDER_SESSION_DAEMON_ENDPOINT", "ws://127.0.0.1:30009")
    parsed = urlparse(endpoint)
    if parsed.scheme != "ws" or parsed.hostname not in {"127.0.0.1", "localhost", "::1"}:
        raise ValueError("production Session Daemon endpoint must be loopback WebSocket")
    if parsed.path not in {"", "/"} or parsed.query or parsed.fragment or parsed.username:
        raise ValueError("production Session Daemon endpoint must not contain path or credentials")
    port = parsed.port or 80
    if not 1 <= port <= 65535:
        raise ValueError("production Session Daemon port is invalid")

    allowed = _csv(environment.get("ASTRORDER_ALLOWED_WORKSPACES"))
    runtime_args: list[str] = []
    if _enabled(environment.get("ASTRORDER_DAEMON_CODEX_ENABLED")):
        executable = environment.get("ASTRORDER_CODEX_EXECUTABLE")
        workspace = environment.get("ASTRORDER_DAEMON_CODEX_WORKSPACE") or str(root)
        if not executable or not workspace or not allowed:
            raise ValueError(
                "production daemon Codex requires executable, workspace and allowed workspaces"
            )
        runtime_args.extend(
            [
                "--enable-codex",
                "--codex-executable",
                executable,
                "--codex-workspace",
                workspace,
                "--codex-agent-id",
                "local-codex",
                "--codex-agent-name",
                "本机 Codex",
            ]
        )
        for item in allowed:
            runtime_args.extend(["--codex-allowed-workspace", item])
    if _enabled(environment.get("ASTRORDER_DAEMON_PTY_ENABLED")):
        if not allowed:
            raise ValueError("production daemon PTY requires allowed workspaces")
        runtime_args.append("--enable-pty")
        for item in allowed:
            runtime_args.extend(["--pty-allowed-workspace", item])
    if _enabled(environment.get("ASTRORDER_DAEMON_HERMES_ENABLED")):
        runtime_args.append("--enable-hermes")
    if _enabled(environment.get("ASTRORDER_DAEMON_GROK_ENABLED")):
        executable = environment.get("ASTRORDER_GROK_EXECUTABLE")
        workspace = environment.get("ASTRORDER_DAEMON_GROK_WORKSPACE") or str(root)
        if not executable or not workspace or not allowed:
            raise ValueError(
                "production daemon Grok requires executable, workspace and allowed workspaces"
            )
        runtime_args.extend(
            ["--enable-grok", "--grok-executable", executable, "--grok-workspace", workspace]
        )
        for item in allowed:
            runtime_args.extend(["--grok-allowed-workspace", item])
    if _enabled(environment.get("ASTRORDER_DAEMON_SSH_ENABLED")):
        runtime_args.append("--enable-ssh")
    return port, runtime_args


def ensure_production_daemon(
    environment: Mapping[str, str],
    *,
    root: Path = ROOT,
    starter: Callable[..., int] = start_daemon,
) -> int:
    if not os.environ.get("ASTRORDER_SESSION_DAEMON_SECRET"):
        raise RuntimeError("ASTRORDER_SESSION_DAEMON_SECRET is required")
    port, runtime_args = production_daemon_spec(environment, root=root)
    return starter(port=port, runtime_args=runtime_args)
