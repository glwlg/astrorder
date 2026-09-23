from __future__ import annotations

from typing import Any
from sqlalchemy import select
from sqlalchemy.orm import Session as OrmSession

from ..models import SessionRow


def _session_wire(row: SessionRow) -> dict[str, Any]:
    from ..store import isoformat
    return {
        "id": row.id,
        "agent_id": row.agent_id,
        "title": row.title,
        "workspace": row.workspace,
        "status": row.status,
        "control_state": row.control_state,
        "history_state": row.history_state,
        "source_id": row.source_id,
        "connection_id": row.connection_id,
        "source_session_id": row.source_session_id,
        "project_id": row.project_id,
        "project_name": row.project_name,
        "parent_session_id": row.parent_session_id,
        "parent_agent_id": row.parent_agent_id,
        "ephemeral": bool(row.ephemeral),
        "live": bool(row.live),
        "created_at": isoformat(row.created_at),
        "updated_at": isoformat(row.updated_at),
    }


class SessionRepository:
    """会话领域仓储：封装会话的增删改查与查询操作。"""

    def __init__(self, store: Any) -> None:
        self.store = store

    def get(self, agent_id: str, session_id: str) -> dict[str, Any] | None:
        with self.store.session() as db:
            row = db.execute(
                select(SessionRow).where(
                    SessionRow.agent_id == agent_id,
                    SessionRow.id == session_id,
                )
            ).scalar_one_or_none()
            return _session_wire(row) if row else None

    def list(self, agent_id: str | None = None) -> list[dict[str, Any]]:
        with self.store.session() as db:
            query = select(SessionRow).order_by(SessionRow.updated_at.desc(), SessionRow.row_id.desc())
            if agent_id is not None:
                query = query.where(SessionRow.agent_id == agent_id)
            rows = db.scalars(query).all()
            return [_session_wire(row) for row in rows]
