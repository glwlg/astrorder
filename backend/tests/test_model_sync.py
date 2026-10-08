import json
import tomllib

from astrorder.core.model_sync import (
    normalize_catalog,
    render_codex_catalog,
    render_codex_config,
    render_grok_config,
    gateway_env_values,
    hermes_bindings,
    render_hermes_config,
)
from astrorder.core.model_sync_service import _diff


def _catalog():
    return normalize_catalog([
        {
            "provider": "openai", "id": "gpt-5.6-sol", "namespaced": "gpt-5.6-sol",
            "disabled": False, "contextWindow": 272000, "autoCompactTokenLimit": 244800,
            "inputModalities": ["image", "text"], "reasoningEfforts": ["low", "high", "ultra"],
            "defaultReasoningEffort": "low",
        },
        {"provider": "xai", "id": "grok-4.7", "namespaced": "xai/grok-4.7", "disabled": False},
        {"provider": "xai", "id": "old", "namespaced": "old", "disabled": True},
    ])


def test_codex_catalog_fills_missing_reasoning_levels():
    catalog = normalize_catalog([
        {
            "provider": "xai",
            "id": "grok-4.7",
            "namespaced": "xai/grok-4.7",
            "defaultReasoningEffort": "medium",
        }
    ])

    existing = {
        "models": [
            {
                "slug": "xai/grok-4.7",
                "default_reasoning_level": "medium",
                "supported_reasoning_levels": [],
            }
        ]
    }

    model = json.loads(render_codex_catalog(catalog, existing))["models"][0]

    assert model["supported_reasoning_levels"] == [
        {"effort": "medium", "description": "medium"}
    ]
    assert model["default_reasoning_level"] == "medium"



def test_codex_catalog_preserves_previous_reasoning_levels_when_gateway_omits_them():
    catalog = normalize_catalog([
        {
            "provider": "xjj-openai",
            "id": "deepseek-v4-flash",
            "namespaced": "xjj-openai/deepseek-v4-flash",
            "defaultReasoningEffort": "medium",
        }
    ])
    previous_levels = [
        {"effort": "low", "description": "Fast"},
        {"effort": "medium", "description": "Balanced"},
        {"effort": "high", "description": "Deep"},
    ]
    existing = {
        "models": [
            {
                "slug": "xjj-openai/deepseek-v4-flash",
                "default_reasoning_level": "medium",
                "supported_reasoning_levels": previous_levels,
            }
        ]
    }

    model = json.loads(render_codex_catalog(catalog, existing))["models"][0]

    assert model["supported_reasoning_levels"] == previous_levels
    assert model["default_reasoning_level"] == "medium"



def test_catalog_and_agent_renderers_preserve_unmanaged_config():
    catalog = _catalog()
    assert [row["slug"] for row in catalog["models"]] == ["gpt-5.6-sol", "xai/grok-4.7"]
    assert len(catalog["fingerprint"]) == 64

    codex_catalog = json.loads(render_codex_catalog(catalog, {"models": [{"slug": "gpt-5.6-sol", "custom": True}]}))["models"]
    assert codex_catalog[0]["custom"] is True
    assert codex_catalog[0]["context_window"] == 272000

    codex = render_codex_config(
        'model = "keep-me"\n[features]\nfoo = true\n[model_providers.old]\nbase_url = "old"\n',
        "https://llm.example/v1", "/home/me/.codex/opencodex-catalog.json",
    )
    parsed_codex = tomllib.loads(codex)
    assert parsed_codex["model"] == "keep-me"
    assert parsed_codex["features"]["foo"] is True
    assert set(parsed_codex["model_providers"]) == {"old", "opencodex"}

    grok = render_grok_config(
        '[ui]\ntheme = "dark"\n[models]\ndefault = "grok-4.5"\n[model."old"]\nmodel = "old"\n',
        catalog, "https://llm.example/v1", "secret",
    )
    parsed_grok = tomllib.loads(grok)
    assert parsed_grok["ui"]["theme"] == "dark"
    assert [row["value"] for row in parsed_grok["model"]["gpt-5.6-sol"]["reasoning_efforts"]] == ["low", "high"]
    assert [row["value"] for row in parsed_grok["model"]["xai-grok-4.7"]["reasoning_efforts"]] == ["high", "medium", "low"]


def test_codex_catalog_derives_missing_compaction_limit_from_context_window():
    catalog = normalize_catalog([
        {"provider": "xai", "id": "grok-4.7", "namespaced": "xai/grok-4.7", "contextWindow": 500000},
    ])
    existing = {"models": [{"slug": "xai/grok-4.7", "auto_compact_token_limit": 115200}]}
    model = json.loads(render_codex_catalog(catalog, existing))["models"][0]
    assert model["context_window"] == 500000
    assert model["auto_compact_token_limit"] == 400000


