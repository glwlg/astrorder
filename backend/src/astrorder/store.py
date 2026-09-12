from __future__ import annotations

import base64
import hashlib
import json
import re
from collections.abc import Iterator
from contextlib import contextmanager
from datetime import datetime
from pathlib import Path
from typing import Any
from uuid import uuid4

from sqlalchemy import create_engine, delete, func, select, text, update
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
    ConnectionHistoryRow,
    DaemonCheckpointRow,
    EventRow,
    MessageRow,
    ProjectRow,
    SessionRow,
    SshConnectionRow,
    TaskRow,
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


_SENSITIVE_TEXT = re.compile(
    r"(?i)\b(api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|passwd|secret|authorization|private[_ -]?key)\s*[:=]\s*[^\s,;]+"
)
_SENSITIVE_KEY = re.compile(
    r"(?i)(api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|passwd|secret|authorization|private[_ -]?key|credential|connection[_-]?string)"
)


def _redact_text(value: str, limit: int = 2000) -> str:
    return _SENSITIVE_TEXT.sub(r"\1=[REDACTED]", value).replace("\x00", " ").replace("\r", " ").replace("\n", " ").strip()[:limit]


def _redact_structured(value: Any, depth: int = 0) -> Any:
    if depth > 16:
        return "[REDACTED]"
    if isinstance(value, dict):
        return {
            str(key): "[REDACTED]"
            if _SENSITIVE_KEY.search(str(key))
            else _redact_structured(item, depth + 1)
            for key, item in value.items()
        }
    if isinstance(value, list):
        return [_redact_structured(item, depth + 1) for item in value]
    if isinstance(value, tuple):
        return [_redact_structured(item, depth + 1) for item in value]
    if isinstance(value, str):
        return _redact_text(value)
    return value


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
        "source_id": row.source_id or row.id,
        "connection_id": row.connection_id,
        "profile_name": row.profile_name,
        "runtime_id": row.runtime_id or row.id,
        "control_state": row.control_state,
    }


def _session_wire(row: SessionRow) -> dict[str, Any]:
    return {
        "id": row.id,
        "agent_id": row.agent_id,
        "title": row.title,
        "workspace": row.workspace,
        "status": row.status,
        "updated_at": isoformat(row.updated_at),
        "source_id": row.source_id or row.agent_id,
        "connection_id": row.connection_id,
        "source_session_id": row.source_session_id or row.id,
        "project_id": row.project_id,
        "project_name": row.project_name,
        "history_state": row.history_state,
        "native_kind": row.native_kind,
        "ephemeral": row.ephemeral,
        "control_state": row.control_state,
    }


