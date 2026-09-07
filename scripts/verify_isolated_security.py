"""Exercise security gates against an explicitly isolated Astrorder server.

This script is for a disposable local server only. It uses only supplied test
credentials and writes no credentials to its result artifact.
"""
from __future__ import annotations

import asyncio
import json
import os
from pathlib import Path
from urllib.parse import urlparse

import httpx
import websockets


def required(name: str) -> str:
    value = os.environ.get(name)
    if not value:
        raise RuntimeError(f"{name} is required")
    return value


async def open_socket(url: str, headers: dict[str, str], origin: str | None = None):
    kwargs = {"additional_headers": headers}
    if origin is not None:
        kwargs["origin"] = origin
    try:
        return await websockets.connect(url, **kwargs)
    except TypeError:
        kwargs["extra_headers"] = kwargs.pop("additional_headers")
        return await websockets.connect(url, **kwargs)


async def assert_rejected(url: str, headers: dict[str, str], origin: str | None = None) -> None:
    try:
        socket = await open_socket(url, headers, origin)
    except websockets.InvalidHandshake:
        return
    async with socket:
        try:
            await asyncio.wait_for(socket.recv(), timeout=2)
        except websockets.ConnectionClosed as exc:
            if exc.code == 4401:
                return
            raise AssertionError(f"websocket closed with unexpected code {exc.code}") from exc
    raise AssertionError("websocket was accepted without the required role")


async def main() -> None:
    base_url = required("ASTRORDER_TEST_BASE_URL").rstrip("/")
    browser_secret = required("ASTRORDER_BROWSER_SECRET")
    connector_secret = required("ASTRORDER_CONNECTOR_SECRET")
    origin = base_url
    parsed = urlparse(base_url)
    websocket_base = f"{'wss' if parsed.scheme == 'https' else 'ws'}://{parsed.netloc}"

    with httpx.Client(base_url=base_url, follow_redirects=True, timeout=5) as client:
        health = client.get("/health")
        assert health.status_code == 200 and health.json() == {
            "status": "ok",
            "service": "astrorder",
            "protocol_version": 1,
        }
        assert client.get("/api/v1/bootstrap").status_code == 401
        assert client.post(
            "/api/v1/auth/session",
            json={"token": browser_secret},
            headers={"Origin": "https://evil.example"},
        ).status_code == 403
        assert client.post(
            "/api/v1/auth/session",
            json={"token": browser_secret},
            headers={"Origin": origin},
        ).status_code == 200
        assert client.post(
            "/api/v1/attachments",
            files={"file": ("../outside.txt", b"must-not-be-read", "text/plain")},
            headers={"Origin": origin},
        ).status_code == 400
        assert client.get("/api/v1/not-a-route").status_code == 404
        assert "<div id=\"root\"></div>" in client.get("/chat").text
        launch = client.post(
            "/api/v1/runtime/launch",
            json={"kind": "codex", "workspace": "P:/workspace/isolated;not-a-command"},
            headers={"Origin": origin},
        )
        assert launch.status_code == 409

    await assert_rejected(f"{websocket_base}/ws/v1/events?after=0", {})
    await assert_rejected(
        f"{websocket_base}/ws/v1/events?after=0",
        {"Authorization": f"Bearer {connector_secret}"},
    )
    await assert_rejected(
        f"{websocket_base}/ws/v1/connector",
        {"Authorization": f"Bearer {browser_secret}"},
    )
    await assert_rejected(
        f"{websocket_base}/ws/v1/connector",
        {"Authorization": f"Bearer {connector_secret}"},
        "https://evil.example",
    )

    evidence_path = Path(required("ASTRORDER_EVIDENCE_PATH"))
    evidence_path.parent.mkdir(parents=True, exist_ok=True)
    evidence_path.write_text(
        json.dumps(
            {
                "health": "public-versioned",
                "private_http": "authenticated-and-origin-checked",
                "spa": "client-routes-served-with-api-404-preserved",
                "attachments": "traversal-rejected",
                "runtime": "shell-shaped-workspace-rejected",
                "websockets": "unauthenticated-and-cross-role-connections-rejected",
            },
            ensure_ascii=False,
            indent=2,
        ),
        encoding="utf-8",
    )
    print("isolated security gates passed")


if __name__ == "__main__":
    asyncio.run(main())
