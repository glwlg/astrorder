from __future__ import annotations

import os
from dataclasses import dataclass
from pathlib import Path


@dataclass(frozen=True)
class CodexConnectorConfig:
    endpoint: str
    secret: str
    agent_id: str
    agent_name: str
    executable: str
    workspace: Path
    allowed_workspaces: tuple[Path, ...]
    thread_id: str | None = None

    @classmethod
    def from_env(cls) -> CodexConnectorConfig:
        secret = os.environ.get("ASTRORDER_CONNECTOR_SECRET")
        if not secret:
            raise ValueError("ASTRORDER_CONNECTOR_SECRET is required")
        agent_id = os.environ.get("ASTRORDER_CODEX_AGENT_ID")
        if not agent_id:
            raise ValueError("ASTRORDER_CODEX_AGENT_ID is required")
        workspace_value = os.environ.get("ASTRORDER_CODEX_WORKSPACE")
        if not workspace_value:
            raise ValueError("ASTRORDER_CODEX_WORKSPACE is required")
        workspace = Path(workspace_value).expanduser().resolve()
        roots = tuple(
            Path(item).expanduser().resolve()
            for item in os.environ.get("ASTRORDER_ALLOWED_WORKSPACES", "").split(",")
            if item.strip()
        )
        if not workspace.is_dir() or not any(_inside(workspace, root) for root in roots):
            raise ValueError("ASTRORDER_CODEX_WORKSPACE must be inside an allowed workspace")
        return cls(
            endpoint=os.environ.get(
                "ASTRORDER_CONNECTOR_ENDPOINT",
                "ws://127.0.0.1:30002/ws/v1/connector",
            ),
            secret=secret,
            agent_id=agent_id,
            agent_name=os.environ.get("ASTRORDER_CODEX_AGENT_NAME", "Codex"),
            executable=os.environ.get("ASTRORDER_CODEX_EXECUTABLE", "codex"),
            workspace=workspace,
            allowed_workspaces=roots,
            thread_id=os.environ.get("ASTRORDER_CODEX_THREAD_ID") or None,
        )


def _inside(path: Path, root: Path) -> bool:
    try:
        path.relative_to(root)
        return True
    except ValueError:
        return False
