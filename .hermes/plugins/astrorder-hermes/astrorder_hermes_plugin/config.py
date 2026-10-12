from __future__ import annotations

import os
from dataclasses import dataclass


@dataclass(frozen=True)
class HermesConnectorConfig:
    endpoint: str
    secret: str
    agent_id: str
    agent_name: str
    workspace: str | None = None
    source_id: str | None = None
    connection_id: str | None = None
    profile_name: str | None = None
    session_key: str | None = None
    connect_on_register: bool = True

    @classmethod
    def from_env(cls) -> HermesConnectorConfig:
        secret = os.environ.get("ASTRORDER_CONNECTOR_SECRET")
        if not secret:
            raise ValueError("ASTRORDER_CONNECTOR_SECRET is required")
        agent_id = os.environ.get("ASTRORDER_HERMES_AGENT_ID")
        if not agent_id:
            raise ValueError("ASTRORDER_HERMES_AGENT_ID is required")
        return cls(
            endpoint=os.environ.get(
                "ASTRORDER_CONNECTOR_ENDPOINT",
                "ws://127.0.0.1:30002/ws/v1/connector",
            ),
            secret=secret,
            agent_id=agent_id,
            agent_name=os.environ.get("ASTRORDER_HERMES_AGENT_NAME", "Hermes"),
            workspace=os.environ.get("ASTRORDER_HERMES_WORKSPACE") or None,
            source_id=os.environ.get("ASTRORDER_HERMES_SOURCE_ID") or None,
            connection_id=os.environ.get("ASTRORDER_HERMES_CONNECTION_ID") or None,
            profile_name=os.environ.get("ASTRORDER_HERMES_PROFILE_NAME") or None,
            session_key=os.environ.get("ASTRORDER_HERMES_SESSION_KEY") or None,
            connect_on_register=os.environ.get("ASTRORDER_HERMES_CONNECT_ON_REGISTER", "1")
            .strip()
            .lower()
            in {"1", "true", "yes", "on"},
        )
