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


def _clean_profiles(raw: Any, existing_profiles: dict[str, dict[str, Any]] | None = None) -> dict[str, dict[str, Any]]:
    existing = existing_profiles or {}
    profiles: dict[str, dict[str, Any]] = {}
    for gt in SUPPORTED_GATEWAY_TYPES:
        if gt in existing and isinstance(existing[gt], dict):
            profiles[gt] = dict(existing[gt])
        else:
            profiles[gt] = dict(DEFAULT_CONFIGS.get(gt, DEFAULT_CONFIG))

    if isinstance(raw, dict):
        for gt, prof in raw.items():
            if gt in SUPPORTED_GATEWAY_TYPES and isinstance(prof, dict):
                p_copy = dict(profiles.get(gt) or DEFAULT_CONFIGS.get(gt, DEFAULT_CONFIG))
                try:
                    if prof.get("management_url"):
                        p_copy["management_url"] = _url(prof.get("management_url"), "管理地址")
                    if prof.get("inference_url"):
                        p_copy["inference_url"] = _url(prof.get("inference_url"), "推理 Base URL")
                    if prof.get("clear_key") is True:
                        p_copy["api_key"] = ""
                    elif "api_key" in prof and str(prof.get("api_key") or "").strip():
                        p_copy["api_key"] = str(prof["api_key"]).strip()
                    overrides = prof.get("target_overrides")
                    if isinstance(overrides, dict):
                        p_copy["target_overrides"] = {k: _url(v, "目标推理地址") for k, v in overrides.items() if isinstance(k, str) and k}
                    p_copy["gateway_type"] = gt
                    profiles[gt] = p_copy
                except ValueError:
                    continue
    return profiles


def validate_gateway_config(value: dict[str, Any], existing_profiles: dict[str, dict[str, Any]] | None = None) -> dict[str, Any]:
    gw_type = str(value.get("gateway_type") or "opencodex").strip().lower()
    if gw_type not in SUPPORTED_GATEWAY_TYPES:
        raise ValueError("暂不支持此 LLM 网关类型")
    overrides = value.get("target_overrides") or {}
    if not isinstance(overrides, dict) or any(not isinstance(key, str) or not key for key in overrides):
        raise ValueError("目标地址覆盖格式无效")

    profiles = _clean_profiles(value.get("gateway_profiles"), existing_profiles)

    # Resolve active api_key
    if value.get("clear_key") is True:
        resolved_key = ""
    elif "api_key" in value and str(value.get("api_key") or "").strip():
        resolved_key = str(value["api_key"]).strip()
    else:
        resolved_key = profiles.get(gw_type, {}).get("api_key", "")

    validated = {
        "gateway_type": gw_type,
        "management_url": _url(value.get("management_url"), "管理地址"),
        "inference_url": _url(value.get("inference_url"), "推理 Base URL"),
        "api_key": resolved_key,
        "target_overrides": {key: _url(url, "目标推理地址") for key, url in overrides.items()},
    }
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
    existing_profiles = current.get("gateway_profiles") or {}
    config = validate_gateway_config(value, existing_profiles)
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
