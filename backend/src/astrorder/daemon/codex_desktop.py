"""Send text through an already-running Codex Desktop renderer."""
from __future__ import annotations

import asyncio
import json
import os
import re
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit
from urllib.request import ProxyHandler, build_opener

import websockets

from .errors import DaemonProtocolError

_ID = re.compile(r"^[A-Za-z0-9._-]{1,200}$")
_LOOPBACK = {"127.0.0.1", "localhost", "::1"}


def _state_path() -> Path:
    root = os.environ.get("LOCALAPPDATA")
    if not root:
        raise DaemonProtocolError("Codex Desktop debugging is unavailable")
    return Path(root) / "AstrOrder" / "CodexCdp" / "state.json"


def _read_state(path: Path) -> tuple[int, str]:
    try:
        raw = path.read_bytes()
        if len(raw) > 16_384:
            raise ValueError
        state = json.loads(raw)
        port, browser_id = state["port"], state["browserId"]
    except (OSError, ValueError, KeyError, TypeError, json.JSONDecodeError):
        raise DaemonProtocolError("Codex Desktop debugging is unavailable") from None
    if not isinstance(port, int) or not 1024 <= port <= 65535 or not isinstance(browser_id, str) or not _ID.fullmatch(browser_id):
        raise DaemonProtocolError("Codex Desktop debugging state is invalid")
    return port, browser_id


def _fetch_json(port: int, resource: str) -> Any:
    try:
        with build_opener(ProxyHandler({})).open(
            f"http://127.0.0.1:{port}{resource}", timeout=2
        ) as response:
            payload = response.read(1_048_577)
        if len(payload) > 1_048_576:
            raise ValueError
        return json.loads(payload)
    except (OSError, ValueError):
        raise DaemonProtocolError("Codex Desktop debugging endpoint is unavailable") from None


def codex_desktop_status(*, state_path: Path | None = None) -> dict[str, Any]:
    current_state_path: Path | None = None
    try:
        current_state_path = state_path or _state_path()
        port, browser_id = _read_state(current_state_path)
        version = _fetch_json(port, "/json/version")
        browser_url = _debugger_url(version, port, "browser")
        if not browser_url.endswith("/" + browser_id):
            raise DaemonProtocolError("Codex Desktop debugging identity changed")
    except DaemonProtocolError as exc:
        if "endpoint is unavailable" in str(exc) and current_state_path is not None:
            current_state_path.unlink(missing_ok=True)
        return {"available": False, "detail": str(exc)}
    return {"available": True, "port": port}


def _debugger_url(target: Any, port: int, kind: str) -> str:
    if not isinstance(target, dict):
        raise DaemonProtocolError("Codex Desktop returned an invalid debugging target")
    value = target.get("webSocketDebuggerUrl")
    try:
        parsed = urlsplit(value)
        actual_port = parsed.port
    except (TypeError, ValueError):
        raise DaemonProtocolError("Codex Desktop returned an invalid debugging target") from None
    expected = rf"^/devtools/{kind}/[A-Za-z0-9._-]{{1,200}}$"
    if (
        parsed.scheme != "ws"
        or parsed.hostname not in _LOOPBACK
        or actual_port != port
        or parsed.username
        or parsed.password
        or parsed.query
        or parsed.fragment
        or not re.fullmatch(expected, parsed.path)
    ):
        raise DaemonProtocolError("Codex Desktop returned an invalid debugging target")
    return value


async def _call(socket: Any, identifier: int, method: str, params: dict[str, Any]) -> dict[str, Any]:
    await socket.send(json.dumps({"id": identifier, "method": method, "params": params}))
    async with asyncio.timeout(10):
        while True:
            try:
                response = json.loads(await socket.recv())
            except (TypeError, ValueError, json.JSONDecodeError):
                raise DaemonProtocolError("Codex Desktop returned an invalid CDP response") from None
            if not isinstance(response, dict) or response.get("id") != identifier:
                continue
            if response.get("error"):
                raise DaemonProtocolError("Codex Desktop rejected renderer control")
            result = response.get("result")
            if not isinstance(result, dict):
                raise DaemonProtocolError("Codex Desktop returned an invalid CDP response")
            return result


async def _evaluate(socket: Any, identifier: int, expression: str) -> Any:
    result = await _call(
        socket,
        identifier,
        "Runtime.evaluate",
        {"expression": expression, "awaitPromise": True, "returnByValue": True},
    )
    if result.get("exceptionDetails"):
        raise DaemonProtocolError("Codex Desktop renderer injection failed")
    value = result.get("result")
    if not isinstance(value, dict):
        raise DaemonProtocolError("Codex Desktop returned an invalid renderer result")
    return value.get("value")


