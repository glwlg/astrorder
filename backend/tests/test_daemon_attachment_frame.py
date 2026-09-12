import pytest
import websockets

from astrorder.daemon.session_daemon import SessionDaemon


@pytest.mark.asyncio
async def test_daemon_websocket_accepts_default_max_attachment_base64_frame(monkeypatch):
    captured = {}

    async def fake_serve(handler, host, port, **kwargs):
        del handler, host, port
        captured.update(kwargs)
        return object()

    monkeypatch.setattr(websockets, "serve", fake_serve)
    daemon = SessionDaemon(secret="test-only-daemon-secret")

    await daemon.serve("127.0.0.1", 30109)

    assert captured["max_size"] >= 14_000_000
