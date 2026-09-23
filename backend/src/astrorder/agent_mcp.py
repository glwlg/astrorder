from __future__ import annotations

import json
import re
import sys
from collections.abc import Callable
from pathlib import Path
from typing import Any

from .agent_cli import invoke_remote
from .agent_gateway import CAPABILITIES, AgentApiError

Invoker = Callable[[str, dict[str, Any]], dict[str, Any]]


def tool_name(capability_id: str) -> str:
    return capability_id.replace(".", "_")


def capability_id(name: str) -> str:
    for item in CAPABILITIES:
        if tool_name(item["id"]) == name:
            return item["id"]
    return name.replace("_", ".")


def _infer_schema_property(desc: str) -> dict[str, Any]:
    text = desc.lower()
    prop: dict[str, Any] = {"description": desc}
    # 优先识别显式的 string/text 或者包含 (string, object, array, or number) 的通用 payload
    if "string, object, array, or number" in text or "data payload" in text:
        return prop  # 开放类型，允许任意 JSON 数据结构
    if re.search(r"\b(boolean|bool)\b", text):
        prop["type"] = "boolean"
    elif re.search(r"\b(integer|int|percentage)\b", text):
        prop["type"] = "integer"
    elif re.search(r"\b(number|float)\b", text):
        prop["type"] = "number"
    elif re.search(r"\b(list|array)\b", text):
        prop["type"] = "array"
    elif re.search(r"\b(object|dict|key-value)\b", text):
        prop["type"] = "object"
    else:
        prop["type"] = "string"
    return prop


def build_input_schema(raw_input: Any) -> dict[str, Any]:
    if not isinstance(raw_input, dict) or not raw_input:
        return {"type": "object", "properties": {}}

    properties: dict[str, Any] = {}
    required: list[str] = []

    for key, desc in raw_input.items():
        desc_str = str(desc)
        properties[key] = _infer_schema_property(desc_str)
        is_optional = any(
            opt in desc_str.lower()
            for opt in ("optional", "or omit", "defaults to", "default:", "default automatically")
        )
        if not is_optional:
            required.append(key)

    schema: dict[str, Any] = {
        "type": "object",
        "properties": properties,
    }
    if required:
        schema["required"] = required
    return schema


def tools() -> list[dict[str, Any]]:
    return [
        {
            "name": tool_name(item["id"]),
            "description": item["summary"],
            "inputSchema": build_input_schema(item.get("input")),
        }
        for item in CAPABILITIES
    ]


def handle_rpc(message: dict[str, Any], invoker: Invoker) -> dict[str, Any] | None:
    method = message.get("method")
    ident = message.get("id")
    if method == "initialize":
        return {
            "jsonrpc": "2.0",
            "id": ident,
            "result": {
                "protocolVersion": "2024-11-05",
                "capabilities": {"tools": {}},
                "serverInfo": {"name": "astrorder", "version": "0.1.0"},
            },
        }
    if method in {"notifications/initialized", "initialized", "notifications/cancelled"}:
        return None
    if method == "tools/list":
        return {"jsonrpc": "2.0", "id": ident, "result": {"tools": tools()}}
    if method == "ping":
        return {"jsonrpc": "2.0", "id": ident, "result": {}}
    if method == "tools/call":
        params = message.get("params") or {}
        name = str(params.get("name") or "")
        arguments = params.get("arguments") if isinstance(params.get("arguments"), dict) else {}
        try:
            data = invoker(capability_id(name), arguments)
            payload = dict(data)
            screenshot = payload.pop("screenshot", None)
            mime_type = payload.pop("mime_type", "image/jpeg")
            content = [{"type": "text", "text": json.dumps(payload, ensure_ascii=False)}]
            if isinstance(screenshot, str) and screenshot:
                content.append({"type": "image", "data": screenshot, "mimeType": mime_type})
            return {
                "jsonrpc": "2.0",
                "id": ident,
                "result": {"content": content},
            }
        except AgentApiError as exc:
            return {"jsonrpc": "2.0", "id": ident, "error": {"code": -32000, "message": exc.message}}
        except SystemExit as exc:
            return {"jsonrpc": "2.0", "id": ident, "error": {"code": -32000, "message": str(exc)}}
    if ident is None:
        return None
    return {"jsonrpc": "2.0", "id": ident, "error": {"code": -32601, "message": f"Unknown method {method}"}}


def ensure_url_mcp_yaml(config_path: Path, url: str, token: str) -> None:
    if not token or not config_path.is_file():
        return
    text = config_path.read_text(encoding="utf-8")
    if "api/v1/agent/mcp" in text and re.search(r"^  astrorder:", text, re.MULTILINE):
        return
    block = f"  astrorder:\n    url: {url}\n    headers:\n      Authorization: Bearer ***"
    if re.search(r"^mcp_servers:\s*$", text, re.MULTILINE):
        text = re.sub(r"^mcp_servers:\s*$", "mcp_servers:\n" + block.rstrip(), text, count=1, flags=re.MULTILINE)
        if not text.endswith("\n"):
            text += "\n"
    else:
        text = text.rstrip() + "\nmcp_servers:\n" + block
    config_path.write_text(text, encoding="utf-8")


def main() -> None:
    if hasattr(sys.stdout, "reconfigure"):
        sys.stdout.reconfigure(encoding="utf-8")
    if hasattr(sys.stdin, "reconfigure"):
        sys.stdin.reconfigure(encoding="utf-8")
    for raw in sys.stdin:
        line = raw.strip()
        if not line:
            continue
        try:
            message = json.loads(line)
        except json.JSONDecodeError:
            continue
        if not isinstance(message, dict):
            continue
        reply = handle_rpc(message, invoke_remote)
        if reply is not None:
            sys.stdout.write(json.dumps(reply, ensure_ascii=False))
            sys.stdout.write(chr(10))
            sys.stdout.flush()


if __name__ == "__main__":
    main()
