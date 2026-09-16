from __future__ import annotations

from datetime import datetime
from typing import Any

from sqlalchemy import JSON, Boolean, DateTime, Integer, String, Text, UniqueConstraint
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column


class Base(DeclarativeBase):
    pass


class WorkspacePreferenceRow(Base):
    __tablename__ = "workspace_preferences"

    key: Mapped[str] = mapped_column(String(1100), primary_key=True)
    value: Mapped[Any] = mapped_column(JSON, nullable=True)


class AgentRow(Base):
    __tablename__ = "agents"

    id: Mapped[str] = mapped_column(String(256), primary_key=True)
    kind: Mapped[str] = mapped_column(String(32), nullable=False)
    name: Mapped[str] = mapped_column(String(256), nullable=False)
    status: Mapped[str] = mapped_column(String(32), nullable=False)
    capabilities: Mapped[list[str]] = mapped_column(JSON, nullable=False, default=list)
    limitation: Mapped[str | None] = mapped_column(Text, nullable=True)
    source_id: Mapped[str | None] = mapped_column(String(256), nullable=True, index=True)
    connection_id: Mapped[str | None] = mapped_column(String(64), nullable=True, index=True)
    profile_name: Mapped[str | None] = mapped_column(String(128), nullable=True)
    runtime_id: Mapped[str | None] = mapped_column(String(256), nullable=True)
    control_state: Mapped[str] = mapped_column(String(32), nullable=False, default="unknown")
    updated_at: Mapped[datetime] = mapped_column(DateTime, nullable=False)


class AgentConnectionChoice(Base):
    __tablename__ = 'agent_connection_choices'
    id: Mapped[str] = mapped_column(String(256), primary_key=True)
    enabled: Mapped[int] = mapped_column(Integer, nullable=False)


class SessionRow(Base):
    __tablename__ = "sessions"
    __table_args__ = (UniqueConstraint("agent_id", "id", name="uq_sessions_agent_id"),)

    row_id: Mapped[int] = mapped_column(Integer, primary_key=True, autoincrement=True)
    id: Mapped[str] = mapped_column(String(256), nullable=False)
    agent_id: Mapped[str] = mapped_column(String(256), nullable=False, index=True)
    title: Mapped[str] = mapped_column(String(512), nullable=False)
    workspace: Mapped[str | None] = mapped_column(Text, nullable=True)
    status: Mapped[str] = mapped_column(String(32), nullable=False)
    source_id: Mapped[str | None] = mapped_column(String(256), nullable=True, index=True)
    connection_id: Mapped[str | None] = mapped_column(String(64), nullable=True, index=True)
    source_session_id: Mapped[str | None] = mapped_column(String(256), nullable=True, index=True)
    project_id: Mapped[str | None] = mapped_column(String(256), nullable=True, index=True)
    project_name: Mapped[str | None] = mapped_column(String(512), nullable=True)
    history_state: Mapped[str] = mapped_column(String(32), nullable=False, default="local")
    native_kind: Mapped[str | None] = mapped_column(String(32), nullable=True)
    ephemeral: Mapped[bool] = mapped_column(Boolean, nullable=False, default=False, server_default="0")
    control_state: Mapped[str] = mapped_column(String(32), nullable=False, default="unknown")
    selected_model_provider: Mapped[str | None] = mapped_column(String(160), nullable=True)
    selected_model: Mapped[str | None] = mapped_column(String(160), nullable=True)
    selected_reasoning_effort: Mapped[str | None] = mapped_column(String(32), nullable=True)
    selected_approval_mode: Mapped[str | None] = mapped_column(String(32), nullable=True)
    handoff_from_agent_id: Mapped[str | None] = mapped_column(String(256), nullable=True, index=True)
    handoff_from_session_id: Mapped[str | None] = mapped_column(String(256), nullable=True, index=True)
    handoff_context: Mapped[str | None] = mapped_column(Text, nullable=True)
    handoff_context_consumed: Mapped[bool] = mapped_column(Boolean, nullable=False, default=False, server_default="0")
    updated_at: Mapped[datetime] = mapped_column(DateTime, nullable=False)


