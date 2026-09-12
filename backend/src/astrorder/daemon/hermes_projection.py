"""App-side projector for daemon-owned Hermes command lifecycle frames."""
from __future__ import annotations

import threading
from collections.abc import Mapping
from typing import Any

from .bridge import DaemonBridge, DaemonBridgeError


class HermesCommandFrameRouter:
    """Project exact native completion frames into the durable App command store."""

    def __init__(self, bridge: DaemonBridge, store: Any, service: Any) -> None:
        self.store = store
        self.service = service
        self._lock = threading.RLock()
        self._unregister = bridge.register_native_frame_handler(
            "hermes.command_complete", self._on_completion
        )

    def close(self) -> None:
        with self._lock:
            unregister, self._unregister = self._unregister, None
        if unregister is not None:
            unregister()

    def _on_completion(self, frame_session_id: str, payload: Mapping[str, Any]) -> None:
        agent_id = payload.get("agent_id")
        session_id = payload.get("session_id")
        command_id = payload.get("command_id")
        if not isinstance(agent_id, str) or not agent_id:
            raise DaemonBridgeError("Hermes completion agent identity is invalid")
        if not isinstance(session_id, str) or not session_id or session_id != frame_session_id:
            raise DaemonBridgeError("Hermes completion session identity does not match")
        if not isinstance(command_id, str) or not command_id:
            raise DaemonBridgeError("Hermes completion command identity is invalid")
        command = self.store.get_command(agent_id, session_id, command_id)
        if command is None:
            raise DaemonBridgeError("Hermes completion command was not found")
        if command.get("state") in {"completed", "failed", "cancelled"}:
            return
        updated = self.store.set_command_state(
            agent_id,
            session_id,
            command_id,
            "completed",
            None,
        )
        self.service._server_event(
            "command.upsert",
            agent_id=agent_id,
            session_id=session_id,
            data=updated,
        )