def _thread_expression(session_id: str, *, focus: bool) -> str:
    sid = json.dumps(session_id)
    action = """
      if (row.getAttribute('data-app-action-sidebar-thread-active') !== 'true') row.click();
      const deadline = Date.now() + 5000;
      while (row.getAttribute('data-app-action-sidebar-thread-active') !== 'true' && Date.now() < deadline) {
        await new Promise(resolve => setTimeout(resolve, 50));
      }
      await new Promise(resolve => setTimeout(resolve, 100));
      const root = document.querySelector('[data-codex-composer-root]');
      const input = root?.querySelector('[contenteditable="true"], textarea, [role="textbox"]');
      if (!input || row.getAttribute('data-app-action-sidebar-thread-active') !== 'true') return false;
      input.focus();
      if (input instanceof HTMLTextAreaElement || input instanceof HTMLInputElement) input.select();
      else {
        const selection = getSelection();
        const range = document.createRange();
        range.selectNodeContents(input);
        selection.removeAllRanges();
        selection.addRange(range);
      }
    """ if focus else ""
    return f"""(async () => {{
      const sid = {sid};
      const matches = value => value === sid || value?.endsWith(':' + sid) || value?.endsWith('/' + sid);
      const rows = [...document.querySelectorAll('[data-app-action-sidebar-thread-id]')].filter(
        row => matches(row.getAttribute('data-app-action-sidebar-thread-id'))
      );
      if (rows.length !== 1) return false;
      const row = rows[0];
      {action}
      return true;
    }})()"""


def desktop_message_input(value: Any) -> tuple[str, list[str]]:
    inputs = [{"type": "text", "text": value}] if isinstance(value, str) else value
    if not isinstance(inputs, list):
        raise DaemonProtocolError("Codex Desktop message is invalid")
    texts = [
        item.get("text")
        for item in inputs
        if isinstance(item, dict) and item.get("type") == "text"
    ]
    images = [
        item.get("url")
        for item in inputs
        if isinstance(item, dict) and item.get("type") == "image"
    ]
    mentions = [
        item
        for item in inputs
        if isinstance(item, dict) and item.get("type") == "mention"
    ]
    if (
        len(texts) > 1
        or texts and (not isinstance(texts[0], str) or not texts[0])
        or not texts and not images and not mentions
        or len(texts) + len(images) + len(mentions) != len(inputs)
        or any(not isinstance(url, str) or not url.startswith("data:image/") for url in images)
        or any(not isinstance(item.get("name"), str) or not isinstance(item.get("path"), str) for item in mentions)
    ):
        raise DaemonProtocolError("Codex Desktop supports one text prompt with optional images and files")
    links = [
        f'[{item["name"].replace("]", "\\]")}](<{item["path"].replace(">", "%3E")}>)'
        for item in mentions
    ]
    return "\n".join(links + (texts or ["请查看附件。"])), images


def _paste_images_expression(images: list[str]) -> str:
    payload = json.dumps(images, separators=(",", ":"))
    return f"""(async () => {{
      const root = document.querySelector('[data-codex-composer-root]');
      const input = root?.querySelector('[contenteditable="true"], textarea, [role="textbox"]');
      if (!root || !input) return false;
      const before = root.innerHTML;
      const transfer = new DataTransfer();
      const extensions = {{'image/png':'png','image/jpeg':'jpg','image/gif':'gif','image/webp':'webp'}};
      for (const [index, url] of {payload}.entries()) {{
        const match = /^data:(image\\/(?:png|jpeg|gif|webp));base64,(.+)$/.exec(url);
        if (!match) return false;
        const binary = atob(match[2]);
        const bytes = Uint8Array.from(binary, character => character.charCodeAt(0));
        const extension = extensions[match[1]];
        transfer.items.add(new File([bytes], 'image-' + (index + 1) + '.' + extension, {{type: match[1]}}));
      }}
      const event = new ClipboardEvent('paste', {{bubbles: true, cancelable: true}});
      Object.defineProperty(event, 'clipboardData', {{value: transfer}});
      input.dispatchEvent(event);
      const deadline = Date.now() + 3000;
      while (root.innerHTML === before && Date.now() < deadline) {{
        await new Promise(resolve => setTimeout(resolve, 50));
      }}
      return root.innerHTML !== before;
    }})()"""


