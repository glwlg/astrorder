from __future__ import annotations

import base64
import hashlib
import json
from collections.abc import Iterator
from contextlib import contextmanager
from datetime import datetime
from pathlib import Path
from typing import Any

from sqlalchemy import create_engine, delete, func, select
from sqlalchemy import event as sqlalchemy_event
from sqlalchemy.engine import Engine
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session, sessionmaker

from .config import Settings
from .models import (
    AgentRow,
    AttachmentRow,
    Base,
    CommandRow,
    EventRow,
    MessageRow,
    SessionRow,
)
from .timeutil import isoformat, parse_timestamp, utc_now


class DuplicateCommand(Exception):
    def __init__(self, existing: dict[str, Any]):
        self.existing = existing
        super().__init__("command already exists")


class UnknownCommand(Exception):
    pass


class ScopeNotFound(Exception):
    pass


if Engine:

    @sqlalchemy_event.listens_for(Engine, "connect")
    def _sqlite_pragmas(dbapi_connection, _connection_record) -> None:
        if dbapi_connection.__class__.__module__.startswith("sqlite3"):
            cursor = dbapi_connection.cursor()
            cursor.execute("PRAGMA foreign_keys=ON")
            cursor.execute("PRAGMA busy_timeout=5000")
            cursor.execute("PRAGMA journal_mode=WAL")
            cursor.close()


