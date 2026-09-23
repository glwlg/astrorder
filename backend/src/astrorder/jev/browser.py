from __future__ import annotations

import io
import json
import os
import subprocess
import threading
import time
import base64
from pathlib import Path
from typing import Any
from urllib.parse import urlparse
from urllib.request import urlopen

from websockets.exceptions import ConnectionClosed
from websockets.sync.client import connect

from astrorder.jev_client import get_jev_key
from astrorder.llm_config import get_llm_config, reasoning_payload

_RUN_LOCK = threading.Lock()  # ponytail: one patched upstream Agent at a time.
_SCREENSHOT_LOCK = threading.Lock()
_TARGET_LOCK = threading.Lock()
_SESSION_TARGETS: dict[str, list[str]] = {}
_SESSION_ACTIVE_TARGET: dict[str, str] = {}
_SESSION_LOGS: dict[str, list[dict[str, Any]]] = {}
_LATEST_SCREENSHOTS: dict[str, dict[str, Any]] = {}
_DEVTOOLS_PORT = 9337
_DEVTOOLS = f"http://127.0.0.1:{_DEVTOOLS_PORT}"


def _upstream_modules():
    # Upstream 0.1.0 omits encoding= on snapshot.js; Windows otherwise decodes it as GBK.
    original = io.text_encoding
    io.text_encoding = lambda encoding, stacklevel=2: encoding or "utf-8"
    try:
        from jev_ultrafast import agent, browser

        return agent, browser
    finally:
        io.text_encoding = original


def _edge_executable() -> Path:
    for root in (os.environ.get("PROGRAMFILES(X86)"), os.environ.get("PROGRAMFILES")):
        candidate = Path(root or "") / "Microsoft/Edge/Application/msedge.exe"
        if candidate.is_file():
            return candidate
    raise RuntimeError("未找到 Microsoft Edge，无法启动可视浏览器")


def _pages() -> list[dict[str, Any]]:
    with urlopen(f"{_DEVTOOLS}/json/list", timeout=1) as response:
        return [item for item in json.load(response) if item.get("type") == "page"]


def _close_page(target_id: str) -> None:
    try:
        urlopen(f"{_DEVTOOLS}/json/close/{target_id}", timeout=1).close()
    except OSError:
        pass


def _session_marker(session_key: str) -> str:
    encoded = base64.urlsafe_b64encode(session_key.encode()).decode().rstrip("=")
    return f"astrorder:{encoded}"


def _marker_session_key(marker: str) -> str | None:
    if not marker.startswith("astrorder:"):
        return None
    try:
        value = marker.removeprefix("astrorder:")
        return base64.urlsafe_b64decode(value + "=" * (-len(value) % 4)).decode()
    except (ValueError, UnicodeDecodeError):
        return None


def _extension_path() -> Path:
    return Path(__file__).with_name("browser_extension")


def _start_browser() -> None:
    profile = Path(os.environ["LOCALAPPDATA"]) / "Astrorder/browser-profile"
    extension = _extension_path()
    args = [
        str(_edge_executable()),
        f"--remote-debugging-port={_DEVTOOLS_PORT}",
        f"--user-data-dir={profile}",
        "--no-first-run",
        "--no-default-browser-check",
        f"--disable-extensions-except={extension}",
        f"--load-extension={extension}",
        "--new-window",
        "about:blank",
    ]
    subprocess.Popen(args, close_fds=True)
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        try:
            _pages()
            return
        except OSError:
            time.sleep(0.1)
    raise RuntimeError("Edge 已启动，但 CDP 端口不可用")


def _new_page() -> dict[str, Any]:
    try:
        with urlopen(f"{_DEVTOOLS}/json/version", timeout=1) as response:
            websocket_url = json.load(response)["webSocketDebuggerUrl"]
    except OSError:
        _start_browser()
        with urlopen(f"{_DEVTOOLS}/json/version", timeout=1) as response:
            websocket_url = json.load(response)["webSocketDebuggerUrl"]
    connection = _PageConnection(websocket_url)
    try:
        target_id = connection.call("Target.createTarget", url="about:blank")["targetId"]
        connection.call("Target.activateTarget", targetId=target_id)
    finally:
        connection.close()
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        page = next((item for item in _pages() if item["id"] == target_id), None)
        if page:
            return page
        time.sleep(0.05)
    raise RuntimeError("Edge 已创建标签页，但未能连接")