async def send_codex_desktop_message(
    session_id: str, message_input: Any, *, state_path: Path | None = None
) -> dict[str, Any]:
    if not isinstance(session_id, str) or not session_id or len(session_id) > 256:
        raise DaemonProtocolError("Codex Desktop session identity is invalid")
    text, images = desktop_message_input(message_input)
    current_state_path = state_path or _state_path()
    port, browser_id = await asyncio.to_thread(_read_state, current_state_path)
    try:
        version, targets = await asyncio.gather(
            asyncio.to_thread(_fetch_json, port, "/json/version"),
            asyncio.to_thread(_fetch_json, port, "/json/list"),
        )
    except DaemonProtocolError:
        await asyncio.to_thread(current_state_path.unlink, missing_ok=True)
        raise
    browser_url = _debugger_url(version, port, "browser")
    if not browser_url.endswith("/" + browser_id) or not isinstance(targets, list):
        raise DaemonProtocolError("Codex Desktop debugging identity changed")
    pages = [
        target
        for target in targets
        if isinstance(target, dict)
        and target.get("type") == "page"
        and isinstance(target.get("url"), str)
        and target["url"].startswith("app://")
    ]
    matches: list[dict[str, Any]] = []
    for target in pages:
        url = _debugger_url(target, port, "page")
        async with websockets.connect(url, open_timeout=2, close_timeout=1, max_size=1_048_576) as socket:
            if await _evaluate(socket, 1, _thread_expression(session_id, focus=False)) is True:
                matches.append(target)
    if len(matches) != 1:
        raise DaemonProtocolError("Codex Desktop does not show exactly one matching task")
    current = await asyncio.to_thread(_fetch_json, port, "/json/version")
    if _debugger_url(current, port, "browser") != browser_url:
        raise DaemonProtocolError("Codex Desktop debugging identity changed")
    async with websockets.connect(
        _debugger_url(matches[0], port, "page"), open_timeout=2, close_timeout=1, max_size=1_048_576
    ) as socket:
        if await _evaluate(socket, 1, _thread_expression(session_id, focus=True)) is not True:
            raise DaemonProtocolError("Codex Desktop task is not controllable")
        identifier = 2
        if images:
            if await _evaluate(socket, identifier, _paste_images_expression(images)) is not True:
                raise DaemonProtocolError("Codex Desktop did not accept the images")
            identifier += 1
        await _call(socket, identifier, "Input.insertText", {"text": text})
        identifier += 1
        for event_id, event_type in ((identifier, "rawKeyDown"), (identifier + 1, "keyUp")):
            await _call(
                socket,
                event_id,
                "Input.dispatchKeyEvent",
                {
                    "type": event_type,
                    "key": "Enter",
                    "code": "Enter",
                    "windowsVirtualKeyCode": 13,
                    "nativeVirtualKeyCode": 13,
                },
            )
    return {"accepted": True, "transport": "codex-desktop-cdp"}


