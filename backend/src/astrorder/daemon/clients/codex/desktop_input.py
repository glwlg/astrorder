"""App-side input formatting for requests delegated to Go Desktop control."""
from typing import Any

from astrorder.daemon.bridge import DaemonBridgeError


def desktop_message_input(value: Any) -> tuple[str, list[str]]:
    inputs = [{"type": "text", "text": value}] if isinstance(value, str) else value
    if not isinstance(inputs, list):
        raise DaemonBridgeError("Codex Desktop message is invalid")
    texts = [item.get("text") for item in inputs if isinstance(item, dict) and item.get("type") == "text"]
    images = [item.get("url") for item in inputs if isinstance(item, dict) and item.get("type") == "image"]
    mentions = [item for item in inputs if isinstance(item, dict) and item.get("type") == "mention"]
    if (
        len(texts) > 1
        or texts and (not isinstance(texts[0], str) or not texts[0])
        or not texts and not images and not mentions
        or len(texts) + len(images) + len(mentions) != len(inputs)
        or any(not isinstance(url, str) or not url.startswith("data:image/") for url in images)
        or any(not isinstance(item.get("name"), str) or not isinstance(item.get("path"), str) for item in mentions)
    ):
        raise DaemonBridgeError("Codex Desktop supports one text prompt with optional images and files")
    links = [
        f'[{item["name"].replace("]", "\\]")}](<{item["path"].replace(">", "%3E")}>)'
        for item in mentions
    ]
    return "\n".join(links + (texts or ["请查看附件。"])), images
