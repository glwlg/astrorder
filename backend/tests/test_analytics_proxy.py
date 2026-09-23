from pathlib import Path

import httpx
from fastapi.testclient import TestClient
from sqlalchemy import func, select

from astrorder import analytics
from astrorder.config import Settings
from astrorder.main import create_app
from astrorder.models import TokenMetricRow


def test_ocx_usage_config_and_live_proxy(tmp_path: Path, monkeypatch) -> None:
    app = create_app(Settings(
        database_url=f"sqlite:///{(tmp_path / 'state.sqlite3').as_posix()}",
        browser_secret="test-secret",
        connector_secret="connector-secret",
        attachments_dir=tmp_path / "attachments",
        auto_connect_local_hermes=False,
    ))
    captured = {}

    class Client:
        def __init__(self, **kwargs):
            captured["options"] = kwargs

        async def __aenter__(self):
            return self

        async def __aexit__(self, *_args):
            return None

        async def get(self, url, params, headers):
            captured.update(url=url, params=params, headers=headers)
            request = httpx.Request("GET", url, params=params)
            return httpx.Response(200, json={"summary": {"requests": 12}, "days": [], "models": [], "providers": [], "accounts": []}, request=request)

    headers = {"Authorization": "Bearer test-secret"}
    with TestClient(app) as client:
        monkeypatch.setattr(analytics.httpx, "AsyncClient", Client)
        saved = client.put("/api/v1/analytics/config", json={
            "gateway_type": "opencodex", "management_url": "https://usage.example",
            "inference_url": "https://llm.example/v1", "api_key": "secret-key",
        }, headers=headers)
        assert saved.status_code == 200
        assert saved.json() == {
            "gateway_type": "opencodex", "management_url": "https://usage.example",
            "inference_url": "https://llm.example/v1", "target_overrides": {}, "masked_key": "已配置",
        }

        response = client.get("/api/v1/analytics/usage?range=all&surface=grok&since=100&until=200", headers=headers)
        assert response.status_code == 200
        assert response.json()["summary"]["requests"] == 12
        assert captured["url"] == "https://usage.example/api/usage"
        assert captured["params"] == {"range": "all", "surface": "grok", "since": 100, "until": 200}
        assert captured["headers"] == {"Authorization": "Bearer secret-key"}

        with app.state.store.session() as db:
            assert db.scalar(select(func.count()).select_from(TokenMetricRow)) == 0


def test_model_quota_matches_agent_prefixed_model_and_combo_targets() -> None:
    models = [
        {"id": "gemini-3.8-flash", "provider": "google-antigravity", "namespaced": "google-antigravity/gemini-3.8-flash", "disabled": False},
        {"id": "gemini-3.8-flash", "provider": "combo", "namespaced": "combo/gemini-3.8-flash", "disabled": False},
    ]
    combos = [{"model": "combo/gemini-3.8-flash", "targets": [{"provider": "google-2012"}]}]
    reports = [
        {"provider": "google-antigravity", "quota": {"weeklyPercent": 74}},
        {"provider": "google-2012", "quota": {"weeklyPercent": 20}},
    ]

    direct = analytics._model_quota("ocx/google-antigravity/gemini-3.8-flash", models, combos, reports)
    assert [row["provider"] for row in direct["reports"]] == ["google-antigravity"]
    assert direct["accounts"] == []

    combo = analytics._model_quota("ocx/combo/gemini-3.8-flash", models, combos, reports)
    assert combo["providers"] == ["google-2012"]
    assert [row["provider"] for row in combo["reports"]] == ["google-2012"]


def test_model_quota_uses_active_openai_accounts_instead_of_aggregate_report() -> None:
    models = [{"id": "gpt-5.6-sol", "provider": "openai", "namespaced": "openai/gpt-5.6-sol", "disabled": False}]
    reports = [{"provider": "openai", "label": "OpenAI (Codex login)", "quota": {"weeklyPercent": 48}}]
    accounts = [{"id": "active", "paused": False, "quota": {"weeklyPercent": 48}}]

    result = analytics._model_quota("opencodex/gpt-5.6-sol", models, [], reports, accounts)

    assert result["reports"] == []
    assert result["accounts"] == accounts
