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
    session_key: str | None = None

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
                "ws://127.0.0.1:8765/ws/v1/connector",
            ),
            secret=secret,
            agent_id=agent_id,
            agent_name=os.environ.get("ASTRORDER_HERMES_AGENT_NAME", "Hermes"),
            workspace=os.environ.get("ASTRORDER_HERMES_WORKSPACE") or None,
            session_key=os.environ.get("ASTRORDER_HERMES_SESSION_KEY") or None,
        )