def test_codex_catalog_uses_authoritative_compaction_limit_when_provided():
    catalog = normalize_catalog([
        {"provider": "openai", "id": "gpt", "namespaced": "gpt", "contextWindow": 272000, "autoCompactTokenLimit": 244800},
    ])
    model = json.loads(render_codex_catalog(catalog))["models"][0]
    assert model["auto_compact_token_limit"] == 244800


def test_codex_catalog_does_not_write_zero_compaction_limit_without_context_window():
    model = json.loads(render_codex_catalog(_catalog()))["models"][1]
    assert "auto_compact_token_limit" not in model


def test_codex_catalog_derives_limit_from_existing_window_when_gateway_omits_both():
    catalog = normalize_catalog([
        {"provider": "xai", "id": "grok-4.7", "namespaced": "xai/grok-4.7"},
    ])
    previous = {"models": [{"slug": "xai/grok-4.7", "context_window": 500000, "auto_compact_token_limit": 115200}]}
    model = json.loads(render_codex_catalog(catalog, previous))["models"][0]
    assert model["auto_compact_token_limit"] == 400000


def test_provider_env_keys_stay_separate():
    text = render_codex_config(
        "",
        "http://192.168.1.100:3425/v1",
        "/home/me/.codex/magpie-catalog.json",
        provider_key="magpie",
        provider_name="Magpie Proxy",
        env_key="MAGPIE_API_KEY",
        all_providers=[
            {"key": "magpie", "name": "Magpie Proxy", "base_url": "http://192.168.1.100:3425/v1", "env_key": "MAGPIE_API_KEY"},
            {"key": "opencodex", "name": "OpenCodeX Proxy", "base_url": "https://llm.example/v1", "env_key": "OPENCODEX_API_AUTH_TOKEN"},
        ],
    )
    parsed = tomllib.loads(text)
    assert parsed["model_providers"]["magpie"]["env_key"] == "MAGPIE_API_KEY"
    assert parsed["model_providers"]["magpie"]["base_url"] == "http://192.168.1.100:3425/v1"
    assert parsed["model_providers"]["opencodex"]["env_key"] == "OPENCODEX_API_AUTH_TOKEN"


def test_hermes_render_updates_address_and_keeps_the_rest():
    existing = """model:
  provider: magpie
  base_url: https://llm.example/v1
  aliases:
    k3: old
providers:
  ocx:
    name: ocx
    base_url: https://llm.example/v1
    models:
      keep: true
  magpie:
    name: magpie
    base_url: http://127.0.0.1:3425/v1
    api_key: old-secret
database:
  journal_mode: wal
"""
    config = {
        "gateway_type": "magpie",
        "inference_url": "http://192.168.1.100:3425/v1",
        "api_key": "magpie-key",
        "target_overrides": {"local": "http://192.168.1.100:3425/v1"},
        "gateway_profiles": {
            "magpie": {"inference_url": "http://192.168.1.100:3425/v1", "api_key": "magpie-key", "target_overrides": {}},
            "opencodex": {"inference_url": "https://llm.example/v1", "api_key": "ocx-key", "target_overrides": {}},
        },
    }
    rendered = render_hermes_config(existing, hermes_bindings(config, "local", existing))
    assert "journal_mode: wal" in rendered
    assert "k3: old" in rendered
    assert "keep: true" in rendered
    assert 'base_url: "http://192.168.1.100:3425/v1"' in rendered
    assert "old-secret" not in rendered
    assert "magpie-key" in rendered
    assert "ocx-key" in rendered
    assert "opencodex:" not in rendered
    assert render_hermes_config(" \n", hermes_bindings(config, "local", existing)) == ""


def test_gateway_env_values_do_not_cross_fill():
    values = gateway_env_values({
        "gateway_type": "magpie",
        "api_key": "magpie-key",
        "gateway_profiles": {"magpie": {"api_key": ""}, "opencodex": {"api_key": "ocx-key"}},
    })
    assert values == {"MAGPIE_API_KEY": "magpie-key", "OPENCODEX_API_AUTH_TOKEN": "ocx-key"}
    assert gateway_env_values({"gateway_type": "opencodex", "api_key": "", "gateway_profiles": {}}) == {}


def test_config_diff_redacts_yaml_api_keys():
    diff = _diff("api_key: secret\n", 'api_key: "next"\n', "hermes_config")
    assert "secret" not in diff and "next" not in diff and "[已配置]" in diff


def test_codex_catalog_can_use_existing_max_context_window():
    catalog = normalize_catalog([
        {"provider": "xai", "id": "grok-4.7", "namespaced": "xai/grok-4.7"},
    ])
    previous = {"models": [{"slug": "xai/grok-4.7", "max_context_window": 500000, "auto_compact_token_limit": 0}]}
    model = json.loads(render_codex_catalog(catalog, previous))["models"][0]
    assert model["auto_compact_token_limit"] == 400000
