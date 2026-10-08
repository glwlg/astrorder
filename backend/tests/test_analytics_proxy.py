import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, urlencode, urlparse

import httpx
from fastapi.testclient import TestClient
from sqlalchemy import func, select

from astrorder import analytics
from astrorder.config import Settings
from astrorder.core.gateway_config import get_gateway_config
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
        data = saved.json()
        assert data["gateway_type"] == "opencodex"
        assert data["management_url"] == "https://usage.example"
        assert data["inference_url"] == "https://llm.example/v1"
        assert data["target_overrides"] == {}
        assert data["masked_key"] == "已配置"

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


def test_magpie_management_key_is_masked_and_kept(tmp_path: Path) -> None:
    app = _app(tmp_path)
    headers = {"Authorization": "Bearer test-secret"}
    with TestClient(app) as client:
        saved = client.put("/api/v1/analytics/config", json={
            "gateway_type": "magpie",
            "management_url": "https://mag.example",
            "inference_url": "http://127.0.0.1:3425/v1",
            "api_key": "sk-magpie-test",
            "management_key": "magpie-web-access",
        }, headers=headers)
        assert saved.status_code == 200
        body = saved.text
        assert "magpie-web-access" not in body
        assert saved.json()["masked_management_key"] == "magpie...cess"
        assert saved.json()["gateway_profiles"]["magpie"]["masked_management_key"] == "magpie...cess"

        kept = client.put("/api/v1/analytics/config", json={
            "gateway_type": "magpie",
            "management_url": "https://mag.example",
            "inference_url": "http://127.0.0.1:3425/v1",
        }, headers=headers)
        assert kept.status_code == 200
        assert get_gateway_config(app.state.store)["management_key"] == "magpie-web-access"

        switched = client.put("/api/v1/analytics/config", json={
            "gateway_type": "opencodex",
            "management_url": "https://ocx.example",
            "inference_url": "https://llm.example/v1",
        }, headers=headers)
        assert switched.status_code == 200
        stored = get_gateway_config(app.state.store)
        assert stored["management_key"] == ""
        assert stored["gateway_profiles"]["magpie"]["management_key"] == "magpie-web-access"
        assert "magpie-web-access" not in switched.text


def test_magpie_usage_requires_management_key(tmp_path: Path) -> None:
    app = create_app(Settings(
        database_url=f"sqlite:///{(tmp_path / 'state.sqlite3').as_posix()}",
        browser_secret="test-secret",
        connector_secret="connector-secret",
        attachments_dir=tmp_path / "attachments",
        auto_connect_local_hermes=False,
    ))
    headers = {"Authorization": "Bearer test-secret"}
    with TestClient(app) as client:
        saved = client.put("/api/v1/analytics/config", json={
            "gateway_type": "magpie", "management_url": "http://127.0.0.1:3425/v1",
            "inference_url": "http://127.0.0.1:3425/v1", "api_key": "sk-magpie-test",
        }, headers=headers)
        assert saved.status_code == 200
        missing = client.get("/api/v1/analytics/usage?range=7d&surface=all", headers=headers)
        assert missing.status_code == 422


