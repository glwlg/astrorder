from __future__ import annotations

import json

import pytest

from astrorder.daemon import codex_desktop
from astrorder.daemon.errors import DaemonProtocolError


@pytest.mark.asyncio
async def test_codex_desktop_cdp_selects_exact_task_and_submits_text_and_image(tmp_path, monkeypatch):
    state = tmp_path / "state.json"
    state.write_text(json.dumps({"port": 9335, "browserId": "browser-1"}), encoding="utf-8")
    target = {
        "type": "page",
        "url": "app://-/index.html",
        "webSocketDebuggerUrl": "ws://127.0.0.1:9335/devtools/page/page-1",
    }

    def fetch(_port, resource):
        if resource == "/json/version":
            return {"webSocketDebuggerUrl": "ws://127.0.0.1:9335/devtools/browser/browser-1"}
        return [target]

    calls = []

    class Socket:
        async def send(self, raw):
            request = json.loads(raw)
            calls.append(request)
            value = True if request["method"] == "Runtime.evaluate" else None
            self.response = json.dumps({"id": request["id"], "result": {"result": {"value": value}}})

        async def recv(self):
            return self.response

    class Connection:
        async def __aenter__(self):
            return Socket()

        async def __aexit__(self, *_args):
            return None

    monkeypatch.setattr(codex_desktop, "_fetch_json", fetch)
    monkeypatch.setattr(codex_desktop.websockets, "connect", lambda *_args, **_kwargs: Connection())

    result = await codex_desktop.send_codex_desktop_message(
        "thread-1",
        [
            {"type": "text", "text": "来自星序"},
            {"type": "image", "url": "data:image/png;base64,aW1hZ2U="},
            {"type": "mention", "name": "report.pdf", "path": "P:/repo/.astrorder/report.pdf"},
        ],
        state_path=state,
    )

    assert result == {"accepted": True, "transport": "codex-desktop-cdp"}
    assert any(
        call["method"] == "Input.insertText" and call["params"] == {"text": "[report.pdf](<P:/repo/.astrorder/report.pdf>)\n来自星序"}
        for call in calls
    )
    assert [
        call["params"]["type"]
        for call in calls
        if call["method"] == "Input.dispatchKeyEvent"
    ] == ["rawKeyDown", "keyUp"]
    assert any("thread-1" in call["params"].get("expression", "") for call in calls)
    assert any(
        "ClipboardEvent('paste'" in call["params"].get("expression", "")
        and "data:image/png;base64,aW1hZ2U=" in call["params"].get("expression", "")
        for call in calls
    )


@pytest.mark.asyncio
async def test_codex_desktop_cdp_updates_reasoning_with_native_key_events(tmp_path, monkeypatch):
    state = tmp_path / "state.json"
    state.write_text(json.dumps({"port": 9335, "browserId": "browser-1"}), encoding="utf-8")
    target = {
        "type": "page",
        "url": "app://-/index.html",
        "webSocketDebuggerUrl": "ws://127.0.0.1:9335/devtools/page/page-1",
    }

    monkeypatch.setattr(
        codex_desktop,
        "_fetch_json",
        lambda _port, resource: (
            {"webSocketDebuggerUrl": "ws://127.0.0.1:9335/devtools/browser/browser-1"}
            if resource == "/json/version"
            else [target]
        ),
    )
    calls = []

    class Socket:
        async def send(self, raw):
            request = json.loads(raw)
            calls.append(request)
            expression = request.get("params", {}).get("expression", "")
            value = {"x": 120, "y": 40} if "getBoundingClientRect" in expression else True
            self.response = json.dumps(
                {"id": request["id"], "result": {"result": {"value": value}}}
            )

        async def recv(self):
            return self.response

    class Connection:
        async def __aenter__(self):
            return Socket()

        async def __aexit__(self, *_args):
            return None

    monkeypatch.setattr(codex_desktop.websockets, "connect", lambda *_args, **_kwargs: Connection())

    result = await codex_desktop.set_codex_desktop_settings(
        "thread-1", {"effort": "high", "effortIndex": 2}, state_path=state
    )

    assert result["accepted"] is True
    mouse = [call for call in calls if call["method"] == "Input.dispatchMouseEvent"]
    assert [call["params"]["type"] for call in mouse] == ["mousePressed", "mouseReleased"]
    assert all(call["params"]["x"] == 120 and call["params"]["y"] == 40 for call in mouse)


@pytest.mark.asyncio
async def test_codex_desktop_cdp_removes_stale_state(tmp_path, monkeypatch):
    state = tmp_path / "state.json"
    state.write_text(json.dumps({"port": 9335, "browserId": "browser-1"}), encoding="utf-8")
    monkeypatch.setattr(
        codex_desktop,
        "_fetch_json",
        lambda *_args: (_ for _ in ()).throw(
            DaemonProtocolError("Codex Desktop debugging endpoint is unavailable")
        ),
    )

    with pytest.raises(DaemonProtocolError, match="endpoint is unavailable"):
        await codex_desktop.send_codex_desktop_message("thread-1", "hello", state_path=state)

    assert not state.exists()
def test_codex_desktop_status_reports_available_endpoint(tmp_path, monkeypatch):
    state = tmp_path / "state.json"
    state.write_text('{"port":9335,"browserId":"browser-1"}', encoding="utf-8")
    monkeypatch.setattr(
        codex_desktop,
        "_fetch_json",
        lambda port, resource: {
            "webSocketDebuggerUrl": "ws://127.0.0.1:9335/devtools/browser/browser-1"
        },
    )

    assert codex_desktop.codex_desktop_status(state_path=state) == {
        "available": True,
        "port": 9335,
    }


def test_codex_desktop_status_removes_stale_endpoint(tmp_path, monkeypatch):
    state = tmp_path / "state.json"
    state.write_text('{"port":9335,"browserId":"browser-1"}', encoding="utf-8")
    monkeypatch.setattr(
        codex_desktop,
        "_fetch_json",
        lambda *_args: (_ for _ in ()).throw(
            codex_desktop.DaemonProtocolError("Codex Desktop debugging endpoint is unavailable")
        ),
    )

    assert codex_desktop.codex_desktop_status(state_path=state)["available"] is False
    assert not state.exists()
