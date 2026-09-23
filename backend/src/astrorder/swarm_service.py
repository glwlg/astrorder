"""Astrorder Swarm Orchestration service: milestones, telemetry, locks, and SOS signals."""
from __future__ import annotations

import json
from datetime import datetime, timezone
from typing import Any
from sqlalchemy.dialects.sqlite import insert

from .models import WorkspacePreferenceRow


def milestone_declare(payload: dict[str, Any], store: Any, service: Any = None) -> dict[str, Any]:
    mid = payload.get("milestone_id")
    if not isinstance(mid, str) or not mid.strip():
        raise ValueError("milestone_id is required")
    mid = mid.strip()

    title = str(payload.get("title") or mid).strip()
    wake_session_key = payload.get("wake_session_key")
    wake_prompt = payload.get("wake_prompt")
    metadata = payload.get("metadata") or {}

    record = {
        "id": mid,
        "title": title,
        "status": "pending",
        "wake_session_key": str(wake_session_key).strip() if wake_session_key else None,
        "wake_prompt": str(wake_prompt).strip() if wake_prompt else None,
        "metadata": metadata if isinstance(metadata, dict) else {},
        "created_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        "resolved_at": None,
        "result": None,
    }

    db_key = f"milestone:{mid}"
    with store.session() as db:
        statement = insert(WorkspacePreferenceRow).values(key=db_key, value=record)
        statement = statement.on_conflict_do_update(index_elements=["key"], set_={"value": record})
        db.execute(statement)

    if service is not None:
        service._server_event("swarm.milestone.event", agent_id=None, session_id=None, data={"action": "declare", "milestone": record})

    return {"ok": True, "milestone": record}


def milestone_resolve(payload: dict[str, Any], store: Any, service: Any = None) -> dict[str, Any]:
    mid = payload.get("milestone_id")
    if not isinstance(mid, str) or not mid.strip():
        raise ValueError("milestone_id is required")
    mid = mid.strip()
    result_data = payload.get("result")

    db_key = f"milestone:{mid}"
    record = None
    with store.session() as db:
        row = db.get(WorkspacePreferenceRow, db_key)
        if row and isinstance(row.value, dict):
            record = dict(row.value)
        else:
            record = {
                "id": mid,
                "title": mid,
                "status": "pending",
                "wake_session_key": None,
                "wake_prompt": None,
                "metadata": {},
                "created_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
            }
        record["status"] = "resolved"
        record["resolved_at"] = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
        record["result"] = result_data
        statement = insert(WorkspacePreferenceRow).values(key=db_key, value=record)
        statement = statement.on_conflict_do_update(index_elements=["key"], set_={"value": record})
        db.execute(statement)

    if service is not None:
        service._server_event("swarm.milestone.event", agent_id=None, session_id=None, data={"action": "resolve", "milestone": record})

    return {"ok": True, "milestone": record}