def test_magpie_usage_and_quota_follow_management_api(tmp_path: Path) -> None:
    analytics._quota_snapshot_cache.update(expires_at=0.0, payload=None, key=None)
    requests_payload = {
        "calls": 2, "input": 100, "output": 40, "cache_read": 10, "reasoning": 5, "cost": 1.25, "unpriced": 0,
        "series": [{
            "time": "2026-10-07T00:00:00+08:00", "calls": 2, "input": 100, "output": 40, "cost": 1.25,
            "by": {"modelAt": {"openai/gpt-5": {"calls": 2, "tokens": 150, "cost": 1.25}}, "provider": {"openai": {"calls": 2, "tokens": 150, "cost": 1.25}}},
        }],
        "by": {
            "modelAt": [{"id": "openai/gpt-5", "name": "gpt-5 · OpenAI", "calls": 2, "input": 100, "output": 40, "cache_read": 10, "reasoning": 5, "cost": 1.25, "unpriced": 0}],
            "provider": [{"id": "openai", "name": "OpenAI", "calls": 2, "input": 100, "output": 40, "cache_read": 10, "reasoning": 5, "cost": 1.25, "unpriced": 0}],
        },
    }
    overview = {"accounts": [{"name": "ada@example.com", "provider": "openai", "calls": 2, "input": 100, "output": 40, "cache_read": 10, "cost": 1.25, "unpriced": 0}]}
    quotas = [{"provider": "openai", "name": "ChatGPT", "user": "ada@example.com", "plan": "plus", "windows": [
        {"name": "5 hours", "used": 12.5, "resetsAt": "2026-10-07T12:00:00+08:00"},
        {"name": "7 days", "used": 40, "resetsAt": "2026-10-08T00:00:00+08:00"},
        {"name": "15 hours", "used": 3},
        {"name": "bonus", "used": 100, "unlimited": True},
    ]}]
    seen: list[str] = []

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            parsed = urlparse(self.path)
            query = parse_qs(parsed.query)
            seen.append(self.path)
            if "k" in query:
                rest = urlencode({key: value[0] for key, value in query.items() if key != "k"})
                self.send_response(303)
                self.send_header("Set-Cookie", "magpie_web_3430=web-key; Path=/; HttpOnly")
                self.send_header("Location", f"{parsed.path}?{rest}" if rest else parsed.path)
                self.end_headers()
                return
            if parsed.path.startswith("/api/") and "magpie_web_3430=web-key" not in self.headers.get("Cookie", ""):
                self.send_response(401)
                self.end_headers()
                return
            if parsed.path == "/api/usage/requests":
                payload = requests_payload
            elif parsed.path == "/api/usage":
                payload = overview
            elif parsed.path == "/api/usage/quotas":
                payload = quotas
            elif parsed.path == "/v1/models":
                payload = {"data": [{"id": "openai/gpt-5", "owned_by": "openai"}]}
            else:
                self.send_response(404)
                self.end_headers()
                return
            raw = json.dumps(payload).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

        def log_message(self, _format, *_args):
            return

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    port = server.server_address[1]
    try:
        app = _app(tmp_path)
        headers = {"Authorization": "Bearer test-secret"}
        with TestClient(app) as client:
            saved = client.put("/api/v1/analytics/config", json={
                "gateway_type": "magpie",
                "management_url": f"http://127.0.0.1:{port}",
                "inference_url": f"http://127.0.0.1:{port}/v1",
                "api_key": "sk-magpie-test",
                "management_key": "web-key",
            }, headers=headers)
            assert saved.status_code == 200
            usage = client.get("/api/v1/analytics/usage?range=7d&surface=codex", headers=headers)
            assert usage.status_code == 200, usage.text
            body = usage.json()
            assert body["summary"]["requests"] == 2
            assert body["summary"]["totalTokens"] == 140
            assert body["summary"]["estimatedCostUsd"] == 1.25
            assert body["models"][0]["model"] == "gpt-5"
            assert body["models"][0]["provider"] == "OpenAI"
            assert body["models"][0]["inputTokens"] == 100
            assert body["providers"][0]["provider"] == "OpenAI"
            assert body["days"][0]["date"] == "2026-10-07"
            assert body["days"][0]["models"][0]["model"] == "gpt-5"
            assert body["accounts"] == []
            assert any("agent=codex" in path and "k=web-key" in path for path in seen)

            overview_usage = client.get("/api/v1/analytics/usage?range=7d&surface=all", headers=headers)
            assert overview_usage.status_code == 200, overview_usage.text
            assert overview_usage.json()["accounts"][0]["accountLogLabel"] == "ada@example.com · openai"

            mapped_reports, mapped_accounts = analytics.magpie_quota_cards(quotas)
            assert mapped_reports[0]["quota"]["fiveHourPercent"] == 12.5
            assert mapped_reports[0]["quota"]["weeklyPercent"] == 40
            assert mapped_reports[0]["quota"]["customWindows"][0]["label"] == "15 hours"
            assert mapped_accounts[0]["quota"]["shortPercent"] == 12.5
            assert all(item["label"] != "bonus" for item in mapped_reports[0]["quota"]["customWindows"])

            quota = client.get("/api/v1/analytics/model-quota", params={"model": "openai/gpt-5"}, headers=headers)
            assert quota.status_code == 200, quota.text
            assert quota.json()["accounts"][0]["quota"]["shortPercent"] == 12.5
            assert quota.json()["accounts"][0]["quota"]["weeklyPercent"] == 40
            assert "127.0.0.1:3425" not in quota.text
    finally:
        server.shutdown()
        server.server_close()
    assert any(path.startswith("/api/usage/requests?") for path in seen)
    assert any(path.startswith("/api/usage/quotas") for path in seen)
    assert all("3425/v1/magpie/quotas" not in path for path in seen)


def _app(tmp_path: Path):
    return create_app(Settings(
        database_url=f"sqlite:///{(tmp_path / 'state.sqlite3').as_posix()}",
        browser_secret="test-secret",
        connector_secret="connector-secret",
        attachments_dir=tmp_path / "attachments",
        auto_connect_local_hermes=False,
    ))
