from __future__ import annotations

from typing import Any
from uuid import uuid4
from sqlalchemy import select

from ..models import SshConnectionRow


def _ssh_connection_wire(row: SshConnectionRow) -> dict[str, Any]:
    settings = {
        "host": row.host,
        "port": row.port,
        "user": row.user,
        "ssh_config_alias": row.ssh_config_alias,
        "identity_file": row.identity_file,
        "hermes_path": row.hermes_path,
        "workspace": row.workspace,
    }
    return {
        "id": row.id,
        "display_name": row.display_name or row.ssh_config_alias or row.host or row.id,
        "profile_name": row.profile_name or "default",
        "state": row.state,
        "settings": settings,
        "detail": row.detail,
        "remote_os": row.remote_os,
        "agent_id": row.agent_id,
        "runtime_id": row.runtime_id,
    }


class ConnectionRepository:
    """SSH 连接与远程机器环境仓储：封装远程主机凭据、连接状态与元数据持久化。"""

    def __init__(self, store: Any) -> None:
        self.store = store

    def get(self, connection_id: str | None = None) -> dict[str, Any] | None:
        with self.store.session() as db:
            if connection_id is None:
                row = db.scalars(select(SshConnectionRow).order_by(SshConnectionRow.id)).first()
            else:
                row = db.get(SshConnectionRow, connection_id)
            return _ssh_connection_wire(row) if row is not None else None

    def list(self) -> list[dict[str, Any]]:
        with self.store.session() as db:
            rows = db.scalars(select(SshConnectionRow).order_by(SshConnectionRow.id)).all()
            return [_ssh_connection_wire(row) for row in rows]
