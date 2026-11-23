"""Run an upstream browser agent in one Astrorder-owned browser session."""

import asyncio
import contextlib
import json
import os
import sys
import threading
import urllib.error
import urllib.parse

config = json.loads(sys.stdin.readline())


def normalize_base_url(base_url):
    trimmed = (base_url or "").strip().rstrip("/")
    parsed = urllib.parse.urlparse(trimmed)
    if parsed.hostname and parsed.hostname.lower() == "generativelanguage.googleapis.com":
        path = parsed.path.rstrip("/")
        if path == "":
            parsed = parsed._replace(path="/v1beta/openai")
            return urllib.parse.urlunparse(parsed).rstrip("/")
        if path in {"/v1", "/v1beta"}:
            parsed = parsed._replace(path=f"{path}/openai")
            return urllib.parse.urlunparse(parsed).rstrip("/")
    return trimmed


def apply_reasoning_body(url, body, reasoning):
    for name in ("reasoning", "reasoning_effort", "thinking"):
        body.pop(name, None)
    effort = (reasoning or "").strip()
    if not effort:
        return
    host = (urllib.parse.urlparse(str(url)).hostname or "").lower()
    if host == "api.deepseek.com":
        body["thinking"] = {"type": "disabled" if effort == "none" else "enabled"}
        if effort != "none":
            body["reasoning_effort"] = effort
    elif host == "openrouter.ai" or host.endswith(".openrouter.ai"):
        if effort == "none":
            body["reasoning"] = {"enabled": False}
        else:
            body["reasoning"] = {"effort": effort}
    else:
        body["reasoning_effort"] = effort


def strip_reasoning_fields(body):
    removed = False
    for name in ("reasoning", "reasoning_effort", "thinking"):
        if name in body:
            body.pop(name, None)
            removed = True
    return removed


def is_reasoning_rejection(status_code, text):
    if status_code not in (400, 422):
        return False
    lower = (text or "").lower()
    return any(
        needle in lower
        for needle in (
            "reasoning_effort",
            '"reasoning"',
            "'reasoning'",
            "thinking",
            "unknown name",
            "cannot find field",
            "unrecognized request argument",
            "unsupported parameter",
            "extra_forbidden",
            "extra inputs are not permitted",
        )
    )


def watch_parent():
    if not sys.stdin.read(1):
        os._exit(1)


threading.Thread(target=watch_parent, daemon=True).start()
os.environ["ANONYMIZED_TELEMETRY"] = "false"
os.environ["BROWSER_USE_LOGGING_LEVEL"] = "error"


