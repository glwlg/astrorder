from __future__ import annotations

import hashlib
import json
import re
from typing import Any

NATIVE_GROK_MODELS = {"grok-4.5", "grok-build", "grok-4.20", "grok-code"}
GROK_EFFORTS = ("none", "minimal", "low", "medium", "high", "xhigh", "max")

DEFAULT_CODEX_MODEL_PROTOTYPE = {
    "visibility": "list",
    "priority": 5,
    "base_instructions": "You are a coding agent powered by the model. You and the user share one workspace, and your job is to collaborate with them until their intended goal is completely handled.",
    "supported_in_api": True,
    "supports_parallel_tool_calls": True,
    "supports_reasoning_summaries": True,
    "shell_type": "unified_exec",
    "support_verbosity": True,
    "default_verbosity": "low",
    "apply_patch_tool_type": "freeform",
    "web_search_tool_type": "text_and_image",
    "truncation_policy": {"mode": "tokens", "limit": 10000},
    "supports_image_detail_original": True,
    "comp_hash": "3000",
    "effective_context_window_percent": 95,
    "experimental_supported_tools": ["send_user_message_async", "clock"],
    "input_modalities": ["text", "image"],
    "supports_search_tool": True,
    "supports_experimental_context": True,
    "node_repl_auto_review_required": True,
    "node_repl_disabled": False,
    "multi_agent_version": "v1",
}
def normalize_catalog(payload: Any) -> dict[str, Any]:
    if not isinstance(payload, list):
        raise TypeError("OpenCodeX 模型目录格式无效")
    models: list[dict[str, Any]] = []
    for row in payload:
        if not isinstance(row, dict) or row.get("disabled") is True:
            continue
        slug = str(row.get("namespaced") or row.get("id") or "").strip()
        provider = str(row.get("provider") or "").strip()
        if not slug or not provider:
            continue
        efforts = [str(item) for item in row.get("reasoningEfforts") or [] if str(item)]
        model = {
            "slug": slug,
            "provider": provider,
            "id": str(row.get("id") or slug),
            "context_window": int(row.get("contextWindow") or 0),
            "max_input_tokens": int(row.get("maxInputTokens") or 0),
            "auto_compact_token_limit": int(row.get("autoCompactTokenLimit") or 0),
            "input_modalities": sorted({str(item) for item in row.get("inputModalities") or ["text"]}),
            "reasoning_efforts": efforts,
            "default_reasoning_effort": str(row.get("defaultReasoningEffort") or (efforts[0] if efforts else "")),
            "native": bool(row.get("native")),
        }
        models.append(model)
    models.sort(key=lambda item: item["slug"])
    encoded = json.dumps(models, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
    return {"models": models, "fingerprint": hashlib.sha256(encoded).hexdigest()}


def render_codex_catalog(catalog: dict[str, Any], existing: Any = None, gateway_name: str = "OpenCodeX") -> str:
    existing_models = existing.get("models", []) if isinstance(existing, dict) else existing
    old = {
        str(row.get("slug")): row for row in (existing_models if isinstance(existing_models, list) else [])
        if isinstance(row, dict) and row.get("slug")
    }
    output = []
    for model in catalog["models"]:
        previous = old.get(model["slug"], {})
        previous_levels = [
            item
            for item in previous.get("supported_reasoning_levels", [])
            if isinstance(item, dict) and item.get("effort")
        ]
        old_descriptions = {
            str(item["effort"]): str(item.get("description") or item["effort"])
            for item in previous_levels
        }
        efforts = (
            [
                {"effort": effort, "description": old_descriptions.get(effort, effort)}
                for effort in model["reasoning_efforts"]
            ]
            if model["reasoning_efforts"]
            else previous_levels
        )
        if not efforts:
            fallback_effort = (
                model["default_reasoning_effort"]
                or previous.get("default_reasoning_level")
                or "medium"
            )
            efforts = [{"effort": fallback_effort, "description": fallback_effort}]
        effort_names = {str(item["effort"]) for item in efforts}
        default_effort = model["default_reasoning_effort"] or previous.get("default_reasoning_level", "")
        if default_effort not in effort_names:
            default_effort = str(efforts[0]["effort"])
        facts = {
            "context_window": model["context_window"] or previous.get("context_window", 0),
            "max_context_window": model["context_window"] or previous.get("max_context_window", 0),
            "auto_compact_token_limit": model["auto_compact_token_limit"] or previous.get("auto_compact_token_limit", 0),
            "input_modalities": model["input_modalities"],
            "supported_reasoning_levels": efforts,
            "default_reasoning_level": default_effort,
        }
        row = {
            **DEFAULT_CODEX_MODEL_PROTOTYPE,
            "slug": model["slug"], "display_name": model["slug"],
            "description": f"由 {gateway_name} 提供", "visibility": "list",
        }
        row.update(previous)
        row.update(facts)
        output.append(row)
    return json.dumps({"models": output}, ensure_ascii=False, indent=2) + "\n"


def _toml_string(value: str) -> str:
    return json.dumps(value, ensure_ascii=False)


def _replace_sections(text: str, names: set[str], replacement: str) -> str:
    lines = text.splitlines()
    kept: list[str] = []
    skipping = False
    for line in lines:
        match = re.match(r"^\s*\[\[?([^]]+)]\]?\s*(?:#.*)?$", line)
        if match:
            section = match.group(1)
            skipping = any(section == name or section.startswith(name + ".") for name in names)
        if not skipping:
            kept.append(line)
    prefix = "\n".join(kept).rstrip()
    return (prefix + ("\n\n" if prefix else "") + replacement.strip() + "\n")


def _replace_top_keys(text: str, values: dict[str, str]) -> str:
    remaining = dict(values)
    output = []
    in_section = False
    for line in text.splitlines():
        if re.match(r"^\s*\[", line):
            in_section = True
        if not in_section:
            match = re.match(r"^\s*([A-Za-z0-9_-]+)\s*=", line)
            if match and match.group(1) in remaining:
                key = match.group(1)
                output.append(f"{key} = {_toml_string(remaining.pop(key))}")
                continue
        output.append(line)
    inserted = [f"{key} = {_toml_string(value)}" for key, value in remaining.items()]
    if inserted:
        index = next((i for i, line in enumerate(output) if re.match(r"^\s*\[", line)), len(output))
        output[index:index] = inserted + ([""] if index < len(output) else [])
    return "\n".join(output).strip() + "\n"


def render_codex_config(existing: str, inference_url: str, catalog_path: str, *, env_key: str = "OPENCODEX_API_AUTH_TOKEN") -> str:
    text = _replace_sections(existing, {"model_providers.opencodex"}, "")
    text = _replace_top_keys(text, {
        "model_provider": "opencodex", "model_catalog_json": catalog_path,
    })
    section = (
        "[model_providers.opencodex]\n"
        'name = "OpenCodeX Proxy"\n'
        f"base_url = {_toml_string(inference_url)}\n"
        'wire_api = "responses"\nrequires_openai_auth = true\n'
        f"env_key = {_toml_string(env_key)}"
    )
    return text.rstrip() + "\n\n" + section + "\n"


def render_grok_config(existing: str, catalog: dict[str, Any], inference_url: str, api_key: str) -> str:
    blocks = []
    for model in catalog["models"]:
        if model["slug"] in NATIVE_GROK_MODELS:
            continue
        key = model["slug"].replace("/", "-")
        efforts = [effort for effort in model["reasoning_efforts"] if effort in GROK_EFFORTS]
        if not efforts and model["provider"] == "xai":
            efforts = ["high", "medium", "low"]
        default_effort = model["default_reasoning_effort"] if model["default_reasoning_effort"] in efforts else (efforts[0] if efforts else "")
        block = [
            f"[model.{_toml_string(key)}]", f"model = {_toml_string(model['slug'])}",
            f"base_url = {_toml_string(inference_url)}", f"api_key = {_toml_string(api_key)}",
            f"name = {_toml_string(model['slug'])}", 'description = "由 OpenCodeX 提供"',
            'api_backend = "chat_completions"', 'extra_headers = { "x-opencodex-grok" = "1" }',
            f"supports_reasoning_effort = {'true' if efforts else 'false'}",
        ]
        if default_effort:
            block.append(f"reasoning_effort = {_toml_string(default_effort)}")
        for effort in efforts:
            block.extend([
                "", f"[[model.{_toml_string(key)}.reasoning_efforts]]",
                f"value = {_toml_string(effort)}", f"label = {_toml_string(effort)}",
                f"description = {_toml_string(effort)}", f"default = {'true' if effort == default_effort else 'false'}",
            ])
        blocks.append("\n".join(block))
    base = _replace_sections(existing, {"model"}, "\n\n".join(blocks))
    if not re.search(r"(?m)^\[models]\s*$", base):
        first = next((model for model in catalog["models"] if model["slug"] not in NATIVE_GROK_MODELS), None)
        default = first["slug"].replace("/", "-") if first else "grok-4.5"
        base = f"[models]\ndefault = {_toml_string(default)}\ndefault_reasoning_effort = \"high\"\n\n" + base
    return base


def sha256_text(value: str) -> str:
    return hashlib.sha256(value.encode()).hexdigest()
