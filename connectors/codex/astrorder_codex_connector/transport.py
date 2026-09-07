from __future__ import annotations

import asyncio
import json
import queue
import threading
from collections.abc import Callable
from typing import Any


class CodexTransport:
    """Connector WebSocket transport. Commands are never resent after a disconnect."""

    def __init__(self, endpoint: str, secret: str, on_command: Callable[[dict[str, Any]], None]):
        self.endpoint = endpoint
        self.secret = secret
        self.on_command = on_command
        self._outgoing: queue.Queue[dict[str, Any] | None] = queue.Queue()
        self._thread: threading.Thread | None = None
        self._stop = threading.Event()
        self._agent: dict[str, Any] | None = None

    def start(self, agent: dict[str, Any]) -> None:
        if self._thread is not None:
            return
        self._agent = dict(agent)
        self._thread = threading.Thread(target=self._run, name="astrorder-codex-connector", daemon=True)
        self._thread.start()

    def send_event(self, event: dict[str, Any]) -> None:
        if self._thread is not None and not self._stop.is_set():
            self._outgoing.put({"type": "event", "event": dict(event)})

    def stop(self) -> None:
        self._stop.set()
        self._outgoing.put(None)
        if self._thread is not None and self._thread is not threading.current_thread():
            self._thread.join(timeout=2)
        self._thread = None

    def _run(self) -> None:
        try:
            asyncio.run(self._session())
        except Exception:  # noqa: BLE001 - transport thread must fail closed on connection errors
            return

    async def _connect(self):
        import websockets

        headers = {"Authorization": f"Bearer {self.secret}"}
        try:
            return await websockets.connect(self.endpoint, additional_headers=headers)
        except TypeError:
            return await websockets.connect(self.endpoint, extra_headers=headers)

    async def _session(self) -> None:
        if self._agent is None:
            return
        async with await self._connect() as socket:
            await socket.send(
                json.dumps(
                    {
                        "type": "hello",
                        "protocol_version": 1,
                        "agent": self._agent,
                    },
                    ensure_ascii=False,
                )
            )
            sender = asyncio.create_task(self._send_outgoing(socket))
            try:
                async for value in socket:
                    frame = json.loads(value)
                    if frame.get("type") == "command" and isinstance(frame.get("command"), dict):
                        self.on_command(frame["command"])
            finally:
                self._outgoing.put(None)
                await asyncio.gather(sender, return_exceptions=True)

    async def _send_outgoing(self, socket) -> None:
        while True:
            value = await asyncio.to_thread(self._outgoing.get)
            if value is None:
                await socket.close()
                return
            if not self._stop.is_set():
                await socket.send(json.dumps(value, ensure_ascii=False))