def _tag_page(page: dict[str, Any], session_key: str) -> None:
    connection = _PageConnection(page["webSocketDebuggerUrl"])
    try:
        connection.call(
            "Runtime.evaluate",
            expression=f"window.name={json.dumps(_session_marker(session_key))}",
            returnByValue=True,
        )
    finally:
        connection.close()


def _recover_session_targets(pages: list[dict[str, Any]]) -> None:
    page_ids = {page["id"] for page in pages}
    for session_key in list(_SESSION_TARGETS.keys()):
        _SESSION_TARGETS[session_key] = [tid for tid in _SESSION_TARGETS[session_key] if tid in page_ids]
        if not _SESSION_TARGETS[session_key]:
            _SESSION_TARGETS.pop(session_key, None)
            _SESSION_ACTIVE_TARGET.pop(session_key, None)
        elif _SESSION_ACTIVE_TARGET.get(session_key) not in _SESSION_TARGETS[session_key]:
            _SESSION_ACTIVE_TARGET[session_key] = _SESSION_TARGETS[session_key][-1]

    known = {tid for targets in _SESSION_TARGETS.values() for tid in targets}
    for page in pages:
        if page["id"] in known:
            continue
        connection = _PageConnection(page["webSocketDebuggerUrl"])
        try:
            result = connection.call("Runtime.evaluate", expression="window.name", returnByValue=True)
            session_key = _marker_session_key(str((result.get("result") or {}).get("value") or ""))
            if session_key:
                targets = _SESSION_TARGETS.setdefault(session_key, [])
                if page["id"] not in targets:
                    targets.append(page["id"])
                if session_key not in _SESSION_ACTIVE_TARGET:
                    _SESSION_ACTIVE_TARGET[session_key] = page["id"]
        except (OSError, RuntimeError):
            continue
        finally:
            connection.close()


def _session_page(session_key: str, *, reset: bool, create: bool = True, target_id: str | None = None) -> tuple[dict[str, Any], bool]:
    try:
        pages = _pages()
    except OSError:
        pages = []
    with _TARGET_LOCK:
        _recover_session_targets(pages)
        targets = _SESSION_TARGETS.setdefault(session_key, [])
        resolved_id = target_id or _SESSION_ACTIVE_TARGET.get(session_key)
        existing = next((page for page in pages if page["id"] == resolved_id), None)
        if existing and not reset:
            _SESSION_ACTIVE_TARGET[session_key] = existing["id"]
            return existing, False
        if existing:
            _close_page(existing["id"])
            if existing["id"] in targets:
                targets.remove(existing["id"])
            with _SCREENSHOT_LOCK:
                _LATEST_SCREENSHOTS.pop(session_key, None)
                _LATEST_SCREENSHOTS.pop(f"{session_key}::{existing['id']}", None)
        if not create:
            raise RuntimeError("该会话还没有浏览器标签页")
        page = _new_page()
        _tag_page(page, session_key)
        if page["id"] not in targets:
            targets.append(page["id"])
        _SESSION_ACTIVE_TARGET[session_key] = page["id"]
        return page, True


def list_browser_tabs(session_key: str) -> dict[str, Any]:
    try:
        pages = _pages()
    except OSError:
        pages = []
    with _TARGET_LOCK:
        _recover_session_targets(pages)
        targets = _SESSION_TARGETS.get(session_key, [])
        active_id = _SESSION_ACTIVE_TARGET.get(session_key)
        page_map = {page["id"]: page for page in pages}
        tabs = []
        for tid in targets:
            page = page_map.get(tid)
            if page:
                tabs.append({
                    "id": tid,
                    "title": page.get("title") or "新标签页",
                    "url": page.get("url") or "about:blank",
                    "active": tid == active_id,
                })
        return {
            "session_key": session_key,
            "active_target_id": active_id,
            "tabs": tabs,
        }


def close_browser_session(session_key: str) -> None:
    try:
        pages = _pages()
    except OSError:
        pages = []
    with _TARGET_LOCK:
        _recover_session_targets(pages)
        raw_targets = _SESSION_TARGETS.pop(session_key, [])
        targets = [raw_targets] if isinstance(raw_targets, str) else list(raw_targets)
        _SESSION_ACTIVE_TARGET.pop(session_key, None)
    with _SCREENSHOT_LOCK:
        _LATEST_SCREENSHOTS.pop(session_key, None)
        for tid in targets:
            _LATEST_SCREENSHOTS.pop(f"{session_key}::{tid}", None)
    for tid in targets:
        _close_page(tid)


