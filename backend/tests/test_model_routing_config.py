import pytest

from astrorder.config import Settings


def test_model_routing_defaults_off():
    settings = Settings()

    assert settings.model_routing_enabled is False


def test_model_routing_enabled_requires_exact_targets(monkeypatch):
    values = {
        "ASTRORDER_BROWSER_SECRET": "browser-secret",
        "ASTRORDER_CONNECTOR_SECRET": "connector-secret",
        "ASTRORDER_MODEL_ROUTING_ENABLED": "true",
        "ASTRORDER_MODEL_ROUTING_DAILY_BUDGET_UNITS": "80",
        "ASTRORDER_MODEL_ROUTING_LARGE_TEXT_THRESHOLD": "1200",
        "ASTRORDER_HERMES_SMALL_MODEL": "ocx/gpt-5.6-luna--fast",
        "ASTRORDER_HERMES_LARGE_MODEL": "ocx/gpt-5.6-sol",
        "ASTRORDER_CODEX_SMALL_MODEL": "opencodex/gpt-5.6-luna",
        "ASTRORDER_CODEX_LARGE_MODEL": "opencodex/gpt-6-astra",
    }
    for key, value in values.items():
        monkeypatch.setenv(key, value)

    settings = Settings.from_env()

    assert settings.model_routing_enabled is True
    assert settings.model_routing_daily_budget_units == 80
    assert settings.model_routing_large_text_threshold == 1200
    assert settings.hermes_small_model == "ocx/gpt-5.6-luna--fast"
    assert settings.codex_large_model == "opencodex/gpt-6-astra"


def test_model_routing_enabled_rejects_missing_target():
    with pytest.raises(ValueError, match="requires exact"):
        Settings(model_routing_enabled=True)
