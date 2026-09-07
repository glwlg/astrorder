from __future__ import annotations

import asyncio
from typing import Any


class EventHub:
    """In-process fan-out for already durable events."""

    def __init__(self, max_queue: int = 1024):
        self.max_queue = max_queue
        self._subscribers: set[asyncio.Queue[dict[str, Any]]] = set()

    def subscribe(self) -> asyncio.Queue[dict[str, Any]]:
        queue: asyncio.Queue[dict[str, Any]] = asyncio.Queue(maxsize=self.max_queue)
        self._subscribers.add(queue)
        return queue

    def unsubscribe(self, queue: asyncio.Queue[dict[str, Any]]) -> None:
        self._subscribers.discard(queue)

    def publish(self, event: dict[str, Any]) -> None:
        for queue in tuple(self._subscribers):
            try:
                queue.put_nowait(event)
            except asyncio.QueueFull:
                self._drain(queue)
                resync = {
                    "id": f"resync-live-{event.get('cursor', 0)}",
                    "cursor": event.get("cursor", 0),
                    "type": "resync_required",
                    "agent_id": None,
                    "session_id": None,
                    "data": {"reason": "live_queue_overflow"},
                }
                try:
                    queue.put_nowait(resync)
                except asyncio.QueueFull:
                    pass

    @staticmethod
    def _drain(queue: asyncio.Queue[dict[str, Any]]) -> None:
        while True:
            try:
                queue.get_nowait()
            except asyncio.QueueEmpty:
                return