async def set_codex_desktop_settings(
    session_id: str,
    updates: dict[str, Any],
    *,
    state_path: Path | None = None,
) -> dict[str, Any]:
    model = updates.get("model")
    model_label = updates.get("modelLabel")
    effort = updates.get("effort")
    effort_index = updates.get("effortIndex")
    if not isinstance(session_id, str) or not session_id or len(session_id) > 256:
        raise DaemonProtocolError("Codex Desktop session identity is invalid")
    if model is not None and (not isinstance(model, str) or not isinstance(model_label, str)):
        raise DaemonProtocolError("Codex Desktop model setting is invalid")
    if effort is not None and (
        not isinstance(effort, str)
        or not isinstance(effort_index, int)
        or isinstance(effort_index, bool)
        or not 0 <= effort_index <= 8
    ):
        raise DaemonProtocolError("Codex Desktop reasoning setting is invalid")
    if model is None and effort is None:
        raise DaemonProtocolError("Codex Desktop settings are empty")

    port, browser_id = await asyncio.to_thread(_read_state, state_path or _state_path())
    version, targets = await asyncio.gather(
        asyncio.to_thread(_fetch_json, port, "/json/version"),
        asyncio.to_thread(_fetch_json, port, "/json/list"),
    )
    browser_url = _debugger_url(version, port, "browser")
    if not browser_url.endswith("/" + browser_id) or not isinstance(targets, list):
        raise DaemonProtocolError("Codex Desktop debugging identity changed")
    matches: list[dict[str, Any]] = []
    for target in targets:
        if not (
            isinstance(target, dict)
            and target.get("type") == "page"
            and isinstance(target.get("url"), str)
            and target["url"].startswith("app://")
        ):
            continue
        async with websockets.connect(
            _debugger_url(target, port, "page"), open_timeout=2, close_timeout=1, max_size=1_048_576
        ) as socket:
            if await _evaluate(socket, 1, _thread_expression(session_id, focus=False)) is True:
                matches.append(target)
    if len(matches) != 1:
        raise DaemonProtocolError("Codex Desktop does not show exactly one matching task")

    async with websockets.connect(
        _debugger_url(matches[0], port, "page"), open_timeout=2, close_timeout=1, max_size=1_048_576
    ) as socket:
        if await _evaluate(socket, 1, _thread_expression(session_id, focus=True)) is not True:
            raise DaemonProtocolError("Codex Desktop task is not controllable")
        identifier = 2
        if model is not None:
            selected = await _evaluate(
                socket,
                identifier,
                f"""(async () => {{
                  const wanted = {json.dumps([model, model_label])};
                  const norm = value => value.toLowerCase().replace(/[^a-z0-9]/g, '');
                  const trigger = document.querySelector('[data-codex-intelligence-trigger="true"]');
                  if (!trigger) return false;
                  await new Promise(resolve => setTimeout(resolve, 200));
                  trigger.click();
                  let deadline = Date.now() + 3000;
                  let item;
                  while (Date.now() < deadline) {{
                    item = [...document.querySelectorAll('[role="menuitemradio"]')].find(row =>
                      wanted.some(value => norm(row.textContent || '') === norm(value))
                    );
                    if (item) break;
                    await new Promise(resolve => setTimeout(resolve, 50));
                  }}
                  if (!item) return false;
                  item.click();
                  await new Promise(resolve => setTimeout(resolve, 150));
                  return wanted.some(value => norm(trigger.textContent || '').includes(norm(value)));
                }})()""",
            )
            identifier += 1
            if selected is not True:
                raise DaemonProtocolError("Codex Desktop did not confirm the model setting")
        if effort is not None:
            position = await _evaluate(
                socket,
                identifier,
                f"""(async () => {{
                  const trigger = document.querySelector('[data-codex-intelligence-trigger="true"]');
                  if (!trigger) return null;
                  const visible = element => Boolean(element?.getClientRects().length);
                  await new Promise(resolve => setTimeout(resolve, 200));
                  if (![...document.querySelectorAll('[data-reasoning-slider]')].some(visible)) trigger.click();
                  const deadline = Date.now() + 3000;
                  let slider, track;
                  while (Date.now() < deadline) {{
                    slider = [...document.querySelectorAll('[data-reasoning-slider]')].find(visible);
                    track = slider?.querySelector('[data-orientation="horizontal"] [class*="Track"]');
                    if (track) break;
                    await new Promise(resolve => setTimeout(resolve, 50));
                  }}
                  if (!track) return null;
                  const bounds = track.getBoundingClientRect();
                  const maximum = Number(slider.querySelector('[role="slider"]')?.getAttribute('aria-valuemax'));
                  if (!Number.isFinite(maximum) || maximum < {effort_index}) return null;
                  return {{
                    x: bounds.left + 13 + (bounds.width - 26) * {effort_index} / maximum,
                    y: bounds.top + bounds.height / 2,
                  }};
                }})()""",
            )
            identifier += 1
            if not (
                isinstance(position, dict)
                and isinstance(position.get("x"), (int, float))
                and isinstance(position.get("y"), (int, float))
            ):
                raise DaemonProtocolError("Codex Desktop reasoning control is unavailable")
            for event_type in ("mousePressed", "mouseReleased"):
                await _call(
                    socket,
                    identifier,
                    "Input.dispatchMouseEvent",
                    {
                        "type": event_type,
                        "x": position["x"],
                        "y": position["y"],
                        "button": "left",
                        "clickCount": 1,
                    },
                )
                identifier += 1
            confirmed = await _evaluate(
                socket,
                identifier,
                f"""(async () => {{
                  const deadline = Date.now() + 3000;
                  while (Date.now() < deadline) {{
                    if (document.querySelector('[data-codex-intelligence-trigger="true"]')
                      ?.getAttribute('data-selected-reasoning-effort') === {json.dumps(effort)}) return true;
                    await new Promise(resolve => setTimeout(resolve, 50));
                  }}
                  return false;
                }})()""",
            )
            if confirmed is not True:
                raise DaemonProtocolError("Codex Desktop did not confirm the reasoning setting")
    return {"accepted": True, "transport": "codex-desktop-cdp", **updates}