def select_browser_tab(session_key: str, target_id: str) -> dict[str, Any]:
    try:
        pages = _pages()
    except OSError:
        pages = []
    with _TARGET_LOCK:
        _recover_session_targets(pages)
        targets = _SESSION_TARGETS.get(session_key, [])
        if target_id not in targets:
            raise ValueError("指定的标签页不属于当前会话")
        _SESSION_ACTIVE_TARGET[session_key] = target_id
    try:
        with urlopen(f"{_DEVTOOLS}/json/version", timeout=1) as response:
            ws_url = json.load(response)["webSocketDebuggerUrl"]
        conn = _PageConnection(ws_url)
        try:
            conn.call("Target.activateTarget", targetId=target_id)
        finally:
            conn.close()
    except Exception:
        pass
    return capture_browser_screenshot(session_key, target_id=target_id)


def new_browser_tab(session_key: str, url: str | None = None) -> dict[str, Any]:
    try:
        pages = _pages()
    except OSError:
        pages = []
    with _TARGET_LOCK:
        _recover_session_targets(pages)
    page = _new_page()
    _tag_page(page, session_key)
    with _TARGET_LOCK:
        targets = _SESSION_TARGETS.setdefault(session_key, [])
        if page["id"] not in targets:
            targets.append(page["id"])
        _SESSION_ACTIVE_TARGET[session_key] = page["id"]
    if url and url != "about:blank":
        return navigate_browser(session_key=session_key, url=url, target_id=page["id"])
    return capture_browser_screenshot(session_key, target_id=page["id"])


def close_browser_tab(session_key: str, target_id: str) -> dict[str, Any]:
    try:
        pages = _pages()
    except OSError:
        pages = []
    with _TARGET_LOCK:
        _recover_session_targets(pages)
        targets = _SESSION_TARGETS.get(session_key, [])
        if target_id in targets:
            targets.remove(target_id)
        if _SESSION_ACTIVE_TARGET.get(session_key) == target_id:
            _SESSION_ACTIVE_TARGET[session_key] = targets[-1] if targets else None
        next_active = _SESSION_ACTIVE_TARGET.get(session_key)
    with _SCREENSHOT_LOCK:
        _LATEST_SCREENSHOTS.pop(f"{session_key}::{target_id}", None)
    _close_page(target_id)
    if next_active:
        return capture_browser_screenshot(session_key, target_id=next_active)
    with _SCREENSHOT_LOCK:
        _LATEST_SCREENSHOTS.pop(session_key, None)
    return {
        "url": "",
        "title": "",
        "screenshot": "",
        "mime_type": "image/jpeg",
        "revision": time.time_ns(),
        "captured_at": time.time(),
        "session_key": session_key,
        "target_id": None,
        "active_target_id": None,
        "tabs": [],
    }


def _remember_screenshot(*, session_key: str, data: str, page: dict[str, Any], target_id: str | None = None) -> dict[str, Any]:
    tabs_info = list_browser_tabs(session_key)
    tid = target_id or tabs_info.get("active_target_id") or ""
    snapshot = {
        "url": page.get("url", ""),
        "title": page.get("title", ""),
        "screenshot": data,
        "mime_type": "image/jpeg",
        "revision": time.time_ns(),
        "captured_at": time.time(),
        "session_key": session_key,
        "target_id": tid,
        "active_target_id": tabs_info.get("active_target_id"),
        "tabs": tabs_info.get("tabs", []),
        "error_count": sum(1 for item in _SESSION_LOGS.get(f"{session_key}::{tid}", []) if item.get("type") in {"error", "network_error"}),
    }
    with _SCREENSHOT_LOCK:
        _LATEST_SCREENSHOTS[session_key] = snapshot
        if tid:
            _LATEST_SCREENSHOTS[f"{session_key}::{tid}"] = snapshot
    return dict(snapshot)


