from __future__ import annotations

import json
import queue
import subprocess
import threading
from collections.abc import Callable
from typing import Any

from .config import CodexConnectorConfig
from .protocol import CodexAppServerProtocol


class CodexAppServer:
    """JSONL app-server client; stdout is parsed as the documented protocol, not scraped text."""

    def __init__(
        self,
        config: CodexConnectorConfig,
        on_notification: Callable[[dict[str, Any]], None],
        protocol: CodexAppServerProtocol | None = None,
    ):
        self.config = config
        self.on_notification = on_notification
        self.protocol = protocol or CodexAppServerProtocol()
        self.process: subprocess.Popen[str] | None = None
        self._reader: threading.Thread | None = None
        self._responses: dict[int, queue.Queue[dict[str, Any]]] = {}
        self._lock = threading.RLock()

    def start(self) -> None:
        if self.process is not None:
            return
        self.process = subprocess.Popen(
            [self.config.executable, "app-server", "--listen", "stdio://"],
            cwd=str(self.config.workspace),
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            shell=False,
        )
        self._reader = threading.Thread(target=self._read_loop, name="astrorder-codex-app-server", daemon=True)
        self._reader.start()

    def stop(self) -> None:
        process = self.process
        self.process = None
        if process is None:
            return
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=3)

    def send(self, message: dict[str, Any]) -> None:
        process = self.process
        if process is None or process.stdin is None or process.poll() is not None:
            raise RuntimeError("Codex app-server is not running")
        process.stdin.write(json.dumps(message, ensure_ascii=False) + "\n")
        process.stdin.flush()

    def request(self, method: str, params: dict[str, Any], timeout: float = 30) -> dict[str, Any]:
        message = self.protocol.request(method, params)
        request_id = int(message["id"])
        response_queue: queue.Queue[dict[str, Any]] = queue.Queue(maxsize=1)
        with self._lock:
            self._responses[request_id] = response_queue
        try:
            self.send(message)
            response = response_queue.get(timeout=timeout)
        finally:
            with self._lock:
                self._responses.pop(request_id, None)
        if "error" in response:
            raise RuntimeError("Codex app-server request failed")
        result = self.protocol.response_result(response)
        if result is None:
            raise RuntimeError("Codex app-server returned an invalid response")
        return result

    def _read_loop(self) -> None:
        process = self.process
        if process is None or process.stdout is None:
            return
        for line in process.stdout:
            try:
                message = json.loads(line)
            except json.JSONDecodeError:
                continue
            if not isinstance(message, dict):
                continue
            request_id = message.get("id")
            if isinstance(request_id, int):
                with self._lock:
                    response_queue = self._responses.get(request_id)
                if response_queue is not None:
                    response_queue.put(message)
                continue
            self.on_notification(message)
