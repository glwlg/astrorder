from __future__ import annotations

from typing import Any
from sqlalchemy import select

from ..models import MessageRow


def _message_wire(row: MessageRow) -> dict[str, Any]:
    from ..store import isoformat
    import json
    tool_payload = None
    if row.tool_name or row.tool_call_id or row.tool_calls:
        try:
            tool_payload = json.loads(row.tool_calls) if row.tool_calls else None
        except Exception:
            tool_payload = None
    return {
        "id": row.id,
        "agent_id": row.agent_id,
        "session_id": row.session_id,
        "role": row.role,
        "kind": row.kind or "message",
        "text": row.content or "",
        "tool": tool_payload,
        "created_at": isoformat(row.created_at),
    }


class MessageRepository:
    """消息与历史领域仓储：封装对话消息的持久化与分页检索。"""

    def __init__(self, store: Any) -> None:
        self.store = store

    def list_by_session(
        self, agent_id: str, session_id: str, limit: int = 100
    ) -> list[dict[str, Any]]:
        with self.store.session() as db:
            rows = db.scalars(
                select(MessageRow)
                .where(
                    MessageRow.agent_id == agent_id,
                    MessageRow.session_id == session_id,
                )
                .order_by(MessageRow.created_at.asc(), MessageRow.row_id.asc())
                .limit(limit)
            ).all()
            return [_message_wire(row) for row in rows]