def latest_browser_screenshot(session_key: str, target_id: str | None = None) -> dict[str, Any] | None:
    with _SCREENSHOT_LOCK:
        key = f"{session_key}::{target_id}" if target_id else session_key
        snapshot = _LATEST_SCREENSHOTS.get(key)
        if snapshot:
            res = dict(snapshot)
            tabs_info = list_browser_tabs(session_key)
            res["tabs"] = tabs_info.get("tabs", [])
            res["active_target_id"] = tabs_info.get("active_target_id")
            return res
        return None


def capture_browser_screenshot(session_key: str, *, require_owner: bool = False, target_id: str | None = None) -> dict[str, Any]:
    page, _created = _session_page(session_key, reset=False, create=not require_owner, target_id=target_id)
    connection = _PageConnection(page["webSocketDebuggerUrl"])
    try:
        data = connection.call("Page.captureScreenshot", format="jpeg", quality=80)["data"]
        state = connection.call(
            "Runtime.evaluate",
            expression="({url:location.href,title:document.title})",
            returnByValue=True,
        )
        page.update((state.get("result") or {}).get("value") or {})
    finally:
        connection.close()
    return _remember_screenshot(session_key=session_key, data=data, page=page, target_id=page["id"])


def navigate_browser(*, session_key: str, url: str, target_id: str | None = None) -> dict[str, Any]:
    parsed = urlparse(url)
    if parsed.scheme not in {"http", "https"} or not parsed.netloc:
        raise ValueError("url 必须是完整的 http 或 https 地址")
    with _RUN_LOCK:
        page, _created = _session_page(session_key, reset=False, target_id=target_id)
        connection = _PageConnection(page["webSocketDebuggerUrl"])
        try:
            connection.call("Page.navigate", url=url)
            deadline = time.monotonic() + 15
            state: dict[str, Any] = {"url": url, "title": ""}
            while time.monotonic() < deadline:
                response = connection.call(
                    "Runtime.evaluate",
                    expression="({ready:document.readyState,url:location.href,title:document.title})",
                    returnByValue=True,
                )
                value = (response.get("result") or {}).get("value") or {}
                state.update(url=value.get("url", state["url"]), title=value.get("title", ""))
                if value.get("ready") == "complete":
                    break
                time.sleep(0.05)
            connection.call("Runtime.evaluate", expression=f"window.name={json.dumps(_session_marker(session_key))}")
            data = connection.call("Page.captureScreenshot", format="jpeg", quality=80)["data"]
        finally:
            connection.close()
        return {
            **_remember_screenshot(session_key=session_key, data=data, page=state, target_id=page["id"]),
            "session_key": session_key,
        }


_AGENT_CDP_DOMAINS = {"Runtime", "DOM", "CSS", "Console", "Log", "Network", "Performance", "Page"}


def call_browser_cdp(*, session_key: str, method: str, params: dict[str, Any] | None = None, target_id: str | None = None) -> dict[str, Any]:
    domain, separator, _command = method.partition(".")
    if not separator or domain not in _AGENT_CDP_DOMAINS:
        raise ValueError(f"不允许调用 CDP 方法：{method}")
    page, _created = _session_page(session_key, reset=False, create=False, target_id=target_id)
    connection = _PageConnection(page["webSocketDebuggerUrl"])
    try:
        return {
            "session_key": session_key,
            "target_id": page["id"],
            "method": method,
            "result": connection.call(method, **(params or {})),
        }
    finally:
        connection.close()


