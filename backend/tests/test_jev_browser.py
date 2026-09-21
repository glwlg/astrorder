import pytest

from astrorder import jev_browser
from astrorder.agent_gateway import CAPABILITIES, AgentContext, invoke
from astrorder.jev_browser import _action_space, _loading_shell, _text_for_action, run_browser_task


def test_browser_capability_and_dynamic_action_space():
    assert any(item["id"] == "browser.run" for item in CAPABILITIES)
    elements, targets, controls = _action_space(
        [
            {"id": "e1", "node": 1, "kind": "fill", "role": "textbox", "label": "Where from?", "value": ""},
            {"id": "e2", "node": 1, "kind": "click", "role": "textbox", "label": "Open Where from?", "value": ""},
            {"id": "wait", "kind": "wait", "label": "Wait"},
        ]
    )
    assert elements[0]["operations"] == ["TYPE_TEXT", "CLICK"]
    assert targets["TYPE_TEXT"]["1"]["id"] == "e1"
    assert controls["WAIT"]["kind"] == "wait"


def test_browser_text_values_match_visible_field_labels():
    action = {"label": "Where from?"}
    assert _text_for_action(action, {"Where from": "Zurich", "Where to": "London"}) == "Zurich"
    with pytest.raises(ValueError, match="没有匹配输入值"):
        _text_for_action(action, {"Departure": "2026-09-20", "Destination": "London"})


def test_browser_waits_for_a_sparse_loading_shell():
    assert _loading_shell({"text": "移除广告", "actions": [{"kind": "wait"}]}) is True
    assert _loading_shell({"text": "完成", "actions": [{"kind": "click"}]}) is False


def test_browser_task_rejects_non_web_urls_before_launching_browser():
    with pytest.raises(ValueError, match="http"):
        run_browser_task(url="file:///etc/passwd", goal="read it", inputs=None, store=None)


def test_browser_task_executes_until_jev_reports_done(monkeypatch):
    closed = []

    class FakeBrowser:
        def __init__(self, _url, *, reset=False):
            self.done = False
            self.target = "browser-1"
            self.resumed = not reset

        def observe(self):
            return {
                "url": "https://example.test/result" if self.done else "https://example.test",
                "title": "Result" if self.done else "Start",
                "text": "Target reached" if self.done else "Open target",
                "fingerprint": "done" if self.done else "start",
                "actions": [] if self.done else [
                    {"id": "e1", "node": 1, "kind": "click", "role": "link", "label": "Open target"}
                ],
            }

        def act(self, _action, _page, _text=None):
            self.done = True

        def close(self):
            closed.append(True)

    def choose(*, state, **_kwargs):
        if "goal" in state:
            return {"answers": {"goal_satisfied": {"choice": "SATISFIED"}}}
        if state["page"]["title"] == "Result":
            return {"answers": {"operation": {"choice": "DONE"}}}
        return {
            "answers": {
                "operation": {"choice": "CLICK"},
                "click_target": {"choice": "1"},
            }
        }

    monkeypatch.setattr(jev_browser, "Browser", FakeBrowser)
    monkeypatch.setattr(jev_browser, "invoke_jev", choose)
    result = run_browser_task(
        url="https://example.test",
        goal="Open target",
        inputs=None,
        store=None,
        max_steps=2,
    )
    assert result["ok"] is True
    assert result["title"] == "Result"
    assert result["browser_session"] == "browser-1"
    assert result["steps"][0]["action"] == "Open target"
    assert closed == []


def test_browser_capability_does_not_replay_urls_into_sidecar(monkeypatch):
    class Service:
        def __init__(self):
            self.events = []

        def _server_event(self, event_type, **kwargs):
            self.events.append((event_type, kwargs))

    service = Service()
    monkeypatch.setattr(
        jev_browser,
        "run_browser_task",
        lambda **_kwargs: {"ok": True, "url": "https://example.test/results"},
    )

    result = invoke(
        "browser.run",
        {
            "url": "https://example.test",
            "goal": "Show results",
            "caller_agent_id": "local-codex",
        },
        AgentContext(store=object(), service=service),
    )

    assert result["ok"] is True
    assert service.events == []


def test_browser_uses_a_real_visible_edge_page(monkeypatch):
    calls = []

    class FakeTransport:
        def __init__(self, websocket_url):
            assert websocket_url == "ws://visible"

        def call(self, method, **params):
            calls.append((method, params))
            if method == "Runtime.evaluate":
                return {"result": {"value": "complete"}}
            return {}

        def close(self):
            pass

    monkeypatch.setattr(
        jev_browser,
        "_launch_visible_page",
        lambda **_kwargs: ({
            "id": "visible-target",
            "url": "about:blank",
            "webSocketDebuggerUrl": "ws://visible",
        }, True),
    )
    monkeypatch.setattr(jev_browser, "_PageCDP", FakeTransport)

    browser = jev_browser.Browser("https://example.test")

    assert ("Page.navigate", {"url": "https://example.test"}) in calls
    assert browser.target == "visible-target"


def test_browser_retry_continues_same_site_without_navigating(monkeypatch):
    calls = []

    class FakeTransport:
        def __init__(self, _websocket_url):
            pass

        def call(self, method, **params):
            calls.append((method, params))
            if method == "Runtime.evaluate":
                return {"result": {"value": "complete"}}
            return {}

        def close(self):
            pass

    monkeypatch.setattr(
        jev_browser,
        "_launch_visible_page",
        lambda **_kwargs: ({
            "id": "game",
            "url": "https://example.test/game/42",
            "webSocketDebuggerUrl": "ws://game",
        }, False),
    )
    monkeypatch.setattr(jev_browser, "_PageCDP", FakeTransport)

    browser = jev_browser.Browser("https://example.test/start")

    assert browser.resumed is True
    assert not any(method == "Page.navigate" for method, _params in calls)


def test_dynamic_page_text_does_not_invalidate_a_still_valid_control():
    browser = jev_browser.Browser.__new__(jev_browser.Browser)
    guard = [7, "button", "Play", None, None, None, None, False, None, None, None, None, None]
    browser.evaluate = lambda _expression: guard
    page = {"guards": {"7": guard}}

    assert browser.fresh(page, {"node": 7, "kind": "click"}) is True
    assert browser.fresh(page, {"id": "wait", "kind": "wait"}) is True