async def browser_use():
    import httpx
    from browser_use import Agent
    from browser_use.browser import BrowserProfile, BrowserSession
    from browser_use.llm import ChatOpenAI

    llm_config = config["llm"]
    base_url = normalize_base_url(llm_config["base_url"])

    class CompatTransport(httpx.AsyncHTTPTransport):
        def __init__(self):
            super().__init__()
            self.drop_reasoning = False
            self.use_max_completion_tokens = False

        def _rewrite(self, request, body):
            content = json.dumps(body, ensure_ascii=False).encode("utf-8")
            headers = request.headers.copy()
            headers["Content-Length"] = str(len(content))
            return httpx.Request(
                method=request.method,
                url=request.url,
                headers=headers,
                content=content,
                extensions=request.extensions,
            )

        async def handle_async_request(self, request):
            if request.method != "POST" or not request.url.path.endswith("/chat/completions"):
                return await super().handle_async_request(request)
            try:
                raw = await request.aread()
                body = json.loads(raw)
            except Exception:
                return await super().handle_async_request(request)

            if self.drop_reasoning:
                strip_reasoning_fields(body)
            else:
                apply_reasoning_body(request.url, body, llm_config["reasoning"])
            if self.use_max_completion_tokens and "max_tokens" in body:
                body["max_completion_tokens"] = body.pop("max_tokens")

            response = await super().handle_async_request(self._rewrite(request, body))
            if response.status_code not in (400, 422):
                return response

            err_bytes = await response.aread()
            err_text = err_bytes.decode("utf-8", errors="ignore")
            changed = False
            if is_reasoning_rejection(response.status_code, err_text) and strip_reasoning_fields(body):
                self.drop_reasoning = True
                changed = True
            if (
                "max_tokens" in body
                and "max_tokens" in err_text.lower()
                and "max_completion_tokens" in err_text.lower()
            ):
                body["max_completion_tokens"] = body.pop("max_tokens")
                self.use_max_completion_tokens = True
                changed = True
            if not changed:
                return response
            await response.aclose()
            return await super().handle_async_request(self._rewrite(request, body))

    task = config["goal"]
    secrets = {}
    for index, (label, value) in enumerate(config["inputs"].items(), start=1):
        if value == "":
            task += f'\nClear the field labeled {json.dumps(label)} using the input action with text set to "" and clear set to true.'
            continue
        name = f"astrorder_field_{index}"
        secrets[name] = value
        task += f'\nFor the field labeled {json.dumps(label)}, use <secret>{name}</secret>.'
    session = BrowserSession(browser_profile=BrowserProfile(
        cdp_url=config["cdp_url"], is_local=True, keep_alive=True,
    ))

    def action_count(agent):
        return sum(len(item.model_output.action) for item in agent.history.history if item.model_output)

    async def limit_actions(agent):
        agent.settings.max_actions_per_step = min(5, config["max_steps"] - action_count(agent))

    async def stop_at_limit(agent):
        if action_count(agent) >= config["max_steps"] and not agent.history.is_done():
            agent.stop()

    async with httpx.AsyncClient(transport=CompatTransport(), follow_redirects=True) as http:
        llm = ChatOpenAI(
            model=llm_config["model"],
            api_key=llm_config["api_key"],
            base_url=base_url,
            http_client=http,
            reasoning_effort=None if llm_config["reasoning"] == "none" else llm_config["reasoning"],
            temperature=None,
            frequency_penalty=None,
            add_schema_to_system_prompt=True,
        )
        agent = Agent(
            task=task,
            llm=llm,
            browser_session=session,
            sensitive_data=secrets or None,
            directly_open_url=False,
            enable_signal_handler=False,
            use_judge=False,
            use_vision="auto",
            max_actions_per_step=min(5, config["max_steps"]),
        )
        history = await agent.run(max_steps=config["max_steps"], on_step_start=limit_actions, on_step_end=stop_at_limit)
    success = history.is_successful() is True
    errors = history.errors()
    if not success and errors and errors[-1] and errors[-1] != "Failed to complete task in maximum steps":
        raise RuntimeError(errors[-1])
    status = "done" if success else "blocked" if history.is_done() else "step_limit"
    steps = []
    for index, item in enumerate(history.history, start=1):
        output = item.model_output
        actions = [action.model_dump(mode="json", exclude_none=True) for action in output.action] if output else []
        steps.append({"step": index, "actions": actions})
    return {
        "ok": success,
        "status": status,
        "target_id": session.agent_focus_target_id,
        "steps": steps,
        "answer": history.final_result() or "",
    }