class _PageConnection:
    def __init__(self, websocket_url: str, session_key: str | None = None, target_id: str | None = None):
        self.websocket = connect(websocket_url, open_timeout=5)
        self.request_id = 0
        self.session_key = session_key
        self.target_id = target_id
        self.console_errors: list[str] = []

    def _record_log(self, log_type: str, message: str) -> None:
        if not getattr(self, "session_key", None) or not getattr(self, "target_id", None):
            return
        key = f"{self.session_key}::{self.target_id}"
        logs = _SESSION_LOGS.setdefault(key, [])
        logs.append({
            "type": log_type,
            "text": str(message)[:1000],
            "time": time.time(),
        })
        if len(logs) > 100:
            del logs[:-100]

    def _event(self, response: dict[str, Any]) -> None:
        method, params = response.get("method"), response.get("params") or {}
        message = None
        if method == "Runtime.exceptionThrown":
            details = params.get("exceptionDetails") or {}
            message = (details.get("exception") or {}).get("description") or details.get("text")
            if message:
                self._record_log("error", message)
        elif method == "Runtime.consoleAPICalled":
            msg_type = params.get("type", "log")
            message = " ".join(
                str(arg.get("value") or arg.get("description") or "") for arg in params.get("args", [])
            ).strip()
            if message:
                self._record_log("error" if msg_type in {"error", "assert"} else msg_type, message)
        elif method == "Log.entryAdded":
            entry = params.get("entry") or {}
            level = entry.get("level", "info")
            message = entry.get("text")
            if message:
                self._record_log("error" if level == "error" else level, message)
        elif method == "Network.loadingFailed":
            err = f"{params.get('type', 'Resource')} 加载失败: {params.get('errorText', 'failed')}"
            self._record_log("network_error", err)
        elif method == "Network.responseReceived":
            status = (params.get("response") or {}).get("status", 0)
            if status >= 400:
                url = (params.get("response") or {}).get("url", "")
                self._record_log("network_error", f"HTTP {status}: {url}")

        if message and method in {"Runtime.exceptionThrown", "Runtime.consoleAPICalled", "Log.entryAdded"}:
            if (params.get("type") in {"error", "assert"} or (params.get("entry") or {}).get("level") == "error" or method == "Runtime.exceptionThrown"):
                if message not in self.console_errors:
                    self.console_errors.append(str(message)[:1000])

    def call(self, method: str, **params: Any) -> dict[str, Any]:
        self.request_id += 1
        request_id = self.request_id
        self.websocket.send(json.dumps({"id": request_id, "method": method, "params": params}))
        while True:
            try:
                response = json.loads(self.websocket.recv())
            except ConnectionClosed as exc:
                raise RuntimeError("浏览器页面连接中断，可以在原窗口继续任务") from exc
            if response.get("id") != request_id:
                self._event(response)
                continue
            if "error" in response:
                raise RuntimeError(response["error"].get("message", f"CDP 调用失败：{method}"))
            return response.get("result", {})

    def close(self) -> None:
        self.websocket.close()


_ACCESSIBILITY_BRIDGE = """(() => {
  for (const e of document.querySelectorAll('body *')) {
    if (e.matches('button') && !e.innerText?.trim() && !e.getAttribute('aria-label')) {
      const label = e.getAttribute('title') || (e.querySelector('svg.lucide-x') ? 'Close' : '');
      if (label) e.setAttribute('aria-label', label);
    }
  }
})()"""


class _VisibleEdge:
    """CDP adapter for upstream jev-ultrafast; no decision or execution policy lives here."""

    def __init__(self, url: str, *, reset: bool, session_key: str):
        self.url = url
        self.reset = reset
        self.session_key = session_key
        self.target: dict[str, Any] | None = None
        self.connection: _PageConnection | None = None
        self.resumed = False

    def cdp(self, method: str, session_id: str | None = None, **params: Any) -> dict[str, Any]:
        if method == "Target.createTarget":
            self.target, created = _session_page(self.session_key, reset=self.reset)
            self.resumed = not created
            return {"targetId": self.target["id"]}
        if method == "Target.attachToTarget":
            if not self.target:
                raise RuntimeError("浏览器页面尚未创建")
            self.connection = _PageConnection(self.target["webSocketDebuggerUrl"])
            self.connection.call("Runtime.enable")
            self.connection.call("Log.enable")
            return {"sessionId": self.target["id"]}
        if method == "Target.closeTarget":
            if self.connection:
                self.connection.close()
                self.connection = None
            return {"success": True}
        if not self.connection:
            raise RuntimeError("浏览器页面尚未连接")
        if method == "Emulation.setDeviceMetricsOverride":
            return {}
        if method == "Page.navigate" and self.resumed and self.target:
            current, requested = urlparse(self.target.get("url", "")), urlparse(params["url"])
            if current.scheme in {"http", "https"} and current.netloc == requested.netloc:
                return {}
        if method == "Runtime.evaluate" and params.get("expression") != _ACCESSIBILITY_BRIDGE:
            self.connection.call("Runtime.evaluate", expression=_ACCESSIBILITY_BRIDGE)
        result = self.connection.call(method, **params)
        if method == "Page.navigate":
            self.connection.call(
                "Runtime.evaluate",
                expression=f"window.name={json.dumps(_session_marker(self.session_key))}",
            )
        return result

    @property
    def console_errors(self) -> list[str]:
        return list(self.connection.console_errors if self.connection else [])


