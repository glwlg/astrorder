from __future__ import annotations

import os
from typing import Any
from urllib.parse import urlparse

from sqlalchemy.dialects.sqlite import insert

from astrorder.models import WorkspacePreferenceRow

PREFERENCE_KEY = "analytics:llm_gateway"
LEGACY_USAGE_KEY = "analytics:ocx_usage_url"
DEFAULT_CONFIG = {
    "gateway_type": "opencodex",
    "management_url": "https://ocx.651971564.xyz",
    "inference_url": "https://llm.651971564.xyz/v1",
    "api_key": "",
    "target_overrides": {},
}


def _url(value: Any, label: str) -> str:
    result = str(value or "").strip().rstrip("/")
    parsed = urlparse(result)
    if parsed.scheme not in {"http", "https"} or not parsed.netloc:
        raise ValueError(f"{label}必须是完整的 http 或 https 地址")
    return result


def validate_gateway_config(value: dict[str, Any]) -> dict[str, Any]:
    if str(value.get("gateway_type") or "") != "opencodex":
        raise ValueError("暂不支持此 LLM 网关类型")
    overrides = value.get("target_overrides") or {}
    if not isinstance(overrides, dict) or any(not isinstance(key, str) or not key for key in overrides):
        raise ValueError("目标地址覆盖格式无效")
    return {
        "gateway_type": "opencodex",
        "management_url": _url(value.get("management_url"), "管理地址"),
        "inference_url": _url(value.get("inference_url"), "推理 Base URL"),
        "api_key": str(value.get("api_key") or "").strip(),
        "target_overrides": {key: _url(url, "目标推理地址") for key, url in overrides.items()},
    }


def get_gateway_config(store: Any) -> dict[str, Any]:
    with store.session() as db:
        row = db.get(WorkspacePreferenceRow, PREFERENCE_KEY)
        if row and isinstance(row.value, dict):
            saved = dict(row.value)
            # Existing installations used base_url for the management API.
            legacy = "management_url" not in saved
            if legacy and saved.get("base_url"):
                saved["management_url"] = saved["base_url"]
            if legacy and not saved.get("api_key"):
                saved["api_key"] = os.environ.get("OPENCODEX_API_AUTH_TOKEN", "")
            return validate_gateway_config({**DEFAULT_CONFIG, **saved})
        legacy = db.get(WorkspacePreferenceRow, LEGACY_USAGE_KEY)
    if legacy and legacy.value:
        parsed = urlparse(str(legacy.value).strip())
        path = parsed.path.removesuffix("/api/usage").rstrip("/")
        management = parsed._replace(path=path, params="", query="", fragment="").geturl().rstrip("/")
        return {**DEFAULT_CONFIG, "management_url": management}
    return {**DEFAULT_CONFIG, "api_key": os.environ.get("OPENCODEX_API_AUTH_TOKEN", "")}


def save_gateway_config(store: Any, value: dict[str, Any]) -> dict[str, Any]:
    config = validate_gateway_config(value)
    with store.session() as db:
        statement = insert(WorkspacePreferenceRow).values(key=PREFERENCE_KEY, value=config)
        db.execute(statement.on_conflict_do_update(index_elements=["key"], set_={"value": config}))
    return config


def public_gateway_config(config: dict[str, Any]) -> dict[str, Any]:
    key = config["api_key"]
    return {
        **{name: config[name] for name in ("gateway_type", "management_url", "inference_url", "target_overrides")},
        "masked_key": f"{key[:6]}...{key[-4:]}" if len(key) > 12 else ("已配置" if key else ""),
    }
