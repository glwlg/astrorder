from __future__ import annotations

from dataclasses import dataclass
from typing import Any


@dataclass(frozen=True)
class CodexNotification:
    method: str
    params: dict[str, Any]


class CodexAppServerProtocol:
    """Small, version-neutral JSON-RPC message builder for the documented app-server API."""

    def __init__(self) -> None:
        self._next_id = 1

    def request(self, method: str, params: dict[str, Any]) -> dict[str, Any]:
        request_id = self._next_id
        self._next_id += 1
        return {"method": method, "id": request_id, "params": params}

    def initialize(self) -> dict[str, Any]:
        return self.request(
            "initialize",
            {
                "clientInfo": {
                    "name": "astrorder_codex_connector",
                    "title": "Astrorder Codex Connector",
                    "version": "0.1.0",
                }
            },
        )

    @staticmethod
    def initialized() -> dict[str, Any]:
        return {"method": "initialized", "params": {}}

    def start_thread(self, cwd: str) -> dict[str, Any]:
        return self.request("thread/start", {"cwd": cwd})

    def start_turn(self, thread_id: str, text: str, cwd: str) -> dict[str, Any]:
        return self.request(
            "turn/start",
            {
                "threadId": thread_id,
                "input": [{"type": "text", "text": text}],
                "cwd": cwd,
            },
        )

    def interrupt_turn(self, thread_id: str, turn_id: str) -> dict[str, Any]:
        return self.request(
            "turn/interrupt", {"threadId": thread_id, "turnId": turn_id}
        )

    @staticmethod
    def notification(message: dict[str, Any]) -> CodexNotification | None:
        method = message.get("method")
        params = message.get("params")
        if not isinstance(method, str) or not isinstance(params, dict) or "id" in message:
            return None
        return CodexNotification(method, params)

    @staticmethod
    def response_result(message: dict[str, Any]) -> dict[str, Any] | None:
        result = message.get("result")
        return result if isinstance(result, dict) else None
