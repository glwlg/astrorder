"""Browser terminal relay for explicitly daemon-owned local PTYs."""
from __future__ import annotations

import asyncio
import hashlib
import json
from collections.abc import Callable, Mapping
from typing import Any
from uuid import uuid4

import websockets
from starlette.websockets import WebSocketDisconnect

from astrorder.daemon.bridge import DaemonBridge, DaemonBridgeError


def daemon_pty_target(session: Mapping[str, Any]) -> tuple[str, str, str] | None:
    """Return an exact local terminal target; SSH sessions never use local PTY."""
    agent_id = session.get("agent_id")
    session_id = session.get("id")
    workspace = session.get("workspace")
    connection_id = session.get("connection_id")
    if connection_id not in {None, "", "local"}:
        return None
    if (
        not isinstance(agent_id, str)
        or not agent_id
        or not isinstance(session_id, str)
        or not session_id
        or not isinstance(workspace, str)
        or not workspace
    ):
        return None
    return agent_id, session_id, workspace


def terminal_runtime_id(agent_id: str, session_id: str) -> str:
    """Derive a stable opaque PTY identity only from exact native identities."""
    if (
        not isinstance(agent_id, str)
        or not agent_id
        or len(agent_id) > 256
        or not isinstance(session_id, str)
        or not session_id
        or len(session_id) > 256
    ):
        raise ValueError("terminal runtime identity inputs are invalid")
    digest = hashlib.sha256(f"{agent_id}\0{session_id}".encode()).hexdigest()
    return f"pty-{digest[:48]}"


class DaemonTerminalRelay:
    """Connect one browser terminal to a daemon-owned local PTY session.

    The App Server is never the PTY parent.  Each browser attachment opens an
    independent daemon replay socket from checkpoint zero, so an App restart
    can reconnect to retained terminal output without sharing bridge control
    sockets or exposing the daemon secret to the browser.
    """

    def __init__(
        self,
        bridge: DaemonBridge,
        *,
        secret: str,
        request_timeout: float = 3.0,
        connect: Callable[..., Any] = websockets.connect,
    ) -> None:
        if not isinstance(secret, str) or not secret:
            raise ValueError("daemon terminal relay requires a daemon secret")
        if request_timeout <= 0:
            raise ValueError("request_timeout must be positive")
        self.bridge = bridge
        self.secret = secret
        self.request_timeout = request_timeout
        self._connect = connect

    async def serve(
        self,
        browser_socket: Any,
        *,
        agent_id: str,
        session_id: str,
        workspace: str,
    ) -> None:
        runtime_id = terminal_runtime_id(agent_id, session_id)
        await browser_socket.accept()
        try:
            await self.bridge.request_control(
                "session.spawn",
                {
                    "session_id": runtime_id,
                    "agent_type": "pty",
                    "cwd": workspace,
                    "params": {},
                },
            )
        except (DaemonBridgeError, ValueError):
            await self._send_error(browser_socket, "守护进程终端未确认启动；未创建本地 PTY。")
            await browser_socket.close()
            return

        stream_task = asyncio.create_task(self._stream_output(browser_socket, runtime_id))
        try:
            while True:
                message = await browser_socket.receive_text()
                if self._is_explicit_close(message):
                    await self.bridge.request_control(
                        "session.close", {"session_id": runtime_id}
                    )
                    break
                await self._forward_input(runtime_id, message)
        except (WebSocketDisconnect, RuntimeError):
            pass
        finally:
            stream_task.cancel()
            await asyncio.gather(stream_task, return_exceptions=True)

    @staticmethod
    def _is_explicit_close(message: str) -> bool:
        if not message.startswith("{"):
            return False
        try:
            payload = json.loads(message)
        except json.JSONDecodeError:
            return False
        return isinstance(payload, Mapping) and payload.get("type") == "close"

    async def _forward_input(self, runtime_id: str, message: str) -> None:
        try:
            resize = json.loads(message) if message.startswith("{") else None
        except json.JSONDecodeError:
            resize = None
        if isinstance(resize, Mapping) and resize.get("type") == "resize":
            await self.bridge.request_control(
                "session.resize",
                {
                    "session_id": runtime_id,
                    "cols": resize.get("cols"),
                    "rows": resize.get("rows"),
                },
            )
            return
        await self.bridge.request_control(
            "session.send", {"session_id": runtime_id, "input": message}
        )

    async def _stream_output(self, browser_socket: Any, runtime_id: str) -> None:
        try:
            async with self._connect(
                self.bridge.endpoint,
                open_timeout=self.request_timeout,
                close_timeout=self.request_timeout,
                max_size=2_000_000,
            ) as daemon_socket:
                await self._request(daemon_socket, "daemon.handshake", {"secret": self.secret})
                sync = await self._request(
                    daemon_socket, "session.sync", {"sessions": {runtime_id: 0}}
                )
                sessions = sync.get("sessions")
                response = sessions.get(runtime_id) if isinstance(sessions, Mapping) else None
                if not isinstance(response, Mapping):
                    raise DaemonBridgeError("daemon terminal sync response is invalid")
                if response.get("overflow") is True:
                    await self._send_error(
                        browser_socket,
                        "守护进程终端历史已溢出；为避免显示不完整输出，未连接 live tail。",
                    )
                    return
                frames = response.get("frames")
                if not isinstance(frames, list):
                    raise DaemonBridgeError("daemon terminal replay frames are invalid")
                for frame in frames:
                    await self._send_output(browser_socket, runtime_id, frame)
                while True:
                    raw = await daemon_socket.recv()
                    if not isinstance(raw, str):
                        raise DaemonBridgeError("daemon terminal live frame is invalid")
                    try:
                        frame = json.loads(raw)
                    except json.JSONDecodeError as exc:
                        raise DaemonBridgeError("daemon terminal live frame is invalid") from exc
                    await self._send_output(browser_socket, runtime_id, frame)
        except (DaemonBridgeError, OSError, TimeoutError, websockets.WebSocketException):
            await self._send_error(browser_socket, "守护进程终端连接已断开。")

    async def _request(
        self, daemon_socket: Any, action: str, fields: Mapping[str, Any]
    ) -> dict[str, Any]:
        request_id = f"daemon-terminal-{uuid4().hex}"
        await daemon_socket.send(
            json.dumps({"action": action, "request_id": request_id, **fields}, separators=(",", ":"))
        )
        raw = await asyncio.wait_for(daemon_socket.recv(), timeout=self.request_timeout)
        if not isinstance(raw, str):
            raise DaemonBridgeError("daemon terminal response is invalid")
        try:
            response = json.loads(raw)
        except json.JSONDecodeError as exc:
            raise DaemonBridgeError("daemon terminal response is invalid") from exc
        if not isinstance(response, dict) or response.get("request_id") != request_id:
            raise DaemonBridgeError("daemon terminal response does not match request")
        if response.get("action") == "error":
            raise DaemonBridgeError("daemon terminal request was rejected")
        if response.get("action") != f"{action}.result":
            raise DaemonBridgeError("daemon terminal response action is invalid")
        return response

    async def _send_output(self, browser_socket: Any, runtime_id: str, frame: Any) -> None:
        if not isinstance(frame, Mapping) or frame.get("session_id") != runtime_id:
            return
        if frame.get("event") != "pty.output":
            return
        payload = frame.get("payload")
        data = payload.get("data") if isinstance(payload, Mapping) else None
        if isinstance(data, str):
            await browser_socket.send_text(data)

    @staticmethod
    async def _send_error(browser_socket: Any, detail: str) -> None:
        await browser_socket.send_text(f"\r\n\x1b[31m{detail}\x1b[0m\r\n")
