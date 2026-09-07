from __future__ import annotations

import os
from collections.abc import Iterable
from dataclasses import dataclass, field
from pathlib import Path

DEFAULT_ORIGINS = (
    "http://127.0.0.1:8765",
    "http://localhost:8765",
    "http://127.0.0.1:5173",
    "http://localhost:5173",
)
DEFAULT_ATTACHMENT_TYPES = (
    "application/json",
    "application/octet-stream",
    "application/pdf",
    "image/gif",
    "image/jpeg",
    "image/png",
    "image/webp",
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
    port: int = 8765
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

    def __post_init__(self) -> None:
        if not 1 <= self.port <= 65535:
            raise ValueError("port must be between 1 and 65535")
        if self.max_attachment_size < 1:
            raise ValueError("max_attachment_size must be positive")
        if self.event_retention < 1:
            raise ValueError("event_retention must be positive")
        if not self.database_url.startswith("sqlite"):
            raise ValueError("database_url must use SQLite")
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
        return cls(
            host=env.get("ASTRORDER_HOST", "127.0.0.1"),
            port=_int(env.get("ASTRORDER_PORT"), 8765),
            database_url=env.get("ASTRORDER_DATABASE_URL", "sqlite:///./astrorder.sqlite3"),
            browser_secret=env.get("ASTRORDER_BROWSER_SECRET") or env.get("ASTRORDER_SECRET"),
            connector_secret=env.get("ASTRORDER_CONNECTOR_SECRET"),
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
        )
