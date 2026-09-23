import pytest

from astrorder import jev_browser
from astrorder.agent_mcp import handle_rpc
from astrorder.agent_gateway import CAPABILITIES


def test_browser_capability_is_registered():
    assert any(item["id"] == "browser.run" for item in CAPABILITIES)
    assert any(item["id"] == "browser.screenshot" for item in CAPABILITIES)
    assert any(item["id"] == "browser.cdp" for item in CAPABILITIES)


def test_browser_task_rejects_non_web_urls_before_launch():
    with pytest.raises(ValueError, match="http"):
        jev_browser.run_browser_task(url="file:///etc/passwd", goal="read it", inputs=None, store=None, session_key="agent::session")


def test_field_values_come_from_declared_inputs():
    value, metadata = jev_browser._field_text(
        {"用户名": "luwei"},
        {"field": {"label": "用户名", "role": "textbox"}},
    )

    assert value == "luwei"
    assert metadata["model"] == "astrorder-inputs"

    with pytest.raises(ValueError, match="没有匹配输入值"):
        jev_browser._field_text(
            {"工作内容": "test"},
            {"field": {"label": "工单标题", "role": "textbox"}},
        )

    fallback = lambda _context: ("generated", {"model": "small-llm"})
    assert jev_browser._field_text({}, {"field": {"label": "工单标题"}}, fallback)[0] == "generated"


def test_console_errors_are_collected():
    connection = jev_browser._PageConnection.__new__(jev_browser._PageConnection)
    connection.console_errors = []

    connection._event({
        "method": "Runtime.consoleAPICalled",
        "params": {"type": "error", "args": [{"value": "request failed"}]},
    })

    assert connection.console_errors == ["request failed"]


def test_session_pages_are_isolated(monkeypatch):
    pages = []

    def new_page():
        page = {"id": f"edge-{len(pages)}", "webSocketDebuggerUrl": "ws://edge"}
        pages.append(page)
        return page

    monkeypatch.setattr(jev_browser, "_pages", lambda: pages)
    monkeypatch.setattr(jev_browser, "_new_page", new_page)
    monkeypatch.setattr(jev_browser, "_tag_page", lambda *_args: None)
    monkeypatch.setattr(jev_browser, "_recover_session_targets", lambda _pages: None)
    jev_browser._SESSION_TARGETS.clear()

    first, _ = jev_browser._session_page("agent::one", reset=False)
    second, _ = jev_browser._session_page("agent::two", reset=False)

    assert first["id"] != second["id"]
    assert jev_browser._SESSION_TARGETS == {"agent::one": [first["id"]], "agent::two": [second["id"]]}


def test_closing_session_removes_target_and_screenshot(monkeypatch):
    jev_browser._SESSION_TARGETS["agent::one"] = "edge-one"
    jev_browser._LATEST_SCREENSHOTS["agent::one"] = {"screenshot": "jpeg"}
    closed = []
    monkeypatch.setattr(jev_browser, "_pages", lambda: [])
    monkeypatch.setattr(jev_browser, "_recover_session_targets", lambda _pages: None)
    monkeypatch.setattr(jev_browser, "_close_page", closed.append)

    jev_browser.close_browser_session("agent::one")

    assert closed == ["edge-one"]
    assert "agent::one" not in jev_browser._SESSION_TARGETS
    assert "agent::one" not in jev_browser._LATEST_SCREENSHOTS


def test_visible_edge_uses_the_real_window_viewport():
    calls = []

    class Connection:
        def call(self, method, **params):
            calls.append((method, params))
            return {}

    edge = jev_browser._VisibleEdge("https://example.test", reset=False, session_key="agent::session")
    edge.connection = Connection()
    edge.cdp(
        "Emulation.setDeviceMetricsOverride",
        width=1120,
        height=780,
        deviceScaleFactor=1,
        mobile=False,
    )

    assert calls == []


def test_current_browser_screenshot_is_returned(monkeypatch):
    page = {"id": "edge", "url": "https://example.test", "title": "Example", "webSocketDebuggerUrl": "ws://edge"}

    class Connection:
        def __init__(self, _url):
            pass

        def call(self, method, **_params):
            if method == "Page.captureScreenshot":
                return {"data": "jpeg-base64"}
            assert method == "Runtime.evaluate"
            return {"result": {"value": {"url": "https://example.test", "title": "Example"}}}

        def close(self):
            pass

    monkeypatch.setattr(jev_browser, "_session_page", lambda *_args, **_kwargs: (page, False))
    monkeypatch.setattr(jev_browser, "_PageConnection", Connection)

    assert jev_browser.capture_browser_screenshot("agent::session")["screenshot"] == "jpeg-base64"
    assert jev_browser.latest_browser_screenshot("agent::session")["screenshot"] == "jpeg-base64"
    assert jev_browser.latest_browser_screenshot("agent::other") is None


