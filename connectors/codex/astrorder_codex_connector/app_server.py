from __future__ import annotations

import json
import os
import queue
import re
import subprocess
import threading
from collections import deque
from collections.abc import Callable, Mapping
from typing import Any

from .config import CodexConnectorConfig
from .protocol import CodexAppServerProtocol


def _safe_error_detail(value: object) -> str:
    detail = str(value or '').replace('\x00', ' ').replace('\r', ' ').replace('\n', ' ').strip()
    detail = re.sub(
        r'(?i)(password|passwd|token|secret|authorization|api[_ -]?key)(\s*[:=]\s*)[^\s,;]+',
        r'\1\2[REDACTED]',
        detail,
    )
    return detail[:1000]


class CodexRpcRejected(RuntimeError):
    def __init__(self, error):
        code = error.get('code') if isinstance(error, dict) else None
        detail = _safe_error_detail(error.get('message') if isinstance(error, dict) else None)
        tags = [word for word in ('cwd', 'directory', 'not found', 'model', 'mcp', 'configuration', 'experimental', 'sandbox', 'permission', 'rollout', 'history', 'lease', 'owner', 'another', 'process', 'already', 'running', 'active', 'busy', 'thread', 'fork', 'resume', 'lock', 'attached', 'not supported', 'external', 'read-only') if word in detail.lower()]
        super().__init__(f'Codex rejected request ({code}; {detail or ", ".join(tags) or "native rejection"})')


class CodexAppServer:
    """JSONL app-server client; stdout is parsed as the documented protocol, not scraped text."""

    def __init__(
        self,
        config: CodexConnectorConfig,
        on_notification: Callable[[dict[str, Any]], None],
        protocol: CodexAppServerProtocol | None = None,
        on_close: Callable[[], None] | None = None,
        launch_argv: list[str] | None = None,
        environment: Mapping[str, str] | None = None,
        bootstrap_stdin: dict[str, Any] | None = None,
    ):
        self.config = config
        self.launch_argv = launch_argv
        self.environment = dict(environment) if environment is not None else None
        self.bootstrap_stdin = dict(bootstrap_stdin) if bootstrap_stdin is not None else None
        self.on_notification = on_notification
        self.protocol = protocol or CodexAppServerProtocol()
        self.on_close = on_close
        self.process: subprocess.Popen[str] | None = None
        self._reader: threading.Thread | None = None
        self._stderr_reader: threading.Thread | None = None
        self._stderr_tail: deque[str] = deque(maxlen=8)
        self._responses: dict[int, queue.Queue[dict[str, Any]]] = {}
        self._lock = threading.RLock()

    def start(self) -> None:
        if self.process is not None:
            return
        startupinfo = None
        if os.name == "nt":
            startupinfo = subprocess.STARTUPINFO()
            startupinfo.dwFlags |= subprocess.STARTF_USESHOWWINDOW
            startupinfo.wShowWindow = 0
        self.process = subprocess.Popen(
            self.launch_argv or [self.config.executable, "app-server", "--listen", "stdio://"],
            cwd=None if self.launch_argv else str(self.config.workspace),
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            encoding='utf-8',
            errors='replace',
            creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0),
            startupinfo=startupinfo,
            shell=False,
            env=self.environment,
        )
        self._stderr_reader = threading.Thread(target=self._drain_stderr, name="astrorder-codex-app-server-stderr", daemon=True)
        self._stderr_reader.start()
        if self.bootstrap_stdin is not None:
            if self.process.stdin is None:
                raise RuntimeError('Codex app-server stdin is unavailable for bootstrap')
            self.process.stdin.write(json.dumps(self.bootstrap_stdin, ensure_ascii=False) + "\n")
            self.process.stdin.flush()
        self._reader = threading.Thread(target=self._read_loop, name="astrorder-codex-app-server", daemon=True)
        self._reader.start()

    def _drain_stderr(self) -> None:
        process = self.process
        stream = getattr(process, 'stderr', None) if process is not None else None
        if stream is None:
            return
        for line in stream:
            detail = _safe_error_detail(line)
            if detail:
                with self._lock:
                    self._stderr_tail.append(detail)

    def _diagnostic_detail(self) -> str:
        with self._lock:
            tail = self._stderr_tail
            raw = tail if isinstance(tail, str) else '\n'.join(tail)
        return _safe_error_detail(raw)

    def _transport_closed_error(self) -> RuntimeError:
        detail = self._diagnostic_detail()
        return RuntimeError(
            f'Codex transport closed before response: {detail}'
            if detail
            else 'Codex transport closed before response'
        )

    def _request_timeout_error(self, method: str) -> RuntimeError:
        detail = self._diagnostic_detail()
        return RuntimeError(
            f'Codex app-server timed out waiting for {method}: {detail}'
            if detail
            else f'Codex app-server timed out waiting for {method}'
        )

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
        if self._stderr_reader and self._stderr_reader is not threading.current_thread():
            self._stderr_reader.join(timeout=2)

    def send(self, message: dict[str, Any]) -> None:
        process = self.process
        if process is None or process.stdin is None or process.poll() is not None:
            raise self._transport_closed_error()
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
        except queue.Empty:
            raise self._request_timeout_error(method) from None
        finally:
            with self._lock:
                self._responses.pop(request_id, None)
        if "error" in response:
            raise CodexRpcRejected(response['error'])
        result = self.protocol.response_result(response)
        if result is None:
            raise RuntimeError("Codex app-server returned an invalid response")
        return result

    def _notify(self, message: dict[str, Any]) -> None:
        try:
            self.on_notification(message)
        except Exception as exc:  # noqa: BLE001 - a bad native event must not kill the transport reader
            with self._lock:
                self._stderr_tail.append(_safe_error_detail(f'Codex notification handling failed: {exc}'))

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
                self._notify(message)
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
            self._notify(message)
        if self._stderr_reader and self._stderr_reader is not threading.current_thread():
            self._stderr_reader.join(timeout=0.5)
        with self._lock:
            waiting = list(self._responses.values())
        closed_error = self._transport_closed_error()
        for response_queue in waiting:
            try:
                response_queue.put_nowait(closed_error)
            except queue.Full:
                pass
        if self.process is process and self.on_close is not None:
            self.on_close()
