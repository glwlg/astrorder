from fastapi import FastAPI
from fastapi.testclient import TestClient

from astrorder.api import router
from astrorder.config import Settings
from astrorder.llm_config import get_llm_config, reasoning_payload, set_llm_config
from astrorder.store import Store


def test_llm_config_persists_and_validates(tmp_path):
    store = Store(Settings(database_url=f"sqlite:///{tmp_path / 'llm.db'}"))
    assert get_llm_config(store)["model"] == "deepseek-chat"

    saved = set_llm_config(store, {
        "base_url": "https://model.example/v1/",
        "api_key": "secret",
        "model": "fast-model",
        "reasoning": "medium",
    })

    assert saved["base_url"] == "https://model.example/v1"
    assert get_llm_config(store) == saved
    assert reasoning_payload(saved["base_url"], "high") == {"reasoning": {"effort": "high"}}
    assert reasoning_payload("https://api.deepseek.com/v1", "none") == {"thinking": {"type": "disabled"}}


def test_llm_config_api_masks_and_preserves_key(tmp_path):
    settings = Settings(database_url=f"sqlite:///{tmp_path / 'api.db'}", browser_secret="test-secret")
    app = FastAPI()
    app.state.settings = settings
    app.state.store = Store(settings)
    app.include_router(router)

    with TestClient(app, headers={"Authorization": "Bearer test-secret"}) as client:
        response = client.post("/api/v1/services/llm/config", json={
            "base_url": "https://model.example/v1",
            "api_key": "very-secret-api-key",
            "model": "fast-model",
            "reasoning": "high",
        })
        assert response.status_code == 200
        assert response.json()["masked_key"] == "very-s...-key"
        assert "api_key" not in response.json()

        response = client.post("/api/v1/services/llm/config", json={
            "base_url": "https://other.example/v1",
            "model": "other-model",
            "reasoning": "none",
        })
        assert response.status_code == 200
        assert get_llm_config(app.state.store)["api_key"] == "very-secret-api-key"