def test_address_bar_navigation_binds_the_browser_to_session(monkeypatch):
    page = {"id": "edge", "webSocketDebuggerUrl": "ws://edge"}
    calls = []

    class Connection:
        def __init__(self, _url):
            pass

        def call(self, method, **params):
            calls.append((method, params))
            if method == "Runtime.evaluate":
                return {"result": {"value": {"ready": "complete", "url": "https://example.test/home", "title": "Home"}}}
            if method == "Page.captureScreenshot":
                return {"data": "jpeg-base64"}
            return {}

        def close(self):
            pass

    monkeypatch.setattr(jev_browser, "_session_page", lambda *_args, **_kwargs: (page, False))
    monkeypatch.setattr(jev_browser, "_PageConnection", Connection)

    result = jev_browser.navigate_browser(session_key="agent::one", url="https://example.test/home")

    assert result["session_key"] == "agent::one"
    assert result["title"] == "Home"
    assert calls[0] == ("Page.navigate", {"url": "https://example.test/home"})


def test_agent_cdp_is_bound_to_session_and_blocks_browser_domain(monkeypatch):
    page = {"id": "edge-one", "webSocketDebuggerUrl": "ws://edge-one"}

    class Connection:
        def __init__(self, url):
            assert url == "ws://edge-one"

        def call(self, method, **params):
            return {"method": method, "params": params}

        def close(self):
            pass

    monkeypatch.setattr(jev_browser, "_session_page", lambda key, **_kwargs: (page, False) if key == "agent::one" else None)
    monkeypatch.setattr(jev_browser, "_PageConnection", Connection)

    result = jev_browser.call_browser_cdp(
        session_key="agent::one",
        method="Runtime.evaluate",
        params={"expression": "document.title"},
    )
    assert result["target_id"] == "edge-one"
    assert result["result"]["params"]["expression"] == "document.title"
    with pytest.raises(ValueError, match="不允许"):
        jev_browser.call_browser_cdp(session_key="agent::one", method="Target.getTargets")