class ProjectRow(Base):
    __tablename__ = "projects"

    id: Mapped[str] = mapped_column(String(512), primary_key=True)
    source_id: Mapped[str] = mapped_column(String(256), nullable=False, index=True)
    connection_id: Mapped[str | None] = mapped_column(String(64), nullable=True, index=True)
    agent_id: Mapped[str | None] = mapped_column(String(256), nullable=True, index=True)
    profile_name: Mapped[str | None] = mapped_column(String(128), nullable=True)
    project_id: Mapped[str] = mapped_column(String(256), nullable=False)
    project_name: Mapped[str | None] = mapped_column(String(512), nullable=True)
    workspace: Mapped[str | None] = mapped_column(Text, nullable=True)
    session_count: Mapped[int] = mapped_column(Integer, nullable=False, default=0)
    updated_at: Mapped[datetime] = mapped_column(DateTime, nullable=False)


class AttachmentRow(Base):
    __tablename__ = "attachments"

    id: Mapped[str] = mapped_column(String(256), primary_key=True)
    name: Mapped[str] = mapped_column(String(256), nullable=False)
    media_type: Mapped[str] = mapped_column(String(128), nullable=False)
    size: Mapped[int] = mapped_column(Integer, nullable=False)
    storage_name: Mapped[str] = mapped_column(String(512), nullable=False, unique=True)
    created_at: Mapped[datetime] = mapped_column(DateTime, nullable=False)


class MessageRow(Base):
    __tablename__ = "messages"
    __table_args__ = (UniqueConstraint("agent_id", "session_id", "id", name="uq_messages_scope"),)

    row_id: Mapped[int] = mapped_column(Integer, primary_key=True, autoincrement=True)
    id: Mapped[str] = mapped_column(String(256), nullable=False)
    session_id: Mapped[str] = mapped_column(String(256), nullable=False, index=True)
    agent_id: Mapped[str] = mapped_column(String(256), nullable=False, index=True)
    role: Mapped[str] = mapped_column(String(32), nullable=False)
    kind: Mapped[str] = mapped_column(String(32), nullable=False)
    text: Mapped[str] = mapped_column(Text, nullable=False)
    attachments: Mapped[list[dict[str, Any]]] = mapped_column(JSON, nullable=False, default=list)
    created_at: Mapped[datetime] = mapped_column(DateTime, nullable=False)
    command_id: Mapped[str | None] = mapped_column(String(256), nullable=True)
    tool: Mapped[dict[str, Any] | None] = mapped_column(JSON, nullable=True)


class CommandRow(Base):
    __tablename__ = "commands"
    __table_args__ = (UniqueConstraint("agent_id", "session_id", "id", name="uq_commands_scope"),)

    row_id: Mapped[int] = mapped_column(Integer, primary_key=True, autoincrement=True)
    id: Mapped[str] = mapped_column(String(256), nullable=False)
    session_id: Mapped[str] = mapped_column(String(256), nullable=False, index=True)
    agent_id: Mapped[str] = mapped_column(String(256), nullable=False, index=True)
    action: Mapped[str] = mapped_column(String(32), nullable=False)
    state: Mapped[str] = mapped_column(String(32), nullable=False)
    text: Mapped[str] = mapped_column(Text, nullable=False)
    attachments: Mapped[list[dict[str, Any]]] = mapped_column(JSON, nullable=False, default=list)
    target_id: Mapped[str | None] = mapped_column(String(256), nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime, nullable=False)
    updated_at: Mapped[datetime] = mapped_column(DateTime, nullable=False)
    error: Mapped[str | None] = mapped_column(Text, nullable=True)
    payload_hash: Mapped[str] = mapped_column(String(128), nullable=False)


