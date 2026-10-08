from __future__ import annotations

import hashlib
import json
import re
from typing import Any

NATIVE_GROK_MODELS = {"grok-4.5", "grok-build", "grok-4.20", "grok-code"}
GROK_EFFORTS = ("none", "minimal", "low", "medium", "high", "xhigh", "max")
GATEWAY_ENV_KEYS = {
    "opencodex": "OPENCODEX_API_AUTH_TOKEN",
    "magpie": "MAGPIE_API_KEY",
}
HERMES_PROVIDER_ALIASES = {
    "magpie": ("magpie",),
    "opencodex": ("ocx", "opencodex"),
}
_YAML_KEY = re.compile(r"^(\s*)([A-Za-z0-9_-]+):(.*)$")

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
def normalize_magpie_catalog(payload: Any) -> dict[str, Any]:
    if isinstance(payload, dict) and "data" in payload:
        payload = payload["data"]
    if not isinstance(payload, list):
        raise TypeError("Magpie 模型目录格式无效")
    models: list[dict[str, Any]] = []
    for row in payload:
        if not isinstance(row, dict):
            continue
        mid = str(row.get("id") or "").strip()
        if not mid:
            continue
        provider = str(row.get("owned_by") or (mid.split("/")[0] if "/" in mid else "magpie")).strip()
        levels = row.get("supported_reasoning_levels") or []
        efforts = [str(item.get("effort")) for item in levels if isinstance(item, dict) and item.get("effort")]
        context_window = int(row.get("context_window") or row.get("context_length") or 0)
        max_input_tokens = int(row.get("max_input_tokens") or 0)
        model = {
            "slug": mid,
            "provider": provider,
            "id": mid,
            "context_window": context_window,
            "max_input_tokens": max_input_tokens,
            "auto_compact_token_limit": int(context_window * 0.8) if context_window > 0 else 0,
            "input_modalities": ["text", "image"],
            "reasoning_efforts": efforts,
            "default_reasoning_effort": efforts[1] if len(efforts) > 1 else (efforts[0] if efforts else ""),
            "native": False,
        }
        models.append(model)
    models.sort(key=lambda item: item["slug"])
    encoded = json.dumps(models, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
    return {"models": models, "fingerprint": hashlib.sha256(encoded).hexdigest()}


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
        context_window = model["context_window"] or previous.get("context_window") or previous.get("max_context_window", 0)
        compact_limit = model["auto_compact_token_limit"] or (context_window * 4 // 5 if context_window > 0 else None)
        facts = {
            "context_window": context_window,
            "max_context_window": model["context_window"] or previous.get("max_context_window", 0),
            "input_modalities": model["input_modalities"],
            "supported_reasoning_levels": efforts,
            "default_reasoning_level": default_effort,
        }
        if compact_limit is not None:
            facts["auto_compact_token_limit"] = compact_limit
        row = {
            **DEFAULT_CODEX_MODEL_PROTOTYPE,
            "slug": model["slug"], "display_name": model["slug"],
            "description": f"由 {gateway_name} 提供", "visibility": "list",
        }
        row.update(previous)
        row.update(facts)
        if compact_limit is None:
            row.pop("auto_compact_token_limit", None)
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


def render_codex_config(
    existing: str,
    inference_url: str,
    catalog_path: str,
    *,
    provider_key: str = "opencodex",
    provider_name: str = "OpenCodeX Proxy",
    env_key: str = "OPENCODEX_API_AUTH_TOKEN",
    all_providers: list[dict[str, str]] | None = None,
) -> str:
    existing = existing.lstrip("\ufeff")
    # Retain all configured gateway providers in config.toml so historical sessions remain valid
    providers_to_render = dict()
    if all_providers:
        for p in all_providers:
            k = p.get("key")
            if k:
                providers_to_render[k] = p
    providers_to_render[provider_key] = {
        "key": provider_key,
        "name": provider_name,
        "base_url": inference_url,
        "env_key": env_key,
    }

    # Replace old entries of these known providers
    text = _replace_sections(existing, {f"model_providers.{k}" for k in providers_to_render} | {"model_providers.opencodex", "model_providers.magpie"}, "")
    text = _replace_top_keys(text, {
        "model_provider": provider_key, "model_catalog_json": catalog_path,
    })

    sections = []
    # Render active provider first, then secondary providers
    ordered_keys = [provider_key] + [k for k in providers_to_render if k != provider_key]
    for k in ordered_keys:
        p = providers_to_render[k]
        p_name = p.get("name") or ("Magpie Proxy" if k == "magpie" else "OpenCodeX Proxy")
        p_url = p.get("base_url") or inference_url
        p_env = p.get("env_key") or env_key
        sec = (
            f"[model_providers.{k}]\n"
            f"name = {_toml_string(p_name)}\n"
            f"base_url = {_toml_string(p_url)}\n"
            'wire_api = "responses"\nrequires_openai_auth = true\n'
            f"env_key = {_toml_string(p_env)}"
        )
        sections.append(sec)

    return text.rstrip() + "\n\n" + "\n\n".join(sections) + "\n"


def gateway_env_values(config: dict[str, Any]) -> dict[str, str]:
    profiles = config.get("gateway_profiles") or {}
    active = str(config.get("gateway_type") or "opencodex")
    values: dict[str, str] = {}
    for gateway, env_name in GATEWAY_ENV_KEYS.items():
        profile = profiles.get(gateway) if isinstance(profiles.get(gateway), dict) else {}
        key = str(profile.get("api_key") or "")
        if not key and gateway == active:
            key = str(config.get("api_key") or "")
        if key:
            values[env_name] = key
    return values


def hermes_bindings(config: dict[str, Any], target_id: str, existing: str) -> dict[str, dict[str, str]]:
    profiles = config.get("gateway_profiles") or {}
    active = str(config.get("gateway_type") or "opencodex")
    bindings: dict[str, dict[str, str]] = {}
    for gateway, aliases in HERMES_PROVIDER_ALIASES.items():
        profile = profiles.get(gateway) if isinstance(profiles.get(gateway), dict) else {}
        if gateway == active:
            overrides = config.get("target_overrides") or {}
            url = overrides.get(target_id) or config.get("inference_url") or ""
            key = str(config.get("api_key") or profile.get("api_key") or "")
        else:
            overrides = profile.get("target_overrides") or {}
            url = overrides.get(target_id) or profile.get("inference_url") or ""
            key = str(profile.get("api_key") or "")
        url = str(url or "").strip().rstrip("/")
        if not url:
            continue
        item = {"base_url": url}
        if key:
            item["api_key"] = key
        bindings[_matching_provider(existing, aliases)] = item
    return bindings


def render_hermes_config(existing: str, bindings: dict[str, dict[str, str]]) -> str:
    existing = existing.lstrip("\ufeff")
    if not existing.strip() or not bindings:
        return ""
    lines = existing.splitlines()
    provider = _model_provider(lines)
    for name, fields in bindings.items():
        _ensure_provider(lines, name, fields)
    if provider in bindings and bindings[provider].get("base_url"):
        _set_mapping_value(lines, ("model",), "base_url", bindings[provider]["base_url"])
    return "\n".join(lines).rstrip("\n") + "\n"


def _yaml_entries(lines: list[str]) -> list[tuple[int, int, str, str]]:
    entries = []
    for index, line in enumerate(lines):
        stripped = line.lstrip()
        if not stripped or stripped.startswith("#"):
            continue
        match = _YAML_KEY.match(line)
        if match:
            entries.append((index, len(match.group(1)), match.group(2), match.group(3)))
    return entries


def _block_end(entries: list[tuple[int, int, str, str]], entry_index: int, line_count: int) -> int:
    indent = entries[entry_index][1]
    for line_index, later_indent, _key, _rest in entries[entry_index + 1:]:
        if later_indent <= indent:
            return line_index
    return line_count


def _find_block(lines: list[str], path: tuple[str, ...]) -> tuple[int, int] | None:
    entries = _yaml_entries(lines)
    parent_end = len(lines)
    parent_indent = -1
    start = 0
    header = None
    for name in path:
        found = None
        for index in range(start, len(entries)):
            line_index, indent, key, _rest = entries[index]
            if line_index >= parent_end or (parent_indent >= 0 and indent <= parent_indent):
                break
            if key != name:
                continue
            if parent_indent < 0 and indent == 0 or parent_indent >= 0 and indent > parent_indent:
                found = index
                break
        if found is None:
            return None
        header = entries[found][0]
        parent_end = _block_end(entries, found, len(lines))
        parent_indent = entries[found][1]
        start = found + 1
    return None if header is None else (header, parent_end)


def _scalar(raw: str) -> str:
    text = raw.split("#", 1)[0].strip()
    if len(text) >= 2 and text[0] == text[-1] and text[0] in {'"', "'"}:
        return text[1:-1]
    return text


def _model_provider(lines: list[str]) -> str:
    block = _find_block(lines, ("model",))
    if block is None:
        return ""
    header, end = block
    parent_indent = len(lines[header]) - len(lines[header].lstrip(" "))
    child_indent = _direct_indent(lines, header, end, parent_indent)
    if child_indent is None:
        return ""
    for line_index, indent, key, rest in _yaml_entries(lines):
        if header < line_index < end and key == "provider" and indent == child_indent:
            return _scalar(rest)
    return ""


def _matching_provider(existing: str, aliases: tuple[str, ...]) -> str:
    lines = existing.splitlines()
    providers = _find_block(lines, ("providers",))
    if providers is None:
        return aliases[-1]
    header, end = providers
    parent_indent = len(lines[header]) - len(lines[header].lstrip(" "))
    child_indent = _direct_indent(lines, header, end, parent_indent)
    present = set()
    if child_indent is not None:
        present = {
            key for line_index, indent, key, _rest in _yaml_entries(lines)
            if header < line_index < end and indent == child_indent and key in aliases
        }
    return next((alias for alias in aliases if alias in present), aliases[-1])


def _direct_indent(lines: list[str], header: int, end: int, parent_indent: int) -> int | None:
    child_indent = None
    for line_index, indent, _key, _rest in _yaml_entries(lines):
        if header < line_index < end and indent > parent_indent and (child_indent is None or indent < child_indent):
            child_indent = indent
    return child_indent


def _set_mapping_value(lines: list[str], path: tuple[str, ...], key: str, value: str) -> None:
    block = _find_block(lines, path)
    if block is None:
        return
    header, end = block
    parent_indent = len(lines[header]) - len(lines[header].lstrip(" "))
    quoted = json.dumps(value, ensure_ascii=False)
    child_indent = _direct_indent(lines, header, end, parent_indent)
    if child_indent is not None:
        for line_index, indent, child, _rest in _yaml_entries(lines):
            if header < line_index < end and child == key and indent == child_indent:
                lines[line_index] = f"{' ' * indent}{key}: {quoted}"
                return
        insert_indent = child_indent
    else:
        insert_indent = parent_indent + 2
    lines.insert(end, f"{' ' * insert_indent}{key}: {quoted}")


def _ensure_provider(lines: list[str], name: str, fields: dict[str, str]) -> None:
    if _find_block(lines, ("providers", name)) is None:
        providers = _find_block(lines, ("providers",))
        if providers is None:
            child_indent = 2
            insert_at = None
        else:
            parent_indent = len(lines[providers[0]]) - len(lines[providers[0]].lstrip(" "))
            child_indent = _direct_indent(lines, providers[0], providers[1], parent_indent) or parent_indent + 2
            insert_at = providers[1]
        chunk = [f"{' ' * child_indent}{name}:", f"{' ' * (child_indent + 2)}name: {json.dumps(name, ensure_ascii=False)}"]
        for field in ("base_url", "api_key"):
            if fields.get(field):
                chunk.append(f"{' ' * (child_indent + 2)}{field}: {json.dumps(fields[field], ensure_ascii=False)}")
        if insert_at is None:
            if lines and lines[-1].strip():
                chunk = ["", "providers:", *chunk]
            else:
                chunk = ["providers:", *chunk]
            lines.extend(chunk)
        else:
            lines[insert_at:insert_at] = chunk
    for field in ("base_url", "api_key"):
        if fields.get(field):
            _set_mapping_value(lines, ("providers", name), field, fields[field])


def render_grok_config(existing: str, catalog: dict[str, Any], inference_url: str, api_key: str) -> str:
    existing = existing.lstrip("\ufeff")
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