def _field_text(inputs: dict[str, str], context: dict[str, Any], fallback=None) -> tuple[str, dict[str, Any]]:
    label = "".join(char.casefold() for char in context["field"]["label"] if char.isalnum())
    matches = [
        (len(key), value)
        for raw_key, value in inputs.items()
        if (key := "".join(char.casefold() for char in raw_key if char.isalnum()))
        and (key in label or label in key)
    ]
    if not matches:
        if fallback:
            try:
                return fallback(context)
            except ValueError as exc:
                raise ValueError(f"文本框“{context['field']['label']}”未能生成内容") from exc
        raise ValueError(f"文本框“{context['field']['label']}”没有匹配输入值")
    value = max(matches)[1]
    return value, {"model": "astrorder-inputs", "latency_ms": 0, "usage": {}}


def run_browser_task(
    *,
    url: str,
    goal: str,
    inputs: dict[str, str] | None,
    store: Any,
    max_steps: int = 30,
    reset: bool = False,
    include_screenshot: bool = False,
    session_key: str,
) -> dict[str, Any]:
    parsed = urlparse(url)
    if parsed.scheme not in {"http", "https"} or not parsed.netloc:
        raise ValueError("url 必须是完整的 http 或 https 地址")
    if not goal.strip():
        raise ValueError("goal 不能为空")
    if not 1 <= max_steps <= 60:
        raise ValueError("max_steps 必须在 1 到 60 之间")
    key = get_jev_key(store)
    if not key:
        raise ValueError("Jev API Key 未配置，请在设置中配置 Jev API Key。")

    upstream_agent, upstream_browser = _upstream_modules()
    llm = get_llm_config(store)

    values = {str(name).strip(): str(value) for name, value in (inputs or {}).items() if str(name).strip()}
    edge = _VisibleEdge(url, reset=reset, session_key=session_key)
    environment = {
        "TYPESAFE_API_KEY": key,
        "TEXT_MODEL_API_KEY": llm["api_key"],
        "TEXT_MODEL_BASE_URL": llm["base_url"],
        "TEXT_MODEL": llm["model"],
        "TEXT_MODEL_REASONING": llm["reasoning"],
    }
    previous_environment = {name: os.environ.get(name) for name in environment}
    with _RUN_LOCK:
        original_cdp = upstream_browser.cdp
        original_daemon = upstream_browser.ensure_daemon
        original_field_text = upstream_agent.field_text
        original_post_json = original_field_text.__globals__["post_json"]

        def configured_post_json(url, api_key, body):
            if url.endswith("/chat/completions"):
                for name in ("reasoning", "reasoning_effort", "thinking"):
                    body.pop(name, None)
                body.update(reasoning_payload(llm["base_url"], llm["reasoning"]))
                if body.get("response_format", {}).get("type") == "json_object":
                    user = next((message for message in reversed(body.get("messages", [])) if message.get("role") == "user"), None)
                    if user and isinstance(user.get("content"), str):
                        user["content"] = "Return json.\n" + user["content"]
            return original_post_json(url, api_key, body)

        upstream_browser.cdp = edge.cdp
        upstream_browser.ensure_daemon = lambda: None
        upstream_agent.field_text = lambda context: _field_text(values, context, original_field_text)
        original_field_text.__globals__["post_json"] = configured_post_json
        os.environ.update(environment)
        agent = None
        try:
            agent = upstream_agent.Agent(url, goal)
            state = agent.snapshot()
            for _ in range(max_steps):
                if state["status"] in {"done", "blocked"}:
                    break
                state = agent.command("tick")
            if state["status"] not in {"done", "blocked"}:
                state["status"] = "step_limit"
            page = state["page"]
            errors = edge.console_errors
            try:
                mirror = capture_browser_screenshot(session_key)
            except RuntimeError:
                if include_screenshot:
                    raise
                mirror = latest_browser_screenshot(session_key) or {}
            result = {
                "ok": state["status"] == "done",
                "status": state["status"],
                "browser_session": edge.target["id"] if edge.target else None,
                "resumed": edge.resumed,
                "url": page["url"],
                "title": page["title"],
                "visible_text": page["text"],
                "console_errors": errors,
                "steps": state["history"],
                "elapsed_ms": state["elapsed_ms"],
                "screenshot_revision": mirror.get("revision"),
                "session_key": session_key,
            }
            if include_screenshot and mirror.get("screenshot"):
                result.update(screenshot=mirror["screenshot"], mime_type=mirror["mime_type"])
            return result
        finally:
            if agent:
                agent.close()
            upstream_browser.cdp = original_cdp
            upstream_browser.ensure_daemon = original_daemon
            upstream_agent.field_text = original_field_text
            original_field_text.__globals__["post_json"] = original_post_json
            for name, old_value in previous_environment.items():
                if old_value is None:
                    os.environ.pop(name, None)
                else:
                    os.environ[name] = old_value


