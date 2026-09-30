from __future__ import annotations

import os
from typing import Any
from urllib.parse import urlparse

from sqlalchemy.dialects.sqlite import insert

from astrorder.models import WorkspacePreferenceRow

PREFERENCE_KEY = "analytics:llm_gateway"
LEGACY_USAGE_KEY = "analytics:ocx_usage_url"

SUPPORTED_GATEWAY_TYPES = {"opencodex", "magpie"}

DEFAULT_CONFIGS: dict[str, dict[str, Any]] = {
    "opencodex": {
        "gateway_type": "opencodex",
        "management_url": "https://ocx.651971564.xyz",
        "inference_url": "https://llm.651971564.xyz/v1",
        "api_key": "",
        "target_overrides": {},
    },
    "magpie": {
        "gateway_type": "magpie",
        "management_url": "http://192.168.1.11:3425/v1",
        "inference_url": "http://192.168.1.11:3425/v1",
        "api_key": "",
        "target_overrides": {},
    },
}

DEFAULT_CONFIG = DEFAULT_CONFIGS["opencodex"]


def _url(value: Any, label: str) -> str:
    result = str(value or "").strip().rstrip("/")
    parsed = urlparse(result)
    if parsed.scheme not in {"http", "https"} or not parsed.netloc:
        raise ValueError(f"{label}必须是完整的 http 或 https 地址")
    return result


def _clean_profiles(raw: Any) -> dict[str, dict[str, Any]]:
    profiles: dict[str, dict[str, Any]] = {}
    if isinstance(raw, dict):
        for gt, prof in raw.items():
            if gt in SUPPORTED_GATEWAY_TYPES and isinstance(prof, dict):
                p_copy = dict(prof)
                try:
                    p_copy["management_url"] = _url(p_copy.get("management_url"), "管理地址")
                    p_copy["inference_url"] = _url(p_copy.get("inference_url"), "推理 Base URL")
                    p_copy["api_key"] = str(p_copy.get("api_key") or "").strip()
                    overrides = p_copy.get("target_overrides") or {}
                    if isinstance(overrides, dict):
                        p_copy["target_overrides"] = {k: _url(v, "目标推理地址") for k, v in overrides.items() if isinstance(k, str) and k}
                    else:
                        p_copy["target_overrides"] = {}
                    p_copy["gateway_type"] = gt
                    profiles[gt] = p_copy
                except ValueError:
                    continue
    return profiles


def validate_gateway_config(value: dict[str, Any]) -> dict[str, Any]:
    gw_type = str(value.get("gateway_type") or "opencodex").strip().lower()
    if gw_type not in SUPPORTED_GATEWAY_TYPES:
        raise ValueError("暂不支持此 LLM 网关类型")
    overrides = value.get("target_overrides") or {}
    if not isinstance(overrides, dict) or any(not isinstance(key, str) or not key for key in overrides):
        raise ValueError("目标地址覆盖格式无效")

    validated = {
        "gateway_type": gw_type,
        "management_url": _url(value.get("management_url"), "管理地址"),
        "inference_url": _url(value.get("inference_url"), "推理 Base URL"),
        "api_key": str(value.get("api_key") or "").strip(),
        "target_overrides": {key: _url(url, "目标推理地址") for key, url in overrides.items()},
    }
    profiles = _clean_profiles(value.get("gateway_profiles"))
    profiles[gw_type] = dict(validated)
    validated["gateway_profiles"] = profiles
    return validated


def get_gateway_config(store: Any) -> dict[str, Any]:
    with store.session() as db:
        row = db.get(WorkspacePreferenceRow, PREFERENCE_KEY)
        if row and isinstance(row.value, dict):
            saved = dict(row.value)
            gw_type = str(saved.get("gateway_type") or "opencodex").strip().lower()
            default = DEFAULT_CONFIGS.get(gw_type, DEFAULT_CONFIG)
            legacy = "management_url" not in saved
            if legacy and saved.get("base_url"):
                saved["management_url"] = saved["base_url"]
            if legacy and not saved.get("api_key"):
                saved["api_key"] = os.environ.get("OPENCODEX_API_AUTH_TOKEN", "")
            return validate_gateway_config({**default, **saved})
        legacy = db.get(WorkspacePreferenceRow, LEGACY_USAGE_KEY)
    if legacy and legacy.value:
        parsed = urlparse(str(legacy.value).strip())
        path = parsed.path.removesuffix("/api/usage").rstrip("/")
        management = parsed._replace(path=path, params="", query="", fragment="").geturl().rstrip("/")
        cfg = {**DEFAULT_CONFIG, "management_url": management}
        cfg["gateway_profiles"] = {"opencodex": dict(cfg)}
        return cfg
    cfg = {**DEFAULT_CONFIG, "api_key": os.environ.get("OPENCODEX_API_AUTH_TOKEN", "")}
    cfg["gateway_profiles"] = {"opencodex": dict(cfg)}
    return cfg


def save_gateway_config(store: Any, value: dict[str, Any]) -> dict[str, Any]:
    current = get_gateway_config(store)
    merged_profiles = dict(current.get("gateway_profiles") or {})
    passed_profiles = _clean_profiles(value.get("gateway_profiles"))
    merged_profiles.update(passed_profiles)

    payload = dict(value)
    payload["gateway_profiles"] = merged_profiles
    config = validate_gateway_config(payload)
    with store.session() as db:
        statement = insert(WorkspacePreferenceRow).values(key=PREFERENCE_KEY, value=config)
        db.execute(statement.on_conflict_do_update(index_elements=["key"], set_={"value": config}))
    return config


def _mask(key: str) -> str:
    return f"{key[:6]}...{key[-4:]}" if len(key) > 12 else ("已配置" if key else "")


def public_gateway_config(config: dict[str, Any]) -> dict[str, Any]:
    key = config["api_key"]
    profiles_public: dict[str, dict[str, Any]] = {}
    raw_profiles = config.get("gateway_profiles") or {}
    for gt, prof in raw_profiles.items():
        k = str(prof.get("api_key") or "")
        profiles_public[gt] = {
            "gateway_type": gt,
            "management_url": prof.get("management_url", ""),
            "inference_url": prof.get("inference_url", ""),
            "target_overrides": prof.get("target_overrides", {}),
            "masked_key": _mask(k),
        }

    return {
        **{name: config[name] for name in ("gateway_type", "management_url", "inference_url", "target_overrides")},
        "masked_key": _mask(key),
        "gateway_profiles": profiles_public,
    }