def jev_ultrafast():
    from websockets.sync.client import connect
    from jev_ultrafast import agent as upstream_agent
    from jev_ultrafast import browser as upstream_browser
    from jev_ultrafast import model as upstream_model

    target = config["target_id"]
    cdp = connect(config["page_ws"], open_timeout=10, max_size=64 * 1024 * 1024)
    sequence = 0

    def call(method, session_id=None, **params):
        nonlocal sequence
        if method == "Target.createTarget":
            return {"targetId": target}
        if method == "Target.attachToTarget":
            if params.get("targetId") != target:
                raise ValueError("浏览器标签不属于当前会话")
            return {"sessionId": target}
        if method == "Target.closeTarget":
            if params.get("targetId") != target:
                raise ValueError("浏览器标签不属于当前会话")
            return {"success": True}
        if session_id != target:
            raise ValueError("浏览器会话不可用")
        if method in {"Emulation.setDeviceMetricsOverride", "Page.navigate"}:
            return {}
        sequence += 1
        request_id = sequence
        cdp.send(json.dumps({"id": request_id, "method": method, "params": params}))
        while True:
            message = json.loads(cdp.recv())
            if message.get("id") != request_id:
                continue
            if "error" in message:
                raise RuntimeError(message["error"].get("message", "浏览器操作失败"))
            return message.get("result", {})

    original_post = upstream_model.post_json
    drop_reasoning = False

    def post(url, key, body):
        nonlocal drop_reasoning
        if url == "https://api.typesafe.ai/v1/systemone":
            url = config["jev"]["url"]
            key = config["jev"]["api_key"]
            body["model"] = config["jev"]["model"]
        elif url.endswith("/chat/completions"):
            if drop_reasoning:
                strip_reasoning_fields(body)
            else:
                apply_reasoning_body(url, body, config["llm"]["reasoning"])
            try:
                return original_post(url, key, body)
            except Exception as exc:
                status_code = getattr(exc, "code", None)
                err_text = str(exc)
                if isinstance(exc, urllib.error.HTTPError):
                    try:
                        err_text = exc.read().decode("utf-8", errors="ignore")
                    except Exception:
                        pass
                elif status_code is None:
                    resp = getattr(exc, "response", None)
                    status_code = getattr(resp, "status_code", None)
                    if resp is not None and getattr(resp, "text", None):
                        err_text = resp.text
                if (status_code in (400, 422) or "400" in err_text) and is_reasoning_rejection(status_code or 400, err_text):
                    if strip_reasoning_fields(body):
                        drop_reasoning = True
                        return original_post(url, key, body)
                raise
        return original_post(url, key, body)

    original_field_text = upstream_agent.field_text

    def field_text(context):
        label = "".join(c.casefold() for c in context["field"]["label"] if c.isalnum())
        matches = [
            (len(clean), value)
            for name, value in config["inputs"].items()
            if (clean := "".join(c.casefold() for c in name if c.isalnum()))
            and label and (clean in label or label in clean)
        ]
        if matches:
            best = max(length for length, _ in matches)
            values = {value for length, value in matches if length == best}
            if len(values) != 1:
                raise ValueError("输入项匹配到多个不同的值")
            return values.pop(), {"model": "astrorder-inputs", "latency_ms": 0, "usage": {}}
        return original_field_text(context)

    upstream_browser.cdp = call
    upstream_browser.ensure_daemon = lambda: None
    upstream_model.post_json = post
    upstream_agent.field_text = field_text
    os.environ["TYPESAFE_API_KEY"] = config["jev"]["api_key"]
    os.environ["TYPESAFE_MODEL"] = config["jev"]["model"]
    os.environ["TEXT_MODEL_API_KEY"] = config["llm"]["api_key"]
    os.environ["TEXT_MODEL_BASE_URL"] = normalize_base_url(config["llm"]["base_url"])
    os.environ["TEXT_MODEL"] = config["llm"]["model"]
    os.environ["TEXT_MODEL_REASONING"] = config["llm"]["reasoning"]
    agent = None
    try:
        agent = upstream_agent.Agent(config["url"], config["goal"])
        state = agent.snapshot()
        for _ in range(config["max_steps"]):
            if state["status"] in {"done", "blocked"}:
                break
            state = agent.command("tick")
        status = state["status"]
        if status not in {"done", "blocked"}:
            status = "step_limit"
        return {
            "ok": status == "done",
            "status": status,
            "target_id": target,
            "steps": state["history"],
            "answer": "",
        }
    finally:
        if agent:
            agent.close()
        cdp.close()


try:
    with contextlib.redirect_stdout(sys.stderr):
        result = asyncio.run(browser_use()) if config["engine"] == "browser_use" else jev_ultrafast()
    print(json.dumps(result, ensure_ascii=False))
except Exception as exc:
    print(json.dumps({"error": str(exc)}, ensure_ascii=False))
    sys.exit(1)
