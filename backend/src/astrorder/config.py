from __future__ import annotations

import os
from collections.abc import Iterable
from dataclasses import dataclass, field
from pathlib import Path

DEFAULT_ORIGINS = (
    "http://127.0.0.1:30001",
    "http://localhost:30001",
    "http://127.0.0.1:30002",
    "http://localhost:30002",
)
DEFAULT_ATTACHMENT_TYPES = (
    "application/json",
    "application/octet-stream",
    "application/pdf",
    "audio/aac",
    "audio/mp4",
    "audio/mpeg",
    "audio/ogg",
    "audio/wav",
    "audio/webm",
    "audio/x-wav",
    "image/gif",
    "image/jpeg",
    "image/png",
    "image/webp",
    "text/csv",
    "text/plain",
    "text/markdown",
)


def _csv(value: str | None, default: Iterable[str]) -> tuple[str, ...]:
    if value is None:
        return tuple(default)
    return tuple(item.strip() for item in value.split(",") if item.strip())


def _bool(value: str | None, default: bool = False) -> bool:
    if value is None:
        return default
    return value.strip().lower() in {"1", "true", "yes", "on"}


def _int(value: str | None, default: int) -> int:
    try:
        return int(value) if value is not None else default
    except ValueError:
        return default


@dataclass(frozen=True)
class Settings:
    """Runtime settings sourced from explicit values or ASTRORDER_* environment variables.

    This class deliberately does not load dotenv files or inspect Hermes/Codex credential stores.
    """

    host: str = "127.0.0.1"
    port: int = 30002
    database_url: str = "sqlite:///./astrorder.sqlite3"
    browser_secret: str | None = None
    connector_secret: str | None = None
    attachments_dir: Path = field(default_factory=lambda: Path("data/attachments"))
    static_dir: Path | None = None
    allowed_origins: tuple[str, ...] = DEFAULT_ORIGINS
    max_attachment_size: int = 10 * 1024 * 1024
    allowed_attachment_types: tuple[str, ...] = DEFAULT_ATTACHMENT_TYPES
    event_retention: int = 1000
    allowed_workspaces: tuple[Path, ...] = ()
    launch_enabled: bool = False
    hermes_executable: str | None = None
    codex_executable: str | None = None
    auto_connect_local_hermes: bool = True
    max_ssh_connections: int = 32
    session_daemon_enabled: bool = False
    session_daemon_endpoint: str = "ws://127.0.0.1:30009"
    session_daemon_secret: str | None = None
    session_daemon_request_timeout: float = 30.0
    daemon_codex_enabled: bool = False
    daemon_pty_enabled: bool = False
    daemon_hermes_enabled: bool = False
    daemon_ssh_enabled: bool = False
    model_routing_enabled: bool = False
    model_routing_daily_budget_units: int = 100
    model_routing_large_text_threshold: int = 2000
    model_routing_small_cost_units: int = 1
    model_routing_large_cost_units: int = 5
    hermes_small_model: str | None = None
    hermes_large_model: str | None = None
    codex_small_model: str | None = None
    codex_large_model: str | None = None
    model_routing_small_effort: str = "low"
    model_routing_large_effort: str = "high"

    def __post_init__(self) -> None:
        if not 1 <= self.port <= 65535:
            raise ValueError("port must be between 1 and 65535")
        if self.max_attachment_size < 1:
            raise ValueError("max_attachment_size must be positive")
        if self.event_retention < 1:
            raise ValueError("event_retention must be positive")
        if self.max_ssh_connections < 1:
            raise ValueError("max_ssh_connections must be positive")
        if self.model_routing_daily_budget_units < 1:
            raise ValueError("model routing daily budget must be positive")
        if self.model_routing_large_text_threshold < 1:
            raise ValueError("model routing large text threshold must be positive")
        if (
            self.model_routing_small_cost_units < 1
            or self.model_routing_large_cost_units < self.model_routing_small_cost_units
        ):
            raise ValueError("model routing cost units are invalid")
        routing_targets = (
            self.hermes_small_model,
            self.hermes_large_model,
            self.codex_small_model,
            self.codex_large_model,
        )
        if self.model_routing_enabled and any(
            not isinstance(target, str)
            or "/" not in target
            or target.startswith("/")
            or target.endswith("/")
            for target in routing_targets
        ):
            raise ValueError("enabled model routing requires exact provider/model targets")
        if not 0 < self.session_daemon_request_timeout <= 300:
            raise ValueError("session daemon request timeout must be between 0 and 300 seconds")
        if not self.database_url.startswith("sqlite"):
            raise ValueError("database_url must use SQLite")
        if self.daemon_codex_enabled and (
            not self.session_daemon_enabled
            or not isinstance(self.session_daemon_secret, str)
            or not self.session_daemon_secret
        ):
            raise ValueError("daemon Codex requires an enabled Session Daemon with a secret")
        if self.daemon_pty_enabled and (
            not self.session_daemon_enabled
            or not isinstance(self.session_daemon_secret, str)
            or not self.session_daemon_secret
        ):
            raise ValueError("daemon PTY requires an enabled Session Daemon with a secret")
        if self.daemon_hermes_enabled and (
            not self.session_daemon_enabled
            or not isinstance(self.session_daemon_secret, str)
            or not self.session_daemon_secret
        ):
            raise ValueError("daemon Hermes requires an enabled Session Daemon with a secret")
        if self.daemon_ssh_enabled and (
            not self.session_daemon_enabled
            or not isinstance(self.session_daemon_secret, str)
            or not self.session_daemon_secret
        ):
            raise ValueError("daemon SSH requires an enabled Session Daemon with a secret")
        object.__setattr__(self, "attachments_dir", Path(self.attachments_dir))
        if self.static_dir is not None:
            object.__setattr__(self, "static_dir", Path(self.static_dir))
        object.__setattr__(
            self,
            "allowed_workspaces",
            tuple(Path(path) for path in self.allowed_workspaces),
        )
        object.__setattr__(self, "allowed_origins", tuple(self.allowed_origins))
        if "*" in self.allowed_origins:
            raise ValueError("wildcard origins are not allowed")
        object.__setattr__(
            self,
            "allowed_attachment_types",
            tuple(self.allowed_attachment_types),
        )

    @classmethod
    def from_env(cls) -> Settings:
        """Build settings from process environment only; no credential files are read."""
        env = os.environ
        workspaces = _csv(env.get("ASTRORDER_ALLOWED_WORKSPACES"), ())
        static_value = env.get("ASTRORDER_STATIC_DIR")
        browser_secret = env.get("ASTRORDER_BROWSER_SECRET") or env.get("ASTRORDER_SECRET")
        if not browser_secret:
            token_candidates = (
                Path(".token"),
                Path(__file__).resolve().parent.parent.parent.parent / ".token",
                Path(__file__).resolve().parent.parent.parent / ".token",
            )
            for c in token_candidates:
                if c.is_file():
                    content = c.read_text(encoding="utf-8").strip()
                    if content:
                        browser_secret = content
                        break
            if not browser_secret:
                import secrets
                browser_secret = secrets.token_urlsafe(24)
                target_file = token_candidates[1] if token_candidates[1].parent.is_dir() else token_candidates[0]
                target_file.write_text(browser_secret, encoding="utf-8")

        connector_secret = env.get("ASTRORDER_CONNECTOR_SECRET")
        if not connector_secret:
            conn_token_candidates = (
                Path(".connector_token"),
                Path(__file__).resolve().parent.parent.parent.parent / ".connector_token",
                Path(__file__).resolve().parent.parent.parent / ".connector_token",
            )
            for c in conn_token_candidates:
                if c.is_file():
                    content = c.read_text(encoding="utf-8").strip()
                    if content:
                        connector_secret = content
                        break
            if not connector_secret:
                import secrets
                connector_secret = secrets.token_urlsafe(24)
                target_file = conn_token_candidates[1] if conn_token_candidates[1].parent.is_dir() else conn_token_candidates[0]
                target_file.write_text(connector_secret, encoding="utf-8")

        return cls(
            host=env.get("ASTRORDER_HOST", "127.0.0.1"),
            port=_int(env.get("ASTRORDER_PORT"), 30002),
            database_url=env.get("ASTRORDER_DATABASE_URL", "sqlite:///./astrorder.sqlite3"),
            browser_secret=browser_secret,
            connector_secret=connector_secret,
            attachments_dir=Path(env.get("ASTRORDER_ATTACHMENTS_DIR", "data/attachments")),
            static_dir=Path(static_value) if static_value else None,
            allowed_origins=_csv(env.get("ASTRORDER_ALLOWED_ORIGINS"), DEFAULT_ORIGINS),
            max_attachment_size=_int(
                env.get("ASTRORDER_MAX_ATTACHMENT_SIZE"), 10 * 1024 * 1024
            ),
            allowed_attachment_types=_csv(
                env.get("ASTRORDER_ALLOWED_ATTACHMENT_TYPES"), DEFAULT_ATTACHMENT_TYPES
            ),
            event_retention=_int(env.get("ASTRORDER_EVENT_RETENTION"), 1000),
            allowed_workspaces=tuple(Path(path) for path in workspaces),
            launch_enabled=_bool(env.get("ASTRORDER_ENABLE_LAUNCH")),
            hermes_executable=env.get("ASTRORDER_HERMES_EXECUTABLE") or None,
            codex_executable=env.get("ASTRORDER_CODEX_EXECUTABLE") or None,
            auto_connect_local_hermes=_bool(env.get("ASTRORDER_AUTO_CONNECT_LOCAL_HERMES", "1")),
            max_ssh_connections=_int(env.get("ASTRORDER_MAX_SSH_CONNECTIONS"), 32),
            session_daemon_enabled=_bool(env.get("ASTRORDER_SESSION_DAEMON_ENABLED")),
            session_daemon_endpoint=env.get(
                "ASTRORDER_SESSION_DAEMON_ENDPOINT", "ws://127.0.0.1:30009"
            ),
            session_daemon_secret=env.get("ASTRORDER_SESSION_DAEMON_SECRET") or None,
            session_daemon_request_timeout=float(
                _int(env.get("ASTRORDER_SESSION_DAEMON_REQUEST_TIMEOUT"), 30)
            ),
            daemon_codex_enabled=_bool(env.get("ASTRORDER_DAEMON_CODEX_ENABLED")),
            daemon_pty_enabled=_bool(env.get("ASTRORDER_DAEMON_PTY_ENABLED")),
            daemon_hermes_enabled=_bool(env.get("ASTRORDER_DAEMON_HERMES_ENABLED")),
            daemon_ssh_enabled=_bool(env.get("ASTRORDER_DAEMON_SSH_ENABLED")),
            model_routing_enabled=_bool(env.get("ASTRORDER_MODEL_ROUTING_ENABLED")),
            model_routing_daily_budget_units=_int(
                env.get("ASTRORDER_MODEL_ROUTING_DAILY_BUDGET_UNITS"), 100
            ),
            model_routing_large_text_threshold=_int(
                env.get("ASTRORDER_MODEL_ROUTING_LARGE_TEXT_THRESHOLD"), 2000
            ),
            model_routing_small_cost_units=_int(
                env.get("ASTRORDER_MODEL_ROUTING_SMALL_COST_UNITS"), 1
            ),
            model_routing_large_cost_units=_int(
                env.get("ASTRORDER_MODEL_ROUTING_LARGE_COST_UNITS"), 5
            ),
            hermes_small_model=env.get("ASTRORDER_HERMES_SMALL_MODEL") or None,
            hermes_large_model=env.get("ASTRORDER_HERMES_LARGE_MODEL") or None,
            codex_small_model=env.get("ASTRORDER_CODEX_SMALL_MODEL") or None,
            codex_large_model=env.get("ASTRORDER_CODEX_LARGE_MODEL") or None,
            model_routing_small_effort=env.get(
                "ASTRORDER_MODEL_ROUTING_SMALL_EFFORT", "low"
            ),
            model_routing_large_effort=env.get(
                "ASTRORDER_MODEL_ROUTING_LARGE_EFFORT", "high"
            ),
        )

    def connector_endpoint(self) -> str:
        host = self.host
        if host in {"", "0.0.0.0", "::"}:
            host = "127.0.0.1"
        if ":" in host and not host.startswith("["):
            host = f"[{host}]"
        return f"ws://{host}:{self.port}/ws/v1/connector"
