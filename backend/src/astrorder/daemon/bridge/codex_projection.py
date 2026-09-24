"""App-side routing for daemon-owned Codex native notification frames."""
from __future__ import annotations

import threading
from collections.abc import Callable, Mapping
from typing import Any

from astrorder.daemon.bridge import DaemonBridge, DaemonBridgeError

CodexNotificationHandler = Callable[[dict[str, Any]], None]


class CodexNativeFrameRouter:
    """Route WAL-replayed Codex notifications by explicit agent and thread ID."""

    def __init__(self, bridge: DaemonBridge) -> None:
        self._handlers: dict[str, CodexNotificationHandler] = {}
        self._lock = threading.RLock()
        self._closed = False
        self._unregister = bridge.register_native_frame_handler(
            "codex.notification", self._handle_frame
        )

    def register(self, agent_id: str, handler: CodexNotificationHandler) -> Callable[[], None]:
        if not isinstance(agent_id, str) or not agent_id or len(agent_id) > 256:
            raise ValueError("Codex agent_id is invalid")
        if not callable(handler):
            raise TypeError("Codex notification handler must be callable")
        with self._lock:
            if self._closed:
                raise RuntimeError("Codex native frame router is closed")
            if agent_id in self._handlers:
                raise ValueError("Codex notification handler is already registered")
            self._handlers[agent_id] = handler

        def unregister() -> None:
            with self._lock:
                if self._handlers.get(agent_id) is handler:
                    self._handlers.pop(agent_id, None)

        return unregister

    def close(self) -> None:
        with self._lock:
            if self._closed:
                return
            self._closed = True
            self._handlers.clear()
        self._unregister()

    def _handle_frame(self, session_id: str, payload: Mapping[str, Any]) -> None:
        agent_id = payload.get("agent_id")
        frame = payload.get("frame")
        if not isinstance(agent_id, str) or not agent_id:
            raise DaemonBridgeError("Codex native frame agent identity is invalid")
        if not isinstance(frame, Mapping):
            raise DaemonBridgeError("Codex native frame payload is invalid")
        params = frame.get("params")
        if isinstance(params, Mapping):
            native_thread_id = params.get("threadId")
            if isinstance(native_thread_id, str) and native_thread_id != session_id:
                raise DaemonBridgeError("Codex native frame thread identity does not match session")
        with self._lock:
            handler = self._handlers.get(agent_id)
        if handler is None:
            raise DaemonBridgeError("Codex native frame agent is not registered")
        handler(dict(frame))
