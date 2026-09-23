from __future__ import annotations

from typing import Any
from urllib.parse import urlparse

from sqlalchemy.dialects.sqlite import insert

from .models import WorkspacePreferenceRow

PREFERENCE_KEY = "services:llm"
DEFAULTS = {
    "base_url": "https://api.deepseek.com/v1",
    "api_key": "",
    "model": "deepseek-chat",
    "reasoning": "low",
}
REASONING_LEVELS = {"none", "low", "medium", "high"}


def reasoning_payload(base_url: str, reasoning: str) -> dict[str, Any]:
    if "api.deepseek.com/" in base_url.rstrip("/") + "/":
        return {"thinking": {"type": "disabled" if reasoning == "none" else "enabled"}}
    if reasoning == "none":
        return {"reasoning": {"enabled": False}}
    return {"reasoning": {"effort": reasoning}}


def get_llm_config(store) -> dict[str, str]:
    with store.session() as db:
        row = db.get(WorkspacePreferenceRow, PREFERENCE_KEY)
        saved = row.value if row and isinstance(row.value, dict) else {}
    return {key: str(saved.get(key) or value).strip() for key, value in DEFAULTS.items()}


def set_llm_config(store, config: dict[str, Any]) -> dict[str, str]:
    value = {key: str(config.get(key, "")).strip() for key in DEFAULTS}
    parsed = urlparse(value["base_url"])
    if parsed.scheme not in {"http", "https"} or not parsed.netloc:
        raise ValueError("API URL 必须是完整的 http 或 https 地址")
    value["base_url"] = value["base_url"].rstrip("/")
    if not value["model"]:
        raise ValueError("模型不能为空")
    if value["reasoning"] not in REASONING_LEVELS:
        raise ValueError("思考程度无效")
    with store.session() as db:
        statement = insert(WorkspacePreferenceRow).values(key=PREFERENCE_KEY, value=value)
        db.execute(statement.on_conflict_do_update(index_elements=["key"], set_={"value": value}))
    return value
