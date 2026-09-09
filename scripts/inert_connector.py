"""Inert connector used only for isolated Astrorder browser integration tests.

It speaks the public connector protocol, never invokes Hermes or Codex, and does
not read credentials from files. Test credentials are supplied explicitly by the
owned test process environment.
"""
from __future__ import annotations

import asyncio
import json
import os
from datetime import UTC, datetime
from typing import Any

import websockets

AGENT_ID = "inert-browser-fixture"
SESSION_ID = "inert-browser-session"


def timestamp() -> str:
    return datetime.now(UTC).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def event(event_id: str, event_type: str, data: dict[str, Any], session_id: str | None) -> dict[str, Any]:
    return {
        "id": event_id,
        "cursor": 0,
        "type": event_type,
        "agent_id": AGENT_ID,
        "session_id": session_id,
        "data": data,
    }


def session(status: str = "idle") -> dict[str, Any]:
    return {
        "id": SESSION_ID,
        "agent_id": AGENT_ID,
        "title": "隔离联调会话",
        "workspace": "P:/workspace/isolated",
        "status": status,
        "updated_at": timestamp(),
    }


def message(
    message_id: str,
    text: str,
    *,
    role: str = "assistant",
    attachments: list[dict[str, Any]] | None = None,
    command_id: str | None = None,
) -> dict[str, Any]:
    return {
        "id": message_id,
        "session_id": SESSION_ID,
        "agent_id": AGENT_ID,
        "role": role,
        "kind": "message",
        "text": text,
        "attachments": attachments or [],
        "created_at": timestamp(),
        "command_id": command_id,
        "tool": None,
    }


async def send(socket, payload: dict[str, Any]) -> None:
    await socket.send(json.dumps(payload, ensure_ascii=False))


async def send_event(
    socket,
    event_id: str,
    event_type: str,
    data: dict[str, Any],
    session_id: str | None = SESSION_ID,
) -> None:
    await send(socket, {"type": "event", "event": event(event_id, event_type, data, session_id)})


async def connect(endpoint: str, secret: str):
    headers = {"Authorization": f"Bearer {secret}"}
    try:
        return await websockets.connect(endpoint, additional_headers=headers)
    except TypeError:
        return await websockets.connect(endpoint, extra_headers=headers)


async def publish_initial_state(socket) -> None:
    await send_event(socket, "inert-session-initial", "session.upsert", session())
    for index in range(60):
        await send_event(
            socket,
            f"inert-initial-message-{index}",
            "message.upsert",
            message(f"inert-initial-message-{index}", f"隔离历史记录 {index + 1}"),
        )


async def handle_command(socket, command: dict[str, Any]) -> bool:
    command_id = str(command["id"])
    text = str(command.get("text", ""))
    if text.startswith("disconnect-once"):
        update = dict(command, state="accepted", error=None)
        await send_event(socket, f"inert-command-{command_id}-accepted", "command.upsert", update)
        return True
    if text.startswith("fixture-fail"):
        update = dict(command, state="failed", error="Inert connector intentionally rejected this command")
        await send_event(socket, f"inert-command-{command_id}-failed", "command.upsert", update)
        return False

    await send_event(socket, f"inert-session-running-{command_id}", "session.upsert", session("running"))
    await send_event(
        socket,
        f"inert-command-{command_id}-accepted",
        "command.upsert",
        dict(command, state="accepted", error=None),
    )
    attachments = command.get("attachments")
    if not isinstance(attachments, list):
        attachments = []
    await send_event(
        socket,
        f"inert-user-{command_id}",
        "message.upsert",
        message(
            f"inert-user-{command_id}",
            text,
            role="user",
            attachments=attachments,
            command_id=command_id,
        ),
    )
    reply = f"回显：{text}" if text else "回显：图片/附件"
    await send_event(
        socket,
        f"inert-assistant-{command_id}",
        "message.upsert",
        message(f"inert-assistant-{command_id}", reply),
    )
    await send_event(
        socket,
        f"inert-command-{command_id}-completed",
        "command.upsert",
        dict(command, state="completed", error=None),
    )
    await send_event(socket, f"inert-session-idle-{command_id}", "session.upsert", session())
    return False


async def run() -> None:
    endpoint = os.environ.get("ASTRORDER_CONNECTOR_ENDPOINT", "ws://127.0.0.1:30002/ws/v1/connector")
    secret = os.environ.get("ASTRORDER_CONNECTOR_SECRET")
    if not secret:
        raise RuntimeError("ASTRORDER_CONNECTOR_SECRET is required for the inert fixture")

    published_initial = False
    connection_count = 0
    while True:
        try:
            async with await connect(endpoint, secret) as socket:
                connection_count += 1
                agent = {
                    "id": AGENT_ID,
                    "kind": "hermes",
                    "name": "Inert integration fixture",
                    "status": "ready",
                    "capabilities": ["chat", "queue", "attachments", "events"],
                    "limitation": "Inert protocol fixture only; this is not a Hermes or Codex runtime.",
                }
                await send(socket, {"type": "hello", "protocol_version": 1, "agent": agent})
                if not published_initial:
                    await publish_initial_state(socket)
                    published_initial = True
                else:
                    await send_event(
                        socket,
                        f"inert-session-reconnect-{connection_count}",
                        "session.upsert",
                        session(),
                    )
                while True:
                    frame = json.loads(await socket.recv())
                    if frame.get("type") != "command" or not isinstance(frame.get("command"), dict):
                        continue
                    if await handle_command(socket, frame["command"]):
                        await socket.close()
                        break
        except asyncio.CancelledError:
            raise
        except (OSError, json.JSONDecodeError, websockets.WebSocketException):
            await asyncio.sleep(0.2)
            continue
        await asyncio.sleep(0.2)


if __name__ == "__main__":
    asyncio.run(run())