def test_mcp_returns_screenshot_as_image_content():
    response = handle_rpc(
        {"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "browser_screenshot"}},
        lambda _capability, _arguments: {
            "url": "https://example.test",
            "screenshot": "jpeg-base64",
            "mime_type": "image/jpeg",
        },
    )

    assert response["result"]["content"] == [
        {"type": "text", "text": '{"url": "https://example.test"}'},
        {"type": "image", "data": "jpeg-base64", "mimeType": "image/jpeg"},
    ]


def test_browser_task_delegates_loop_to_upstream_agent(monkeypatch):
    upstream_agent, _upstream_browser = jev_browser._upstream_modules()
    posted = {}
    model_globals = upstream_agent.field_text.__globals__
    monkeypatch.setitem(
        model_globals,
        "post_json",
        lambda _url, _key, body: posted.update(body) or {},
    )

    class FakeEdge:
        def __init__(self, _url, *, reset=False, session_key):
            self.target = {"id": "edge"}
            self.resumed = False
            self.console_errors = []

        def cdp(self, _method, _params=None):
            return {}

    class FakeAgent:
        def __init__(self, url, goal, *, screenshots=False):
            self.url = url
            self.goal = goal
            assert screenshots is False
            assert jev_browser.os.environ["TEXT_MODEL"] == "fast-model"
            model_globals["post_json"](
                "https://model.example/v1/chat/completions",
                "llm-key",
                {
                    "reasoning": {"effort": "low"},
                    "response_format": {"type": "json_object"},
                    "messages": [{"role": "system", "content": "Return JSON."}, {"role": "user", "content": "field context"}],
                },
            )

        def snapshot(self):
            return {"status": "ready"}

        def command(self, name):
            assert name == "tick"
            return {
                "status": "done",
                "page": {"url": self.url, "title": "Result", "text": "complete", "screenshot": "jpeg-base64"},
                "history": [{"step": 1, "action": "Open"}],
                "elapsed_ms": 12,
            }

        def close(self):
            pass

    monkeypatch.setattr(jev_browser, "get_jev_key", lambda _store: "test-key")
    monkeypatch.setattr(jev_browser, "get_llm_config", lambda _store: {
        "base_url": "https://model.example/v1",
        "api_key": "llm-key",
        "model": "fast-model",
        "reasoning": "medium",
    })
    monkeypatch.setattr(jev_browser, "_VisibleEdge", FakeEdge)
    monkeypatch.setattr(jev_browser, "capture_browser_screenshot", lambda _session_key: {
        "url": "https://example.test",
        "title": "Result",
        "screenshot": "jpeg-base64",
        "mime_type": "image/jpeg",
        "revision": 1,
        "captured_at": 1.0,
    })
    monkeypatch.setattr(upstream_agent, "Agent", FakeAgent)

    result = jev_browser.run_browser_task(
        url="https://example.test",
        goal="Open result",
        inputs=None,
        store=object(),
        include_screenshot=True,
        session_key="agent::session",
    )

    assert result["status"] == "done"
    assert result["steps"] == [{"step": 1, "action": "Open"}]
    assert result["screenshot"] == "jpeg-base64"
    assert posted["reasoning"] == {"effort": "medium"}
    assert posted["messages"][1]["content"] == "Return json.\nfield context"


def test_multi_tab_management(monkeypatch):
    pages = []

    def make_page(tid, title, url):
        return {"id": tid, "title": title, "url": url, "webSocketDebuggerUrl": f"ws://{tid}"}

    class DummyConnection:
        def __init__(self, _url):
            pass
        def call(self, method, **params):
            if method == "Page.captureScreenshot":
                return {"data": "test-shot"}
            if method == "Runtime.evaluate":
                return {"result": {"value": {"url": "https://example.test", "title": "Example"}}}
            return {}
        def close(self):
            pass

    monkeypatch.setattr(jev_browser, "_pages", lambda: pages)
    monkeypatch.setattr(jev_browser, "_PageConnection", DummyConnection)
    monkeypatch.setattr(jev_browser, "_tag_page", lambda *_args: None)
    monkeypatch.setattr(jev_browser, "_recover_session_targets", lambda _pages: None)
    jev_browser._SESSION_TARGETS.clear()
    jev_browser._SESSION_ACTIVE_TARGET.clear()
    jev_browser._LATEST_SCREENSHOTS.clear()

    # 1. New first tab
    p1 = make_page("t-1", "Tab 1", "https://t1.test")
    pages.append(p1)
    monkeypatch.setattr(jev_browser, "_new_page", lambda: p1)
    shot1 = jev_browser.new_browser_tab("agent::session")
    assert shot1["active_target_id"] == "t-1"
    assert len(shot1["tabs"]) == 1

    # 2. New second tab
    p2 = make_page("t-2", "Tab 2", "https://t2.test")
    pages.append(p2)
    monkeypatch.setattr(jev_browser, "_new_page", lambda: p2)
    shot2 = jev_browser.new_browser_tab("agent::session")
    assert shot2["active_target_id"] == "t-2"
    assert len(shot2["tabs"]) == 2
    assert [t["id"] for t in shot2["tabs"]] == ["t-1", "t-2"]

    # 3. Select tab 1
    shot3 = jev_browser.select_browser_tab("agent::session", "t-1")
    assert shot3["active_target_id"] == "t-1"
    assert next(t["active"] for t in shot3["tabs"] if t["id"] == "t-1") is True

    # 4. Close tab 1 -> fallback to tab 2
    closed = []
    monkeypatch.setattr(jev_browser, "_close_page", closed.append)
    shot4 = jev_browser.close_browser_tab("agent::session", "t-1")
    assert closed == ["t-1"]
    assert shot4["active_target_id"] == "t-2"
    assert len(shot4["tabs"]) == 1

    # 5. Close tab 2 -> empty
    shot5 = jev_browser.close_browser_tab("agent::session", "t-2")
    assert shot5["active_target_id"] is None
    assert shot5["tabs"] == []


def test_interact_and_diagnostics(monkeypatch):
    page = {"id": "target-1", "url": "https://test.local", "title": "Test Page", "webSocketDebuggerUrl": "ws://test"}
    calls = []

    class Connection:
        def __init__(self, _url, session_key=None, target_id=None):
            self.session_key = session_key
            self.target_id = target_id
            self.console_errors = []

        def call(self, method, **params):
            calls.append((method, params))
            if method == "Runtime.evaluate":
                return {"result": {"value": {"title": "Test Page", "url": "https://test.local", "text": "Page body content"}}}
            if method == "Page.captureScreenshot":
                return {"data": "shot"}
            return {}

        def close(self):
            pass

    monkeypatch.setattr(jev_browser, "_session_page", lambda *_args, **_kwargs: (page, False))
    monkeypatch.setattr(jev_browser, "_PageConnection", Connection)

    # 1. Click interaction
    shot = jev_browser.interact_browser(session_key="agent::session", action="click", ratio_x=0.5, ratio_y=0.5)
    assert shot["screenshot"] == "shot"
    assert any(m == "Input.dispatchMouseEvent" and p.get("type") == "mousePressed" for m, p in calls)

    # 2. Extract content
    extracted = jev_browser.extract_page_content(session_key="agent::session")
    assert extracted["title"] == "Test Page"
    assert "Page body" in extracted["text"]

    # 3. Diagnostics
    jev_browser._SESSION_LOGS["agent::session::target-1"] = [{"type": "error", "text": "Something broke", "time": 100}]
    diag = jev_browser.get_browser_diagnostics(session_key="agent::session", target_id="target-1")
    assert len(diag["logs"]) == 1
    assert diag["logs"][0]["text"] == "Something broke"
