from __future__ import annotations

import json
import queue
import subprocess
import threading
from collections.abc import Callable
from typing import Any

from .config import CodexConnectorConfig
from .protocol import CodexAppServerProtocol


class CodexRpcRejected(RuntimeError):
    def __init__(self, error):
        code = error.get('code') if isinstance(error, dict) else None
        message = str(error.get('message') or '').lower() if isinstance(error, dict) else ''
        tags = [word for word in ('cwd', 'directory', 'not found', 'model', 'mcp', 'configuration', 'experimental', 'sandbox', 'permission', 'rollout', 'history', 'lease', 'owner', 'another', 'process', 'already', 'running', 'active', 'busy', 'thread', 'fork', 'resume', 'lock', 'attached', 'not supported', 'external', 'read-only') if word in message]
        super().__init__(f'Codex rejected request ({code}; {", ".join(tags) or "native rejection"})')


class CodexAppServer:
    """JSONL app-server client; stdout is parsed as the documented protocol, not scraped text."""

    def __init__(
        self,
        config: CodexConnectorConfig,
        on_notification: Callable[[dict[str, Any]], None],
        protocol: CodexAppServerProtocol | None = None,
        on_close: Callable[[], None] | None = None,
        launch_argv: list[str] | None = None,
    ):
        self.config = config
        self.launch_argv = launch_argv
        self.on_notification = on_notification
        self.protocol = protocol or CodexAppServerProtocol()
        self.on_close = on_close
        self.process: subprocess.Popen[str] | None = None
        self._reader: threading.Thread | None = None
        self._responses: dict[int, queue.Queue[dict[str, Any]]] = {}
        self._lock = threading.RLock()

    def start(self) -> None:
        if self.process is not None:
            return
        self.process = subprocess.Popen(
            self.launch_argv or [self.config.executable, "app-server", "--listen", "stdio://"],
            cwd=str(self.config.workspace),
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            encoding='utf-8',
            errors='replace',
            creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0),
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
            if process.stdin:
                try:
                    process.stdin.close()
                except OSError:
                    pass
            try:
                process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                process.terminate()
                try:
                    process.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=3)
        if self._reader and self._reader is not threading.current_thread():
            self._reader.join(timeout=2)

    def send(self, message: dict[str, Any]) -> None:
        process = self.process
        if process is None or process.stdin is None or process.poll() is not None:
            raise RuntimeError("Codex app-server is not running")
        with self._lock:
            process.stdin.write(json.dumps(message, ensure_ascii=False) + "\n")
            process.stdin.flush()

    def request(self, method: str, params: dict[str, Any], timeout: float = 30) -> dict[str, Any]:
        response_queue: queue.Queue[dict[str, Any]] = queue.Queue(maxsize=1)
        with self._lock:
            message = self.protocol.request(method, params)
            request_id = int(message["id"])
            self._responses[request_id] = response_queue
        try:
            self.send(message)
            response = response_queue.get(timeout=timeout)
            if isinstance(response, Exception):
                raise response
        finally:
            with self._lock:
                self._responses.pop(request_id, None)
        if "error" in response:
            raise CodexRpcRejected(response['error'])
        result = self.protocol.response_result(response)
        if result is None:
            raise RuntimeError("Codex app-server returned an invalid response")
        return result

    def _read_loop(self) -> None:
        process = self.process
        if process is None or process.stdout is None:
            return
        for line in process.stdout:
            if self.process is not process:
                break
            try:
                message = json.loads(line)
            except json.JSONDecodeError:
                continue
            if not isinstance(message, dict):
                continue
            if isinstance(message.get('method'), str):
                self.on_notification(message)
                continue
            request_id = message.get("id")
            if isinstance(request_id, int):
                with self._lock:
                    response_queue = self._responses.get(request_id)
                if response_queue is not None:
                    try:
                        response_queue.put_nowait(message)
                    except queue.Full:
                        pass
                continue
            self.on_notification(message)
        with self._lock:
            waiting = list(self._responses.values())
        for response_queue in waiting:
            try:
                response_queue.put_nowait(RuntimeError('Codex transport closed before response'))
            except queue.Full:
                pass
        if self.process is process and self.on_close is not None:
            self.on_close()
