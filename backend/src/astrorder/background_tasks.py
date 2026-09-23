from __future__ import annotations

import asyncio


class BackgroundTaskRegistry:
    def __init__(self) -> None:
        self._cancellations: dict[str, asyncio.Event] = {}

    def start(self, operation_id: str) -> asyncio.Event:
        if operation_id in self._cancellations:
            raise ValueError("background task ID is already active")
        event = asyncio.Event()
        self._cancellations[operation_id] = event
        return event

    def finish(self, operation_id: str, event: asyncio.Event) -> None:
        if self._cancellations.get(operation_id) is event:
            self._cancellations.pop(operation_id)

    def cancel(self, operation_id: str) -> bool:
        event = self._cancellations.get(operation_id)
        if event is None:
            return False
        event.set()
        return True

    def cancel_all(self) -> None:
        for event in self._cancellations.values():
            event.set()

    def is_active(self, operation_id: str) -> bool:
        return operation_id in self._cancellations
