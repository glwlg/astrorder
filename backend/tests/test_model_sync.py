import json
import tomllib

from astrorder.model_sync import (
    normalize_catalog,
    render_codex_catalog,
    render_codex_config,
    render_grok_config,
)


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
