"""Run an upstream browser agent in one Astrorder-owned browser session."""

import asyncio
import contextlib
import json
import os
import sys
import threading

config = json.loads(sys.stdin.readline())


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

    async def apply_reasoning(request):
        if request.method != "POST" or not request.url.path.endswith("/chat/completions"):
            return
        if request.url.host == "api.openai.com":
            return
        body = json.loads(request.content)
        body.pop("reasoning_effort", None)
        reasoning = llm_config["reasoning"]
        if request.url.host == "api.deepseek.com":
            body["thinking"] = {"type": "disabled" if reasoning == "none" else "enabled"}
            if reasoning != "none":
                body["reasoning_effort"] = reasoning
        elif reasoning == "none":
            body["reasoning"] = {"enabled": False}
        else:
            body["reasoning"] = {"effort": reasoning}
        content = json.dumps(body, ensure_ascii=False).encode("utf-8")
        request.headers["Content-Length"] = str(len(content))
        request.stream = httpx.ByteStream(content)

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

    async with httpx.AsyncClient(event_hooks={"request": [apply_reasoning]}, follow_redirects=True) as http:
        llm = ChatOpenAI(
            model=llm_config["model"],
            api_key=llm_config["api_key"],
            base_url=llm_config["base_url"],
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

    def post(url, key, body):
        if url == "https://api.typesafe.ai/v1/systemone":
            url = config["jev"]["url"]
            key = config["jev"]["api_key"]
            body["model"] = config["jev"]["model"]
        elif url.endswith("/chat/completions"):
            for name in ("reasoning", "reasoning_effort", "thinking"):
                body.pop(name, None)
            reasoning = config["llm"]["reasoning"]
            if "api.deepseek.com/" in url:
                body["thinking"] = {"type": "disabled" if reasoning == "none" else "enabled"}
            elif reasoning == "none":
                body["reasoning"] = {"enabled": False}
            else:
                body["reasoning"] = {"effort": reasoning}
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
    os.environ["TEXT_MODEL_BASE_URL"] = config["llm"]["base_url"]
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