def _hash_payload(payload: dict[str, Any]) -> str:
    encoded = json.dumps(payload, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(encoded.encode("utf-8")).hexdigest()


def encode_history_cursor(row_id: int) -> str:
    raw = json.dumps({"row": row_id}, separators=(",", ":")).encode("ascii")
    return base64.urlsafe_b64encode(raw).decode("ascii").rstrip("=")


def decode_history_cursor(value: str) -> int:
    try:
        padded = value + "=" * (-len(value) % 4)
        decoded = json.loads(base64.urlsafe_b64decode(padded).decode("ascii"))
        row_id = decoded["row"]
        if not isinstance(row_id, int) or row_id < 1:
            raise ValueError
        return row_id
    except (ValueError, TypeError, KeyError, json.JSONDecodeError, UnicodeError) as exc:
        raise ValueError("invalid history cursor") from exc


def _attachment_wire(row: AttachmentRow | dict[str, Any]) -> dict[str, Any]:
    if isinstance(row, AttachmentRow):
        attachment_id, name, media_type = row.id, row.name, row.media_type
    else:
        attachment_id = str(row["id"])
        name = str(row["name"])
        media_type = str(row["media_type"])
    return {
        "id": attachment_id,
        "name": name,
        "media_type": media_type,
        "url": f"/api/v1/attachments/{attachment_id}",
    }


def _agent_wire(row: AgentRow) -> dict[str, Any]:
    return {
        "id": row.id,
        "kind": row.kind,
        "name": row.name,
        "status": row.status,
        "capabilities": list(row.capabilities or []),
        "limitation": row.limitation,
    }


def _session_wire(row: SessionRow) -> dict[str, Any]:
    return {
        "id": row.id,
        "agent_id": row.agent_id,
        "title": row.title,
        "workspace": row.workspace,
        "status": row.status,
        "updated_at": isoformat(row.updated_at),
    }


def _message_wire(row: MessageRow) -> dict[str, Any]:
    return {
        "id": row.id,
        "session_id": row.session_id,
        "agent_id": row.agent_id,
        "role": row.role,
        "kind": row.kind,
        "text": row.text,
        "attachments": list(row.attachments or []),
        "created_at": isoformat(row.created_at),
        "command_id": row.command_id,
        "tool": row.tool,
    }


def _command_wire(row: CommandRow) -> dict[str, Any]:
    return {
        "id": row.id,
        "session_id": row.session_id,
        "agent_id": row.agent_id,
        "action": row.action,
        "state": row.state,
        "text": row.text,
        "attachments": list(row.attachments or []),
        "created_at": isoformat(row.created_at),
        "error": row.error,
        "target_id": row.target_id,
    }


def _event_wire(row: EventRow) -> dict[str, Any]:
    return {
        "id": row.event_id,
        "cursor": row.cursor,
        "type": row.type,
        "agent_id": row.agent_id,
        "session_id": row.session_id,
        "data": row.data,
    }


class Store:
    def __init__(self, settings: Settings):
        self.settings = settings
        if settings.database_url.startswith("sqlite:///"):
            raw_path = settings.database_url.removeprefix("sqlite:///")
            if raw_path and raw_path != ":memory:":
                Path(raw_path).parent.mkdir(parents=True, exist_ok=True)
        connect_args = {"check_same_thread": False} if settings.database_url.startswith("sqlite") else {}
        self.engine = create_engine(settings.database_url, connect_args=connect_args)
        self.session_factory = sessionmaker(self.engine, expire_on_commit=False, class_=Session)
        Base.metadata.create_all(self.engine)

    @contextmanager
    def session(self) -> Iterator[Session]:
        db = self.session_factory()
        try:
            yield db
            db.commit()
        except Exception:
            db.rollback()
            raise
        finally:
            db.close()

    def close(self) -> None:
        self.engine.dispose()

    def get_agent(self, agent_id: str) -> dict[str, Any] | None:
        with self.session() as db:
            row = db.get(AgentRow, agent_id)
            return _agent_wire(row) if row else None

    def list_agents(self) -> list[dict[str, Any]]:
        with self.session() as db:
            rows = db.scalars(select(AgentRow).order_by(AgentRow.id)).all()
            return [_agent_wire(row) for row in rows]

    def upsert_agent(self, data: dict[str, Any], *, status: str | None = None) -> dict[str, Any]:
        with self.session() as db:
            row = db.get(AgentRow, data["id"])
            if row is None:
                row = AgentRow(
                    id=data["id"],
                    kind=data["kind"],
                    name=data["name"],
                    status=status or data["status"],
                    capabilities=list(data.get("capabilities", [])),
                    limitation=data.get("limitation"),
                    updated_at=utc_now(),
                )
                db.add(row)
            else:
                row.kind = data["kind"]
                row.name = data["name"]
                row.status = status or data["status"]
                row.capabilities = list(data.get("capabilities", []))
                row.limitation = data.get("limitation")
                row.updated_at = utc_now()
            db.flush()
            return _agent_wire(row)

    def set_agent_status(self, agent_id: str, status: str) -> dict[str, Any] | None:
        with self.session() as db:
            row = db.get(AgentRow, agent_id)
            if row is None:
                return None
            row.status = status
            row.updated_at = utc_now()
            db.flush()
            return _agent_wire(row)

    def get_session(self, agent_id: str, session_id: str) -> dict[str, Any] | None:
        with self.session() as db:
            row = db.execute(
                select(SessionRow).where(SessionRow.agent_id == agent_id, SessionRow.id == session_id)
            ).scalar_one_or_none()
            return _session_wire(row) if row else None

    def list_sessions(self, agent_id: str | None = None) -> list[dict[str, Any]]:
        with self.session() as db:
            query = select(SessionRow).order_by(SessionRow.updated_at.desc(), SessionRow.row_id.desc())
            if agent_id is not None:
                query = query.where(SessionRow.agent_id == agent_id)
            return [_session_wire(row) for row in db.scalars(query).all()]

    def upsert_session(self, data: dict[str, Any]) -> dict[str, Any]:
        with self.session() as db:
            row = db.execute(
                select(SessionRow).where(
                    SessionRow.agent_id == data["agent_id"], SessionRow.id == data["id"]
                )
            ).scalar_one_or_none()
            updated_at = data["updated_at"]
            if not isinstance(updated_at, datetime):
                updated_at = parse_timestamp(str(updated_at))
            if row is None:
                row = SessionRow(
                    id=data["id"],
                    agent_id=data["agent_id"],
                    title=data.get("title", ""),
                    workspace=data.get("workspace"),
                    status=data["status"],
                    updated_at=updated_at,
                )
                db.add(row)
            else:
                row.title = data.get("title", "")
                row.workspace = data.get("workspace")
                row.status = data["status"]
                row.updated_at = updated_at
            db.flush()
            return _session_wire(row)

    def get_attachment_row(self, attachment_id: str) -> AttachmentRow | None:
        with self.session() as db:
            return db.get(AttachmentRow, attachment_id)

    def add_attachment(
        self,
        attachment_id: str,
        name: str,
        media_type: str,
        size: int,
        storage_name: str,
    ) -> dict[str, Any]:
        with self.session() as db:
            row = AttachmentRow(
                id=attachment_id,
                name=name,
                media_type=media_type,
                size=size,
                storage_name=storage_name,
                created_at=utc_now(),
            )
            db.add(row)
            db.flush()
            return _attachment_wire(row)

    def get_attachment(self, attachment_id: str) -> dict[str, Any] | None:
        with self.session() as db:
            row = db.get(AttachmentRow, attachment_id)
            if row is None:
                return None
            return {
                "id": row.id,
                "name": row.name,
                "media_type": row.media_type,
                "size": row.size,
                "storage_name": row.storage_name,
                "created_at": isoformat(row.created_at),
            }

    def attachment_rows(self, attachment_ids: list[str]) -> list[dict[str, Any]]:
        if not attachment_ids:
            return []
        with self.session() as db:
            rows = db.scalars(select(AttachmentRow).where(AttachmentRow.id.in_(attachment_ids))).all()
            by_id = {row.id: row for row in rows}
            if any(item not in by_id for item in attachment_ids):
                raise ScopeNotFound("attachment not found")
            return [_attachment_wire(by_id[item]) for item in attachment_ids]

    def create_command(
        self,
        *,
        command: dict[str, Any],
        attachments: list[dict[str, Any]],
        initial_state: str,
    ) -> tuple[dict[str, Any], bool]:
        payload = {
            "id": command["id"],
            "agent_id": command["agent_id"],
            "session_id": command["session_id"],
            "action": command["action"],
            "text": command["text"],
            "attachment_ids": [item["id"] for item in attachments],
            "target_id": command.get("target_id"),
        }
        payload_hash = _hash_payload(payload)
        now = utc_now()
        with self.session() as db:
            row = db.execute(
                select(CommandRow).where(
                    CommandRow.agent_id == command["agent_id"],
                    CommandRow.session_id == command["session_id"],
                    CommandRow.id == command["id"],
                )
            ).scalar_one_or_none()
            if row is not None:
                if row.payload_hash != payload_hash:
                    raise DuplicateCommand({"mismatch": True})
                return _command_wire(row), False
            row = CommandRow(
                id=command["id"],
                session_id=command["session_id"],
                agent_id=command["agent_id"],
                action=command["action"],
                state=initial_state,
                text=command["text"],
                attachments=attachments,
                target_id=command.get("target_id"),
                created_at=now,
                updated_at=now,
                error=None,
                payload_hash=payload_hash,
            )
            db.add(row)
            try:
                db.flush()
            except IntegrityError:
                db.rollback()
                existing = db.execute(
                    select(CommandRow).where(
                        CommandRow.agent_id == command["agent_id"],
                        CommandRow.session_id == command["session_id"],
                        CommandRow.id == command["id"],
                    )
                ).scalar_one_or_none()
                if existing is not None and existing.payload_hash == payload_hash:
                    return _command_wire(existing), False
                raise DuplicateCommand({"mismatch": True})
            return _command_wire(row), True

    def get_command(self, agent_id: str, session_id: str, command_id: str) -> dict[str, Any] | None:
        with self.session() as db:
            row = db.execute(
                select(CommandRow).where(
                    CommandRow.agent_id == agent_id,
                    CommandRow.session_id == session_id,
                    CommandRow.id == command_id,
                )
            ).scalar_one_or_none()
            return _command_wire(row) if row else None

    def list_commands(self, agent_id: str, session_id: str) -> list[dict[str, Any]]:
        with self.session() as db:
            rows = db.scalars(
                select(CommandRow)
                .where(CommandRow.agent_id == agent_id, CommandRow.session_id == session_id)
                .order_by(CommandRow.row_id)
            ).all()
            return [_command_wire(row) for row in rows]

    def pending_commands(self, agent_id: str) -> list[dict[str, Any]]:
        with self.session() as db:
            rows = db.scalars(
                select(CommandRow)
                .where(CommandRow.agent_id == agent_id, CommandRow.state == "queued")
                .order_by(CommandRow.row_id)
            ).all()
            return [_command_wire(row) for row in rows]

    def active_commands(self) -> list[dict[str, Any]]:
        with self.session() as db:
            rows = db.scalars(
                select(CommandRow).where(
                    CommandRow.state.in_({"received", "queued", "accepted", "running"})
                ).order_by(CommandRow.row_id)
            ).all()
            return [_command_wire(row) for row in rows]

    def set_command_state(
        self,
        agent_id: str,
        session_id: str,
        command_id: str,
        state: str,
        error: str | None,
    ) -> dict[str, Any]:
        with self.session() as db:
            row = db.execute(
                select(CommandRow).where(
                    CommandRow.agent_id == agent_id,
                    CommandRow.session_id == session_id,
                    CommandRow.id == command_id,
                )
            ).scalar_one_or_none()
            if row is None:
                raise UnknownCommand(command_id)
            row.state = state
            row.error = error
            row.updated_at = utc_now()
            db.flush()
            return _command_wire(row)

    def upsert_message(self, data: dict[str, Any]) -> dict[str, Any]:
        with self.session() as db:
            row = db.execute(
                select(MessageRow).where(
                    MessageRow.agent_id == data["agent_id"],
                    MessageRow.session_id == data["session_id"],
                    MessageRow.id == data["id"],
                )
            ).scalar_one_or_none()
            created_at = data["created_at"]
            if not isinstance(created_at, datetime):
                created_at = parse_timestamp(str(created_at))
            values = {
                "role": data["role"],
                "kind": data["kind"],
                "text": data.get("text", ""),
                "attachments": list(data.get("attachments", [])),
                "created_at": created_at,
                "command_id": data.get("command_id"),
                "tool": data.get("tool"),
            }
            if row is None:
                row = MessageRow(
                    id=data["id"],
                    session_id=data["session_id"],
                    agent_id=data["agent_id"],
                    **values,
                )
                db.add(row)
            else:
                for key, value in values.items():
                    setattr(row, key, value)
            db.flush()
            return _message_wire(row)

    def get_message(self, agent_id: str, session_id: str, message_id: str) -> dict[str, Any] | None:
        with self.session() as db:
            row = db.execute(
                select(MessageRow).where(
                    MessageRow.agent_id == agent_id,
                    MessageRow.session_id == session_id,
                    MessageRow.id == message_id,
                )
            ).scalar_one_or_none()
            return _message_wire(row) if row else None

    def list_messages(
        self, agent_id: str, session_id: str, before: str | None, limit: int
    ) -> tuple[list[dict[str, Any]], str | None]:
        with self.session() as db:
            query = select(MessageRow).where(
                MessageRow.agent_id == agent_id, MessageRow.session_id == session_id
            )
            if before is not None:
                query = query.where(MessageRow.row_id < decode_history_cursor(before))
            rows = db.scalars(query.order_by(MessageRow.row_id.desc()).limit(limit + 1)).all()
            has_more = len(rows) > limit
            rows = rows[:limit]
            rows.reverse()
            next_cursor = encode_history_cursor(rows[0].row_id) if has_more and rows else None
            return [_message_wire(row) for row in rows], next_cursor

    def latest_cursor(self) -> int:
        with self.session() as db:
            return int(db.scalar(select(func.max(EventRow.cursor))) or 0)

    def append_event(
        self,
        *,
        event_id: str,
        event_type: str,
        agent_id: str | None,
        session_id: str | None,
        data: dict[str, Any],
    ) -> dict[str, Any] | None:
        with self.session() as db:
            if self._event_exists(db, event_id, agent_id):
                return None
            row = self._insert_event(
                db,
                event_id=event_id,
                event_type=event_type,
                agent_id=agent_id,
                session_id=session_id,
                data=data,
            )
            return _event_wire(row)

    def apply_connector_event(
        self,
        *,
        event_id: str,
        event_type: str,
        agent_id: str,
        session_id: str | None,
        data: dict[str, Any],
    ) -> dict[str, Any] | None:
        with self.session() as db:
            if self._event_exists(db, event_id, agent_id):
                return None
            canonical = data
            if event_type == "agent.upsert":
                row = db.get(AgentRow, agent_id)
                if row is None:
                    row = AgentRow(
                        id=agent_id,
                        kind=data["kind"],
                        name=data["name"],
                        status=data["status"],
                        capabilities=list(data.get("capabilities", [])),
                        limitation=data.get("limitation"),
                        updated_at=utc_now(),
                    )
                    db.add(row)
                else:
                    row.kind = data["kind"]
                    row.name = data["name"]
                    row.status = data["status"]
                    row.capabilities = list(data.get("capabilities", []))
                    row.limitation = data.get("limitation")
                    row.updated_at = utc_now()
                db.flush()
                canonical = _agent_wire(row)
            elif event_type == "session.upsert":
                canonical = self._upsert_session_in(db, data)
            elif event_type == "message.upsert":
                canonical = self._upsert_message_in(db, data)
            elif event_type == "command.upsert":
                command_id = data.get("id")
                row = db.execute(
                    select(CommandRow).where(
                        CommandRow.agent_id == agent_id,
                        CommandRow.session_id == session_id,
                        CommandRow.id == command_id,
                    )
                ).scalar_one_or_none()
                if row is None:
                    raise UnknownCommand(str(command_id))
                row.state = data["state"]
                row.error = data.get("error")
                row.updated_at = utc_now()
                db.flush()
                canonical = _command_wire(row)
            row = self._insert_event(
                db,
                event_id=event_id,
                event_type=event_type,
                agent_id=agent_id,
                session_id=session_id,
                data=canonical,
            )
            return _event_wire(row)

    def _upsert_session_in(self, db: Session, data: dict[str, Any]) -> dict[str, Any]:
        row = db.execute(
            select(SessionRow).where(SessionRow.agent_id == data["agent_id"], SessionRow.id == data["id"])
        ).scalar_one_or_none()
        updated_at = data["updated_at"]
        if not isinstance(updated_at, datetime):
            updated_at = parse_timestamp(str(updated_at))
        if row is None:
            row = SessionRow(
                id=data["id"],
                agent_id=data["agent_id"],
                title=data.get("title", ""),
                workspace=data.get("workspace"),
                status=data["status"],
                updated_at=updated_at,
            )
            db.add(row)
        else:
            row.title = data.get("title", "")
            row.workspace = data.get("workspace")
            row.status = data["status"]
            row.updated_at = updated_at
        db.flush()
        return _session_wire(row)

    def _upsert_message_in(self, db: Session, data: dict[str, Any]) -> dict[str, Any]:
        row = db.execute(
            select(MessageRow).where(
                MessageRow.agent_id == data["agent_id"],
                MessageRow.session_id == data["session_id"],
                MessageRow.id == data["id"],
            )
        ).scalar_one_or_none()
        created_at = data["created_at"]
        if not isinstance(created_at, datetime):
            created_at = parse_timestamp(str(created_at))
        values = {
            "role": data["role"],
            "kind": data["kind"],
            "text": data.get("text", ""),
            "attachments": list(data.get("attachments", [])),
            "created_at": created_at,
            "command_id": data.get("command_id"),
            "tool": data.get("tool"),
        }
        if row is None:
            row = MessageRow(
                id=data["id"], session_id=data["session_id"], agent_id=data["agent_id"], **values
            )
            db.add(row)
        else:
            for key, value in values.items():
                setattr(row, key, value)
        db.flush()
        return _message_wire(row)

    def _event_exists(self, db: Session, event_id: str, agent_id: str | None) -> bool:
        query = select(EventRow.cursor).where(EventRow.event_id == event_id)
        if agent_id is None:
            query = query.where(EventRow.agent_id.is_(None))
        else:
            query = query.where(EventRow.agent_id == agent_id)
        return db.scalar(query) is not None

    def _insert_event(
        self,
        db: Session,
        *,
        event_id: str,
        event_type: str,
        agent_id: str | None,
        session_id: str | None,
        data: dict[str, Any],
    ) -> EventRow:
        row = EventRow(
            event_id=event_id,
            type=event_type,
            agent_id=agent_id,
            session_id=session_id,
            data=data,
            created_at=utc_now(),
        )
        db.add(row)
        db.flush()
        cutoff = row.cursor - self.settings.event_retention + 1
        if cutoff > 1:
            db.execute(delete(EventRow).where(EventRow.cursor < cutoff))
        return row

    def replay(self, after: int) -> tuple[bool, list[dict[str, Any]]]:
        with self.session() as db:
            latest = int(db.scalar(select(func.max(EventRow.cursor))) or 0)
            oldest = int(db.scalar(select(func.min(EventRow.cursor))) or 0)
            if after > latest or (oldest and after < oldest - 1):
                return True, []
            rows = db.scalars(
                select(EventRow).where(EventRow.cursor > after).order_by(EventRow.cursor)
            ).all()
            return False, [_event_wire(row) for row in rows]

    def sessions_for_bootstrap(self) -> list[dict[str, Any]]:
        return self.list_sessions()
