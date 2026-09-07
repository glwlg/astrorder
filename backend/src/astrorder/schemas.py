from __future__ import annotations

from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator


class WireModel(BaseModel):
    model_config = ConfigDict(extra="ignore")


class AgentModel(WireModel):
    id: str = Field(min_length=1, max_length=256)
    kind: Literal["hermes", "codex"]
    name: str = Field(min_length=1, max_length=256)
    status: Literal["disconnected", "connecting", "ready", "error"]
    capabilities: list[str] = Field(default_factory=list, max_length=32)
    limitation: str | None = Field(default=None, max_length=2000)


class SessionModel(WireModel):
    id: str = Field(min_length=1, max_length=256)
    agent_id: str = Field(min_length=1, max_length=256)
    title: str = Field(default="", max_length=512)
    workspace: str | None = Field(default=None, max_length=2000)
    status: Literal["idle", "running", "waiting_approval", "error"]
    updated_at: str = Field(min_length=1, max_length=64)


class AttachmentModel(WireModel):
    id: str = Field(min_length=1, max_length=256)
    name: str = Field(min_length=1, max_length=256)
    media_type: str = Field(min_length=1, max_length=128)
    url: str = Field(min_length=1, max_length=512)


class MessageModel(WireModel):
    id: str = Field(min_length=1, max_length=256)
    session_id: str = Field(min_length=1, max_length=256)
    agent_id: str = Field(min_length=1, max_length=256)
    role: Literal["user", "assistant", "system", "tool"]
    kind: Literal["message", "thinking", "tool"]
    text: str = Field(default="", max_length=2_000_000)
    attachments: list[AttachmentModel] = Field(default_factory=list, max_length=32)
    created_at: str = Field(min_length=1, max_length=64)
    command_id: str | None = Field(default=None, max_length=256)
    tool: dict[str, Any] | None = None


CommandAction = Literal["send", "enqueue", "stop", "approve", "cancel"]
CommandState = Literal[
    "received",
    "queued",
    "accepted",
    "running",
    "completed",
    "failed",
    "unknown",
    "cancelled",
]


class CommandSubmission(BaseModel):
    model_config = ConfigDict(extra="forbid")

    id: str = Field(min_length=1, max_length=256)
    agent_id: str = Field(min_length=1, max_length=256)
    session_id: str = Field(min_length=1, max_length=256)
    action: CommandAction
    text: str = Field(default="", max_length=2_000_000)
    attachment_ids: list[str] = Field(default_factory=list, max_length=32)
    target_id: str | None = Field(default=None, max_length=256)

    @field_validator("attachment_ids")
    @classmethod
    def validate_attachment_ids(cls, value: list[str]) -> list[str]:
        if any(not item or len(item) > 256 for item in value):
            raise ValueError("attachment_ids must contain non-empty IDs")
        return value


class AuthRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    token: str = Field(min_length=1, max_length=4096)


class RuntimeLaunch(BaseModel):
    model_config = ConfigDict(extra="forbid")

    kind: Literal["hermes", "codex"]
    workspace: str = Field(min_length=1, max_length=2000)