def interact_browser(
    *,
    session_key: str,
    action: str,
    x: float | None = None,
    y: float | None = None,
    ratio_x: float | None = None,
    ratio_y: float | None = None,
    delta_y: float | None = None,
    text: str | None = None,
    target_id: str | None = None,
) -> dict[str, Any]:
    with _RUN_LOCK:
        page, _created = _session_page(session_key, reset=False, target_id=target_id)
        connection = _PageConnection(page["webSocketDebuggerUrl"], session_key=session_key, target_id=page["id"])
        try:
            connection.call("Runtime.enable")
            connection.call("Log.enable")
            viewport = connection.call(
                "Runtime.evaluate",
                expression="({width: window.innerWidth, height: window.innerHeight})",
                returnByValue=True,
            )
            val = (viewport.get("result") or {}).get("value") or {}
            vw = val.get("width") or 1120
            vh = val.get("height") or 780

            if ratio_x is not None and ratio_y is not None:
                px = round(float(ratio_x) * vw)
                py = round(float(ratio_y) * vh)
            else:
                px = round(float(x or 0))
                py = round(float(y or 0))

            if action == "click":
                connection.call("Input.dispatchMouseEvent", type="mouseMoved", x=px, y=py)
                connection.call("Input.dispatchMouseEvent", type="mousePressed", x=px, y=py, button="left", clickCount=1)
                connection.call("Input.dispatchMouseEvent", type="mouseReleased", x=px, y=py, button="left", clickCount=1)
                time.sleep(0.4)
            elif action == "wheel":
                connection.call("Input.dispatchMouseEvent", type="mouseWheel", x=px, y=py, deltaX=0, deltaY=delta_y or 100)
                time.sleep(0.15)
            elif action == "text":
                connection.call("Input.insertText", text=text or "")
                time.sleep(0.1)
            elif action == "back":
                connection.call("Runtime.evaluate", expression="window.history.back()")
                time.sleep(0.5)
            elif action == "forward":
                connection.call("Runtime.evaluate", expression="window.history.forward()")
                time.sleep(0.5)
            else:
                raise ValueError(f"未知交互动作：{action}")

            state = connection.call(
                "Runtime.evaluate",
                expression="({url:location.href,title:document.title})",
                returnByValue=True,
            )
            page.update((state.get("result") or {}).get("value") or {})
            data = connection.call("Page.captureScreenshot", format="jpeg", quality=80)["data"]
        finally:
            connection.close()
        return {
            **_remember_screenshot(session_key=session_key, data=data, page=page, target_id=page["id"]),
            "session_key": session_key,
        }


def extract_page_content(*, session_key: str, target_id: str | None = None) -> dict[str, Any]:
    page, _created = _session_page(session_key, reset=False, create=False, target_id=target_id)
    connection = _PageConnection(page["webSocketDebuggerUrl"])
    try:
        expr = """(() => {
            const title = document.title || '';
            const url = location.href;
            const text = (document.body ? document.body.innerText : '').slice(0, 5000);
            return { title, url, text };
        })()"""
        result = connection.call("Runtime.evaluate", expression=expr, returnByValue=True)
        val = (result.get("result") or {}).get("value") or {}
        return {
            "title": val.get("title") or page.get("title", ""),
            "url": val.get("url") or page.get("url", ""),
            "text": val.get("text") or "",
            "session_key": session_key,
            "target_id": page["id"],
        }
    finally:
        connection.close()


def get_browser_diagnostics(*, session_key: str, target_id: str | None = None) -> dict[str, Any]:
    tid = target_id or _SESSION_ACTIVE_TARGET.get(session_key)
    logs = _SESSION_LOGS.get(f"{session_key}::{tid}", [])
    return {
        "session_key": session_key,
        "target_id": tid,
        "logs": list(logs[-100:]),
    }