class TaskRow(Base):
    __tablename__ = "tasks"
    __table_args__ = (UniqueConstraint("agent_id", "session_id", "id", name="uq_tasks_scope"),)

    row_id: Mapped[int] = mapped_column(Integer, primary_key=True, autoincrement=True)
    id: Mapped[str] = mapped_column(String(256), nullable=False)
    session_id: Mapped[str] = mapped_column(String(256), nullable=False, index=True)
    agent_id: Mapped[str] = mapped_column(String(256), nullable=False, index=True)
    kind: Mapped[str] = mapped_column(String(32), nullable=False)
    title: Mapped[str] = mapped_column(String(512), nullable=False)
    status: Mapped[str] = mapped_column(String(32), nullable=False)
    progress: Mapped[dict[str, Any] | None] = mapped_column(JSON, nullable=True)
    command: Mapped[str | None] = mapped_column(Text, nullable=True)
    logs: Mapped[list[dict[str, Any]]] = mapped_column(JSON, nullable=False, default=list)
    target_id: Mapped[str | None] = mapped_column(String(256), nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime, nullable=False)
    updated_at: Mapped[datetime] = mapped_column(DateTime, nullable=False)


class SshConnectionRow(Base):
    __tablename__ = "ssh_connections"

    id: Mapped[str] = mapped_column(String(64), primary_key=True)
    display_name: Mapped[str | None] = mapped_column(String(256), nullable=True)
    profile_name: Mapped[str | None] = mapped_column(String(128), nullable=True)
    host: Mapped[str | None] = mapped_column(String(253), nullable=True)
    port: Mapped[int] = mapped_column(Integer, nullable=False, default=22)
    user: Mapped[str | None] = mapped_column(String(64), nullable=True)
    ssh_config_alias: Mapped[str | None] = mapped_column(String(128), nullable=True)
    identity_file: Mapped[str | None] = mapped_column(Text, nullable=True)
    hermes_path: Mapped[str | None] = mapped_column(Text, nullable=True)
    workspace: Mapped[str | None] = mapped_column(Text, nullable=True)
    state: Mapped[str] = mapped_column(String(32), nullable=False)
    detail: Mapped[str | None] = mapped_column(Text, nullable=True)
    remote_os: Mapped[str | None] = mapped_column(String(64), nullable=True)
    agent_id: Mapped[str | None] = mapped_column(String(256), nullable=True)
    runtime_id: Mapped[str | None] = mapped_column(String(256), nullable=True)
    updated_at: Mapped[datetime] = mapped_column(DateTime, nullable=False)


class ConnectionHistoryRow(Base):
    __tablename__ = "connection_history"

    row_id: Mapped[int] = mapped_column(Integer, primary_key=True, autoincrement=True)
    id: Mapped[str] = mapped_column(String(256), unique=True, nullable=False)
    connection_id: Mapped[str] = mapped_column(String(64), nullable=False, index=True)
    stage: Mapped[str] = mapped_column(String(64), nullable=False)
    state: Mapped[str] = mapped_column(String(32), nullable=False)
    detail: Mapped[str] = mapped_column(Text, nullable=False)
    details: Mapped[dict[str, Any]] = mapped_column(JSON, nullable=False, default=dict)
    created_at: Mapped[datetime] = mapped_column(DateTime, nullable=False)


class EventRow(Base):
    __tablename__ = "events"
    __table_args__ = (UniqueConstraint("agent_id", "event_id", name="uq_events_source_id"),)

    cursor: Mapped[int] = mapped_column(Integer, primary_key=True, autoincrement=True)
    event_id: Mapped[str] = mapped_column(String(256), nullable=False)
    type: Mapped[str] = mapped_column(String(128), nullable=False)
    agent_id: Mapped[str | None] = mapped_column(String(256), nullable=True, index=True)
    session_id: Mapped[str | None] = mapped_column(String(256), nullable=True, index=True)
    data: Mapped[dict[str, Any]] = mapped_column(JSON, nullable=False)
    created_at: Mapped[datetime] = mapped_column(DateTime, nullable=False)


class DaemonCheckpointRow(Base):
    """Durable App Server acknowledgement for one daemon WAL stream."""

    __tablename__ = "daemon_checkpoints"

    daemon_id: Mapped[str] = mapped_column(String(128), primary_key=True)
    session_id: Mapped[str] = mapped_column(String(256), primary_key=True)
    seq_id: Mapped[int] = mapped_column(Integer, nullable=False)
    updated_at: Mapped[datetime] = mapped_column(DateTime, nullable=False)