def _project_wire(row: ProjectRow) -> dict[str, Any]:
    return {
        "id": row.id,
        "source_id": row.source_id,
        "connection_id": row.connection_id,
        "agent_id": row.agent_id,
        "profile_name": row.profile_name,
        "project_id": row.project_id,
        "project_name": row.project_name,
        "workspace": row.workspace,
        "session_count": row.session_count,
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


def _task_wire(row: TaskRow) -> dict[str, Any]:
    return {
        "id": row.id,
        "session_id": row.session_id,
        "agent_id": row.agent_id,
        "kind": row.kind,
        "title": row.title,
        "status": row.status,
        "progress": row.progress,
        "command": row.command,
        "logs": list(row.logs or []),
        "target_id": row.target_id,
        "created_at": isoformat(row.created_at),
        "updated_at": isoformat(row.updated_at),
    }


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


def _connection_history_wire(row: ConnectionHistoryRow) -> dict[str, Any]:
    return {
        "id": row.id,
        "connection_id": row.connection_id,
        "stage": row.stage,
        "state": row.state,
        "detail": row.detail,
        "details": _redact_structured(dict(row.details or {})),
        "created_at": isoformat(row.created_at),
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
        self._migrate_schema()
        from .native_identity_migration import migrate_native_identity

        with self.session() as db:
            migrate_native_identity(db)

    def _migrate_schema(self) -> None:
        """Apply additive SQLite migrations without rewriting user data."""
        additions = {
            "agents": {
                "source_id": "VARCHAR(256)",
                "connection_id": "VARCHAR(64)",
                "profile_name": "VARCHAR(128)",
                "runtime_id": "VARCHAR(256)",
                "control_state": "VARCHAR(32) NOT NULL DEFAULT 'unknown'",
            },
            "sessions": {
                "source_id": "VARCHAR(256)",
                "connection_id": "VARCHAR(64)",
                "source_session_id": "VARCHAR(256)",
                "project_id": "VARCHAR(256)",
                "project_name": "VARCHAR(512)",
                "history_state": "VARCHAR(32) NOT NULL DEFAULT 'local'",
                "native_kind": "VARCHAR(32)",
                "ephemeral": "BOOLEAN NOT NULL DEFAULT 0",
                "control_state": "VARCHAR(32) NOT NULL DEFAULT 'unknown'",
                "selected_model_provider": "VARCHAR(160)",
                "selected_model": "VARCHAR(160)",
                "selected_reasoning_effort": "VARCHAR(32)",
            },
            "ssh_connections": {
                "display_name": "VARCHAR(256)",
                "profile_name": "VARCHAR(128)",
                "remote_os": "VARCHAR(64)",
                "agent_id": "VARCHAR(256)",
                "runtime_id": "VARCHAR(256)",
            },
        }
        with self.engine.begin() as db:
            for table, columns in additions.items():
                present = {
                    str(row[1]) for row in db.exec_driver_sql(f"PRAGMA table_info({table})").fetchall()
                }
                for name, definition in columns.items():
                    if name not in present:
                        db.exec_driver_sql(f"ALTER TABLE {table} ADD COLUMN {name} {definition}")
            db.exec_driver_sql(
                "UPDATE agents SET source_id = id WHERE source_id IS NULL OR source_id = ''"
            )
            # Older side-chat builds dropped ephemeral before persistence.
            # Only the exact application-generated marker is a legacy match.
            db.exec_driver_sql(
                "UPDATE sessions SET ephemeral = 1 WHERE title = ? AND ephemeral = 0",
                ("[侧边聊天]",),
            )
            db.exec_driver_sql(
                "UPDATE agents SET runtime_id = id WHERE runtime_id IS NULL OR runtime_id = ''"
            )
            db.exec_driver_sql(
                "UPDATE sessions SET source_id = (SELECT source_id FROM agents WHERE agents.id = sessions.agent_id) "
                "WHERE source_id IS NULL OR source_id = ''"
            )
            db.exec_driver_sql(
                "UPDATE sessions SET source_session_id = id WHERE source_session_id IS NULL OR source_session_id = ''"
            )
            db.exec_driver_sql(
                "UPDATE ssh_connections SET profile_name = 'default' "
                "WHERE profile_name IS NULL OR profile_name = ''"
            )
            legacy = db.execute(text("SELECT id FROM ssh_connections WHERE id = 'default'")).fetchone()
            if legacy is not None:
                migrated_id = f"ssh-{uuid4().hex[:24]}"
                db.execute(
                    text("UPDATE ssh_connections SET id = :new_id WHERE id = 'default'"),
                    {"new_id": migrated_id},
                )

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

    @staticmethod
    def _validate_daemon_checkpoint_key(daemon_id: str, session_id: str) -> None:
        if not isinstance(daemon_id, str) or not daemon_id or len(daemon_id) > 128:
            raise ValueError("daemon_id must be a non-empty string up to 128 characters")
        if not isinstance(session_id, str) or not session_id or len(session_id) > 256:
            raise ValueError("session_id must be a non-empty string up to 256 characters")

    def get_daemon_checkpoint(self, daemon_id: str, session_id: str) -> int:
        self._validate_daemon_checkpoint_key(daemon_id, session_id)
        with self.session() as db:
            row = db.get(
                DaemonCheckpointRow,
                {"daemon_id": daemon_id, "session_id": session_id},
            )
            return row.seq_id if row is not None else 0

    def list_daemon_checkpoints(self, daemon_id: str) -> dict[str, int]:
        self._validate_daemon_checkpoint_key(daemon_id, "checkpoint-probe")
        with self.session() as db:
            rows = db.scalars(
                select(DaemonCheckpointRow)
                .where(DaemonCheckpointRow.daemon_id == daemon_id)
                .order_by(DaemonCheckpointRow.session_id)
            ).all()
            return {row.session_id: row.seq_id for row in rows}

    def set_daemon_checkpoint(self, daemon_id: str, session_id: str, seq_id: int) -> int:
        self._validate_daemon_checkpoint_key(daemon_id, session_id)
        if not isinstance(seq_id, int) or isinstance(seq_id, bool) or seq_id < 0:
            raise ValueError("seq_id must be a non-negative integer")
        with self.session() as db:
            row = db.get(
                DaemonCheckpointRow,
                {"daemon_id": daemon_id, "session_id": session_id},
            )
            if row is None:
                row = DaemonCheckpointRow(
                    daemon_id=daemon_id,
                    session_id=session_id,
                    seq_id=seq_id,
                    updated_at=utc_now(),
                )
                db.add(row)
            elif seq_id > row.seq_id:
                row.seq_id = seq_id
                row.updated_at = utc_now()
            db.flush()
            return row.seq_id

    def get_agent(self, agent_id: str) -> dict[str, Any] | None:
        with self.session() as db:
            row = db.get(AgentRow, agent_id)
            return _agent_wire(row) if row else None

    def list_agents(self) -> list[dict[str, Any]]:
        with self.session() as db:
            rows = db.scalars(select(AgentRow).order_by(AgentRow.id)).all()
            return [_agent_wire(row) for row in rows]

    def reconcile_legacy_local_source(
        self, *, source_id: str, current_agent_id: str, profile_name: str
    ) -> dict[str, list[dict[str, Any]]]:
        """Attach provenance-free local preview rows to the active stable source.

        Old Astrorder rows used ``local-hermes-<runtime>`` as both source and runtime identity.
        Only that explicit legacy shape is eligible: rows with a profile or connection provenance,
        non-local agent IDs, and already-stable source IDs are left untouched.  Agent/session and
        message IDs remain unchanged so history and opaque routing still resolve to their original
        rows; only source grouping and control state are reconciled.
        """
        if not source_id or not current_agent_id or not profile_name:
            return {"agents": [], "sessions": []}
        reconciled_agents: list[dict[str, Any]] = []
        reconciled_sessions: list[dict[str, Any]] = []
        with self.session() as db:
            current = db.get(AgentRow, current_agent_id)
            if current is None:
                return {"agents": [], "sessions": []}
            candidates = db.scalars(select(AgentRow).where(AgentRow.kind == "hermes")).all()
            for agent in candidates:
                if agent.id == current_agent_id or not agent.id.startswith("local-hermes-"):
                    continue
                if agent.connection_id is not None or agent.profile_name is not None:
                    continue
                if (agent.source_id or agent.id) != agent.id:
                    continue
                agent.source_id = source_id
                agent.profile_name = profile_name
                agent.control_state = "readonly"
                agent.status = "disconnected"
                agent.updated_at = utc_now()
                reconciled_agents.append(_agent_wire(agent))
                sessions = db.scalars(
                    select(SessionRow).where(SessionRow.agent_id == agent.id)
                ).all()
                for session in sessions:
                    session.source_id = source_id
                    session.connection_id = None
                    session.control_state = "readonly"
                    session.updated_at = session.updated_at or utc_now()
                    reconciled_sessions.append(_session_wire(session))
        return {"agents": reconciled_agents, "sessions": reconciled_sessions}

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
                    source_id=data.get("source_id") or data["id"],
                    connection_id=data.get("connection_id"),
                    profile_name=data.get("profile_name"),
                    runtime_id=data.get("runtime_id") or data["id"],
                    control_state=data.get("control_state", "unknown"),
                    updated_at=utc_now(),
                )
                db.add(row)
            else:
                row.kind = data["kind"]
                row.name = data["name"]
                row.status = status or data["status"]
                row.capabilities = list(data.get("capabilities", []))
                row.limitation = data.get("limitation")
                row.source_id = data.get("source_id") or row.source_id or row.id
                row.connection_id = data.get("connection_id")
                row.profile_name = data.get("profile_name")
                row.runtime_id = data.get("runtime_id") or row.runtime_id or row.id
                row.control_state = data.get("control_state", row.control_state or "unknown")
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

    def update_session(self, agent_id: str, session_id: str, updates: dict[str, Any]) -> dict[str, Any] | None:
        with self.session() as db:
            row = db.execute(
                select(SessionRow).where(SessionRow.agent_id == agent_id, SessionRow.id == session_id)
            ).scalar_one_or_none()
            if row is None:
                return None
            if "title" in updates and isinstance(updates["title"], str):
                row.title = updates["title"].strip()
            if "status" in updates and isinstance(updates["status"], str):
                row.status = updates["status"]
            if "workspace" in updates:
                row.workspace = updates["workspace"]
            row.updated_at = utc_now()
            db.flush()
            return _session_wire(row)

    def delete_session(self, agent_id: str, session_id: str) -> bool:
        with self.session() as db:
            row = db.execute(
                select(SessionRow).where(SessionRow.agent_id == agent_id, SessionRow.id == session_id)
            ).scalar_one_or_none()
            if row is None:
                return False
            db.delete(row)
            # 级联清除该会话的消息与指令
            for msg in db.scalars(select(MessageRow).where(MessageRow.agent_id == agent_id, MessageRow.session_id == session_id)).all():
                db.delete(msg)
            for cmd in db.scalars(select(CommandRow).where(CommandRow.agent_id == agent_id, CommandRow.session_id == session_id)).all():
                db.delete(cmd)
            for task in db.scalars(select(TaskRow).where(TaskRow.agent_id == agent_id, TaskRow.session_id == session_id)).all():
                db.delete(task)
            db.flush()
            return True

    def find_session_by_id(self, session_id: str) -> dict[str, Any] | None:
        with self.session() as db:
            row = db.execute(
                select(SessionRow).where(SessionRow.id == session_id).order_by(SessionRow.row_id.desc())
            ).scalars().first()
            return _session_wire(row) if row else None

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
            result: list[dict[str, Any]] = []
            seen: set[tuple[str, str]] = set()
            for row in db.scalars(query).all():
                # 只有当具有相同的 session id 时才去重；
                # 绝不能用 (source_id, source_session_id) 去重把同 native 会话的真正 session 顶掉
                key = (row.agent_id, row.id)
                if key in seen:
                    continue
                seen.add(key)
                result.append(_session_wire(row))
            return result

    def list_projects(self) -> list[dict[str, Any]]:
        with self.session() as db:
            rows = db.scalars(select(ProjectRow).order_by(ProjectRow.updated_at.desc(), ProjectRow.id)).all()
            return [_project_wire(row) for row in rows]

    def delete_project(
        self,
        *,
        project_key: str | None = None,
        project_id: str | None = None,
        source_id: str | None = None,
        workspace: str | None = None,
        session_keys: list[tuple[str, str]] | None = None,
        delete_sessions: bool = True,
    ) -> tuple[bool, list[dict[str, Any]]]:
        deleted_sessions: list[dict[str, Any]] = []
        found_project = False
        with self.session() as db:
            project_rows: list[ProjectRow] = []
            if project_key:
                row = db.get(ProjectRow, project_key)
                if row is not None:
                    project_rows.append(row)
                elif "\0" in project_key:
                    parts = project_key.split("\0", 1)
                    src = parts[0].removeprefix("project:").removeprefix("workspace:")
                    pid = parts[1]
                    matched = db.scalars(
                        select(ProjectRow).where(
                            ProjectRow.source_id == src,
                            ProjectRow.project_id == pid,
                        )
                    ).all()
                    project_rows.extend(matched)

            if not project_rows and project_id:
                query = select(ProjectRow).where(ProjectRow.project_id == project_id)
                if source_id:
                    query = query.where(ProjectRow.source_id == source_id)
                project_rows.extend(db.scalars(query).all())

            if not project_rows and workspace:
                project_rows.extend(
                    db.scalars(select(ProjectRow).where(ProjectRow.workspace == workspace)).all()
                )

            seen_pids = set()
            unique_project_rows: list[ProjectRow] = []
            for prow in project_rows:
                if prow.id not in seen_pids:
                    seen_pids.add(prow.id)
                    unique_project_rows.append(prow)

            if unique_project_rows:
                found_project = True
                for prow in unique_project_rows:
                    db.delete(prow)

            if delete_sessions:
                candidate_sessions: list[SessionRow] = []
                if session_keys:
                    for agent_id, sess_id in session_keys:
                        srow = db.execute(
                            select(SessionRow).where(
                                SessionRow.agent_id == agent_id, SessionRow.id == sess_id
                            )
                        ).scalar_one_or_none()
                        if srow is not None:
                            candidate_sessions.append(srow)

                for prow in unique_project_rows:
                    if prow.project_id and prow.project_id != "__no_project__":
                        q = select(SessionRow).where(SessionRow.project_id == prow.project_id)
                        if prow.source_id:
                            q = q.where(SessionRow.source_id == prow.source_id)
                        candidate_sessions.extend(db.scalars(q).all())
                    if prow.workspace:
                        candidate_sessions.extend(
                            db.scalars(select(SessionRow).where(SessionRow.workspace == prow.workspace)).all()
                        )

                if project_id and project_id != "__no_project__":
                    q = select(SessionRow).where(SessionRow.project_id == project_id)
                    if source_id:
                        q = q.where(SessionRow.source_id == source_id)
                    candidate_sessions.extend(db.scalars(q).all())

                if workspace:
                    candidate_sessions.extend(
                        db.scalars(select(SessionRow).where(SessionRow.workspace == workspace)).all()
                    )

                seen_sessions = set()
                for srow in candidate_sessions:
                    skey = (srow.agent_id, srow.id)
                    if skey in seen_sessions:
                        continue
                    seen_sessions.add(skey)
                    deleted_sessions.append({"agent_id": srow.agent_id, "id": srow.id, "title": srow.title})
                    for msg in db.scalars(
                        select(MessageRow).where(
                            MessageRow.agent_id == srow.agent_id, MessageRow.session_id == srow.id
                        )
                    ).all():
                        db.delete(msg)
                    for cmd in db.scalars(
                        select(CommandRow).where(
                            CommandRow.agent_id == srow.agent_id, CommandRow.session_id == srow.id
                        )
                    ).all():
                        db.delete(cmd)
                    for task in db.scalars(
                        select(TaskRow).where(
                            TaskRow.agent_id == srow.agent_id, TaskRow.session_id == srow.id
                        )
                    ).all():
                        db.delete(task)
                    db.delete(srow)

            db.flush()
            return found_project or bool(deleted_sessions), deleted_sessions

    def upsert_projects(self, projects: list[dict[str, Any]]) -> list[dict[str, Any]]:
        canonical: list[dict[str, Any]] = []
        with self.session() as db:
            for data in projects:
                source_id = str(data.get("source_id") or "")
                project_id = str(data.get("project_id") or "")
                if not source_id or not project_id:
                    continue
                row_id = f"project:{source_id}\0{project_id}"
                updated_at = data.get("updated_at") or utc_now()
                if not isinstance(updated_at, datetime):
                    updated_at = parse_timestamp(str(updated_at))
                row = db.get(ProjectRow, row_id)
                values = {
                    "source_id": source_id,
                    "connection_id": data.get("connection_id"),
                    "agent_id": data.get("agent_id"),
                    "profile_name": data.get("profile_name"),
                    "project_id": project_id,
                    "project_name": data.get("project_name"),
                    "workspace": data.get("workspace"),
                    "session_count": max(0, int(data.get("session_count") or 0)),
                    "updated_at": updated_at,
                }
                if row is None:
                    row = ProjectRow(id=row_id, **values)
                    db.add(row)
                else:
                    for key, value in values.items():
                        setattr(row, key, value)
                canonical.append(_project_wire(row))
            db.flush()
            return canonical

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
                # Use insert ... on conflict do update to prevent multi-threaded race condition
                from sqlalchemy.dialects.sqlite import insert as sqlite_insert
                stmt = sqlite_insert(SessionRow).values(
                    id=data["id"],
                    agent_id=data["agent_id"],
                    title=data.get("title", ""),
                    workspace=data.get("workspace"),
                    status=data["status"],
                    source_id=data.get("source_id") or data["agent_id"],
                    connection_id=data.get("connection_id"),
                    source_session_id=data.get("source_session_id") or data["id"],
                    project_id=data.get("project_id"),
                    project_name=data.get("project_name"),
                    history_state=data.get("history_state", "local"),
                    native_kind=data.get("native_kind"),
                    ephemeral=data.get("ephemeral") is True,
                    control_state=data.get("control_state", "unknown"),
                    updated_at=updated_at,
                ).on_conflict_do_update(
                    index_elements=[SessionRow.agent_id, SessionRow.id],
                    set_={
                        "title": data.get("title", ""),
                        "workspace": data.get("workspace"),
                        "status": data["status"],
                        "source_id": data.get("source_id") or data["agent_id"],
                        "connection_id": data.get("connection_id"),
                        "source_session_id": data.get("source_session_id") or data["id"],
                        "project_id": data.get("project_id"),
                        "project_name": data.get("project_name"),
                        "history_state": data.get("history_state", "local"),
                        "native_kind": data.get("native_kind"),
                        # Native snapshots cannot promote temporary sessions.
                        "ephemeral": SessionRow.ephemeral | (data.get("ephemeral") is True),
                        "control_state": data.get("control_state", "unknown"),
                        "updated_at": updated_at,
                    }
                )
                db.execute(stmt)
                db.flush()
                row = db.execute(
                    select(SessionRow).where(
                        SessionRow.agent_id == data["agent_id"], SessionRow.id == data["id"]
                    )
                ).scalar_one()
            else:
                row.title = data.get("title", "")
                row.workspace = data.get("workspace")
                row.status = data["status"]
                row.source_id = data.get("source_id") or row.source_id or data["agent_id"]
                row.connection_id = data.get("connection_id")
                row.source_session_id = data.get("source_session_id") or row.source_session_id or data["id"]
                row.project_id = data.get("project_id")
                row.project_name = data.get("project_name")
                row.history_state = data.get("history_state", row.history_state or "local")
                row.native_kind = data.get("native_kind", row.native_kind)
                row.ephemeral = row.ephemeral or data.get("ephemeral") is True
                row.control_state = data.get("control_state", row.control_state or "unknown")
                row.updated_at = updated_at
                db.flush()
            return _session_wire(row)

    def set_session_history_state(
        self, agent_id: str, session_id: str, history_state: str
    ) -> dict[str, Any] | None:
        with self.session() as db:
            row = db.execute(
                select(SessionRow).where(
                    SessionRow.agent_id == agent_id,
                    SessionRow.id == session_id,
                )
            ).scalar_one_or_none()
            if row is None:
                return None
            row.history_state = history_state
            row.updated_at = utc_now()
            db.flush()
            return _session_wire(row)

    def set_session_model_binding(
        self, agent_id: str, session_id: str, provider: str, model: str
    ) -> dict[str, str]:
        with self.session() as db:
            row = db.execute(
                select(SessionRow).where(SessionRow.agent_id == agent_id, SessionRow.id == session_id)
            ).scalar_one_or_none()
            if row is None:
                raise ScopeNotFound("session not found")
            row.selected_model_provider = provider
            row.selected_model = model
            db.flush()
            return {"provider": provider, "model": model}

    def set_session_reasoning_binding(
        self, agent_id: str, session_id: str, effort: str
    ) -> dict[str, str]:
        with self.session() as db:
            row = db.execute(
                select(SessionRow).where(SessionRow.agent_id == agent_id, SessionRow.id == session_id)
            ).scalar_one_or_none()
            if row is None:
                raise ScopeNotFound("session not found")
            row.selected_reasoning_effort = effort
            db.flush()
            return {"effort": effort}

    def get_session_model_binding(self, agent_id: str, session_id: str) -> dict[str, str] | None:
        with self.session() as db:
            row = db.execute(
                select(SessionRow).where(SessionRow.agent_id == agent_id, SessionRow.id == session_id)
            ).scalar_one_or_none()
            if row is None or not row.selected_model_provider or not row.selected_model:
                return None
            binding = {"provider": row.selected_model_provider, "model": row.selected_model}
            if row.selected_reasoning_effort:
                binding["effort"] = row.selected_reasoning_effort
            return binding

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

    def upsert_task(self, data: dict[str, Any]) -> dict[str, Any]:
        with self.session() as db:
            return self._upsert_task_in(db, data)

    def list_tasks(self, agent_id: str, session_id: str) -> list[dict[str, Any]]:
        with self.session() as db:
            rows = db.scalars(
                select(TaskRow)
                .where(TaskRow.agent_id == agent_id, TaskRow.session_id == session_id)
                .order_by(TaskRow.updated_at.desc(), TaskRow.row_id.desc())
            ).all()
            return [_task_wire(row) for row in rows]

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

    def get_ssh_connection(self, connection_id: str | None = None) -> dict[str, Any] | None:
        with self.session() as db:
            if connection_id is None:
                row = db.scalars(select(SshConnectionRow).order_by(SshConnectionRow.id)).first()
            else:
                row = db.get(SshConnectionRow, connection_id)
            return _ssh_connection_wire(row) if row is not None else None

    def list_ssh_connections(self) -> list[dict[str, Any]]:
        with self.session() as db:
            rows = db.scalars(select(SshConnectionRow).order_by(SshConnectionRow.id)).all()
            return [_ssh_connection_wire(row) for row in rows]

    def save_ssh_connection(
        self,
        settings: dict[str, object],
        *,
        connection_id: str | None = None,
        state: str,
        detail: str,
    ) -> dict[str, Any]:
        with self.session() as db:
            row = db.get(SshConnectionRow, connection_id) if connection_id else None
            if row is None:
                row = SshConnectionRow(
                    id=connection_id or f"ssh-{uuid4().hex[:24]}",
                    port=22,
                    state=state,
                    detail=detail,
                    updated_at=utc_now(),
                )
                db.add(row)
            row.display_name = settings.get("display_name") or None
            row.profile_name = settings.get("profile_name") or "default"
            row.host = settings.get("host") or None
            row.port = int(settings["port"])
            row.user = settings.get("user") or None
            row.ssh_config_alias = settings.get("ssh_config_alias") or None
            row.identity_file = settings.get("identity_file") or None
            row.hermes_path = settings.get("hermes_path") or None
            row.workspace = settings.get("workspace") or None
            row.state = state
            row.detail = detail
            row.updated_at = utc_now()
            db.flush()
            return _ssh_connection_wire(row)

    def update_ssh_connection_state(
        self,
        connection_id: str | None,
        state: str,
        detail: str,
        *,
        remote_os: str | None = None,
        agent_id: str | None = None,
        runtime_id: str | None = None,
    ) -> dict[str, Any] | None:
        with self.session() as db:
            row = db.get(SshConnectionRow, connection_id) if connection_id else None
            if row is None and connection_id is None:
                row = db.scalars(select(SshConnectionRow).order_by(SshConnectionRow.id)).first()
            if row is None:
                return None
            row.state = state
            row.detail = detail
            if remote_os is not None:
                row.remote_os = remote_os
            if agent_id is not None:
                row.agent_id = agent_id
            if runtime_id is not None:
                row.runtime_id = runtime_id
            row.updated_at = utc_now()
            db.flush()
            return _ssh_connection_wire(row)

    def append_connection_history(
        self,
        connection_id: str,
        stage: str,
        state: str,
        detail: str,
        details: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        safe_detail = _redact_text(str(detail))
        safe_details = _redact_structured(details or {})
        with self.session() as db:
            row = ConnectionHistoryRow(
                id=f"history-{uuid4()}",
                connection_id=connection_id,
                stage=str(stage)[:64],
                state=str(state)[:32],
                detail=safe_detail,
                details=safe_details if isinstance(safe_details, dict) else {},
                created_at=utc_now(),
            )
            db.add(row)
            db.flush()
            return _connection_history_wire(row)

    def list_connection_history(
        self, connection_id: str, before: str | None, limit: int
    ) -> tuple[list[dict[str, Any]], str | None]:
        with self.session() as db:
            query = select(ConnectionHistoryRow).where(
                ConnectionHistoryRow.connection_id == connection_id
            )
            if before is not None:
                query = query.where(ConnectionHistoryRow.row_id < decode_history_cursor(before))
            rows = db.scalars(
                query.order_by(ConnectionHistoryRow.row_id.desc()).limit(limit + 1)
            ).all()
            has_more = len(rows) > limit
            rows = rows[:limit]
            rows.reverse()
            next_cursor = encode_history_cursor(rows[0].row_id) if has_more and rows else None
            return [_connection_history_wire(row) for row in rows], next_cursor

    def delete_ssh_connection(self, connection_id: str) -> bool:
        with self.session() as db:
            row = db.get(SshConnectionRow, connection_id)
            if row is None:
                return False
            db.delete(row)
            return True

    def mark_ssh_connections_disconnected(self) -> None:
        with self.session() as db:
            rows = db.scalars(
                select(SshConnectionRow).where(
                    SshConnectionRow.state.in_({"connecting", "connected", "ready"})
                )
            ).all()
            for row in rows:
                row.state = "disconnected"
                row.detail = "Astrorder restarted; reconnect explicitly"
                row.agent_id = None
                row.runtime_id = None
                row.updated_at = utc_now()

    def set_command_state(
        self,
        agent_id: str,
        session_id: str,
        command_id: str,
        state: str,
        error: str | None,
        *,
        preserve_progress: bool = False,
    ) -> dict[str, Any]:
        with self.session() as db:
            if preserve_progress:
                blocked = {'completed', 'failed', 'cancelled'}
                if state == 'accepted':
                    blocked.add('running')
                db.execute(update(CommandRow).where(CommandRow.agent_id == agent_id, CommandRow.session_id == session_id, CommandRow.id == command_id, CommandRow.state.not_in(blocked)).values(state=state, error=error, updated_at=utc_now()))
            row = db.execute(
                select(CommandRow).where(
                    CommandRow.agent_id == agent_id,
                    CommandRow.session_id == session_id,
                    CommandRow.id == command_id,
                )
            ).scalar_one_or_none()
            if row is None:
                raise UnknownCommand(command_id)
            if not preserve_progress:
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
                anchor = db.scalar(query.where(MessageRow.row_id == decode_history_cursor(before)))
                if anchor is None:
                    raise ValueError("History cursor is outside this session")
                query = query.where((MessageRow.created_at < anchor.created_at) | ((MessageRow.created_at == anchor.created_at) & (MessageRow.row_id < anchor.row_id)))
            rows = db.scalars(query.order_by(MessageRow.created_at.desc(), MessageRow.row_id.desc()).limit(limit + 1)).all()
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
                        source_id=data.get("source_id") or agent_id,
                        connection_id=data.get("connection_id"),
                        profile_name=data.get("profile_name"),
                        runtime_id=data.get("runtime_id") or agent_id,
                        control_state=data.get("control_state", "unknown"),
                        updated_at=utc_now(),
                    )
                    db.add(row)
                else:
                    row.kind = data["kind"]
                    row.name = data["name"]
                    row.status = data["status"]
                    row.capabilities = list(data.get("capabilities", []))
                    row.limitation = data.get("limitation")
                    row.source_id = data.get("source_id") or row.source_id or agent_id
                    row.connection_id = data.get("connection_id")
                    row.profile_name = data.get("profile_name")
                    row.runtime_id = data.get("runtime_id") or row.runtime_id or agent_id
                    row.control_state = data.get("control_state", row.control_state or "unknown")
                    row.updated_at = utc_now()
                db.flush()
                canonical = _agent_wire(row)
            elif event_type == "session.upsert":
                canonical = self._upsert_session_in(db, data)
            elif event_type == "session.delete":
                canonical = data
                del_id = data.get("id") or session_id
                if del_id:
                    row = db.execute(
                        select(SessionRow).where(SessionRow.agent_id == agent_id, SessionRow.id == del_id)
                    ).scalar_one_or_none()
                    if row is not None:
                        db.delete(row)
            elif event_type == "message.upsert":
                canonical = self._upsert_message_in(db, data)
            elif event_type == "task.upsert":
                canonical = self._upsert_task_in(db, data)
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

    def _upsert_task_in(self, db: Session, data: dict[str, Any]) -> dict[str, Any]:
        row = db.execute(
            select(TaskRow).where(
                TaskRow.agent_id == data["agent_id"],
                TaskRow.session_id == data["session_id"],
                TaskRow.id == data["id"],
            )
        ).scalar_one_or_none()
        created_at = data["created_at"]
        updated_at = data["updated_at"]
        if not isinstance(created_at, datetime):
            created_at = parse_timestamp(str(created_at))
        if not isinstance(updated_at, datetime):
            updated_at = parse_timestamp(str(updated_at))
        values = {
            "kind": data["kind"],
            "title": data.get("title", ""),
            "status": data["status"],
            "progress": data.get("progress"),
            "command": data.get("command"),
            "logs": list(data.get("logs", [])),
            "target_id": data.get("target_id"),
            "created_at": created_at,
            "updated_at": updated_at,
        }
        if row is None:
            row = TaskRow(
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
        return _task_wire(row)

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
                source_id=data.get("source_id") or data["agent_id"],
                connection_id=data.get("connection_id"),
                source_session_id=data.get("source_session_id") or data["id"],
                project_id=data.get("project_id"),
                project_name=data.get("project_name"),
                history_state=data.get("history_state", "local"),
                ephemeral=data.get("ephemeral") is True,
                control_state=data.get("control_state", "unknown"),
                updated_at=updated_at,
            )
            db.add(row)
        else:
            if data.get("title") and data["title"] != data["id"]:
                row.title = data["title"]
            if "workspace" in data and ("project_id" in data or not row.project_id):
                row.workspace = data["workspace"]
            row.status = data["status"]
            row.source_id = data.get("source_id") or row.source_id or data["agent_id"]
            row.connection_id = data.get("connection_id")
            row.source_session_id = data.get("source_session_id") or row.source_session_id or data["id"]
            if "project_id" in data:
                row.project_id = data["project_id"]
            if "project_name" in data:
                row.project_name = data["project_name"]
            row.history_state = data.get("history_state", row.history_state or "local")
            row.ephemeral = row.ephemeral or data.get("ephemeral") is True
            row.control_state = data.get("control_state", row.control_state or "unknown")
            row.updated_at = updated_at
        db.flush()
        return _session_wire(row)

    def _upsert_message_in(self, db: Session, data: dict[str, Any]) -> dict[str, Any]:
        agent_id = data["agent_id"]
        session_id = data["session_id"]
        target_session_id = session_id

        row = db.execute(
            select(MessageRow).where(
                MessageRow.agent_id == agent_id,
                MessageRow.session_id == target_session_id,
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
                id=data["id"], session_id=target_session_id, agent_id=agent_id, **values
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
