from fastapi.testclient import TestClient

from astrorder.config import Settings
from astrorder.main import create_app


def test_model_routing_status_is_authenticated_and_reports_budget(tmp_path):
    app = create_app(
        Settings(
            database_url=f"sqlite:///{tmp_path / 'api.sqlite3'}",
            browser_secret="browser-secret",
            connector_secret="connector-secret",
            static_dir=tmp_path / "static",
            allowed_origins=("http://testserver",),
            auto_connect_local_hermes=False,
            model_routing_enabled=True,
            model_routing_daily_budget_units=80,
            hermes_small_model="ocx/small",
            hermes_large_model="ocx/large",
            codex_small_model="opencodex/small",
            codex_large_model="opencodex/large",
        )
    )
    with TestClient(app) as client:
        assert client.get("/api/v1/model-routing").status_code == 401
        login = client.post(
            "/api/v1/auth/session",
            json={"token": "browser-secret"},
            headers={"Origin": "http://testserver"},
        )
        assert login.status_code == 200

        response = client.get("/api/v1/model-routing")

    assert response.status_code == 200
    assert response.json() == {
        "enabled": True,
        "daily_budget_units": 80,
        "small_cost_units": 1,
        "large_cost_units": 5,
        "large_text_threshold": 2000,
        "usage": [],
    }
