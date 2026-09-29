import time
from pathlib import Path

import httpx
import pytest
from fastapi.testclient import TestClient

from astrorder.config import Settings
from astrorder.core import analytics, model_sync_service
from astrorder.core.model_sync import normalize_catalog, render_codex_catalog
from astrorder.main import create_app


@pytest.mark.asyncio
async def test_fetch_catalog_excludes_models_of_disabled_providers(monkeypatch):
    def gateway(request: httpx.Request) -> httpx.Response:
        if request.url.path == "/api/models":
            return httpx.Response(200, json=[
                {"provider": "google-ani", "id": "gemini-3.8-flash", "namespaced": "google-ani/gemini-3.8-flash", "disabled": False},
                {"provider": "xai", "id": "grok-4.7", "namespaced": "xai/grok-4.7", "disabled": False},
                {"provider": "combo", "id": "mix", "namespaced": "combo/mix", "disabled": False},
            ])
        if request.url.path == "/api/providers":
            return httpx.Response(200, json=[
                {"name": "google-ani", "disabled": True},
                {"name": "xai", "disabled": False},
            ])
        raise AssertionError(request.url.path)

    client = httpx.AsyncClient
    monkeypatch.setattr(model_sync_service.httpx, "AsyncClient", lambda **kw: client(transport=httpx.MockTransport(gateway), **kw))
    catalog = await model_sync_service.fetch_catalog({"management_url": "https://gateway.example", "api_key": ""})
    assert [model["slug"] for model in catalog["models"]] == ["combo/mix", "xai/grok-4.7"]
    old = {"models": [{"slug": "google-ani/gemini-3.8-flash", "context_window": 0}]}
    rendered = render_codex_catalog(catalog, old)
    assert "google-ani/gemini-3.8-flash" not in rendered


def test_model_sync_preview_and_background_job(tmp_path: Path, monkeypatch):
    app = create_app(Settings(
        database_url=f"sqlite:///{(tmp_path / 'state.sqlite3').as_posix()}",
        browser_secret="test-secret", connector_secret="connector-secret",
        attachments_dir=tmp_path / "attachments", auto_connect_local_hermes=False,
    ))
    catalog = normalize_catalog([{
        "provider": "openai", "id": "gpt-test", "namespaced": "gpt-test",
        "disabled": False, "reasoningEfforts": ["low"],
    }])

    async def fake_catalog(_config):
        return catalog

    class Bridge:
        def __init__(self):
            self.actions = []

        async def request_control(self, action, fields):
            self.actions.append(action)
            if action == "model_config.plan":
                empty = {"exists": False, "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "content": ""}
                return {"result": {"_home": "/home/test", "codex_config": empty, "codex_catalog": empty, "grok_config": empty}}
            if action == "model_config.apply":
                return {"result": {"changed": list(fields["files"])}}
            if action == "model_config.reload":
                return {"result": {"reloaded": fields["agents"], "pending": []}}
            raise AssertionError(action)

    monkeypatch.setattr(analytics, "fetch_catalog", fake_catalog)
    monkeypatch.setattr(model_sync_service, "fetch_catalog", fake_catalog)
    headers = {"Authorization": "Bearer test-secret"}
    with TestClient(app) as client:
        bridge = app.state.daemon_bridge = Bridge()
        payload = {"targets": [{"target_id": "local", "agents": ["codex", "grok"]}]}
        preview = client.post("/api/v1/model-sync/preview", json=payload, headers=headers)
        assert preview.status_code == 200
        assert preview.json()["model_count"] == 1
        assert len(preview.json()["targets"][0]["changes"]) == 3

        started = client.post("/api/v1/model-sync/jobs", json=payload, headers=headers)
        assert started.status_code == 200
        operation_id = started.json()["id"]
        for _ in range(30):
            jobs = client.get("/api/v1/model-sync/jobs", headers=headers).json()["items"]
            job = next(item for item in jobs if item["id"] == operation_id)
            if job["status"] != "running":
                break
            time.sleep(0.02)
        assert job["status"] == "success"
        assert job["targets"][0]["reloaded"] == ["codex", "grok"]

        hermes = client.post("/api/v1/model-sync/jobs", json={
            "targets": [{"target_id": "local", "agents": ["hermes"]}],
        }, headers=headers).json()
        for _ in range(30):
            hermes_job = next(
                item for item in client.get("/api/v1/model-sync/jobs", headers=headers).json()["items"]
                if item["id"] == hermes["id"]
            )
            if hermes_job["status"] != "running":
                break
            time.sleep(0.02)
        assert hermes_job["status"] == "success"
        assert hermes_job["targets"][0]["changed"] == []
        assert bridge.actions.count("model_config.apply") == 1

        duplicate = client.post("/api/v1/model-sync/preview", json={"targets": payload["targets"] * 2}, headers=headers)
        assert duplicate.status_code == 422


@pytest.mark.asyncio
async def test_fetch_catalog_magpie(monkeypatch):
    def gateway(request: httpx.Request) -> httpx.Response:
        if request.url.path == "/v1/models":
            return httpx.Response(200, json={
                "data": [
                    {
                        "id": "codex/gpt-6-astra", "owned_by": "codex",
                        "context_window": 272000, "max_input_tokens": 272000,
                        "supported_reasoning_levels": [{"effort": "low"}, {"effort": "medium"}],
                    }
                ]
            })
        raise AssertionError(request.url.path)

    client = httpx.AsyncClient
    monkeypatch.setattr(model_sync_service.httpx, "AsyncClient", lambda **kw: client(transport=httpx.MockTransport(gateway), **kw))
    catalog = await model_sync_service.fetch_catalog({
        "gateway_type": "magpie",
        "inference_url": "http://127.0.0.1:3425/v1",
        "api_key": "sk-magpie-test",
    })
    assert len(catalog["models"]) == 1
    assert catalog["models"][0]["slug"] == "codex/gpt-6-astra"
    assert catalog["models"][0]["provider"] == "codex"
