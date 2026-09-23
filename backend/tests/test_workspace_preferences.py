from fastapi import FastAPI
from fastapi.testclient import TestClient

from astrorder.config import Settings
from astrorder.core.llm_config import set_llm_config
from astrorder.store import Store
from astrorder.core.workspace_preferences import router


def test_preferences_persist_across_devices_and_import_never_overwrites(tmp_path):
    settings = Settings(database_url=f"sqlite:///{tmp_path / 'prefs.db'}", browser_secret="test-only-secret")
    app = FastAPI()
    app.state.settings = settings
    app.state.store = Store(settings)
    set_llm_config(app.state.store, {"base_url": "https://model.example/v1", "api_key": "secret", "model": "fast", "reasoning": "low"})
    app.include_router(router)
    path = "/api/v1/preferences"
    with TestClient(app) as desktop, TestClient(app) as phone:
        assert phone.get(path).status_code == 401
        assert phone.patch(path, json={"session_pins": {"a": True}}).status_code == 401
        assert phone.post(path + "/import", json={}).status_code == 401
        for client in (desktop, phone):
            client.headers["Authorization"] = "Bearer test-only-secret"
        # An empty phone must not initialize away the desktop's customizations.
        assert phone.post(path + "/import", json={"pinned_projects": [], "project_order": []}).status_code == 200
        legacy = {"appearance": {"project:a\u0000b": {"icon": "cloud", "color": "orange"}}, "session_pins": {"a\u0000session": True}, "pinned_projects": ["p"], "project_order": ["p", "q"]}
        assert desktop.post(path + "/import", json=legacy).json() == legacy
        assert phone.get(path).json() == legacy
        assert "services" not in phone.get(path).json()
        assert phone.patch(path, json={"session_pins": {"other": True}}).json()["session_pins"] == {"a\u0000session": True, "other": True}
        removed = {"appearance": {"project:a\u0000b": None}, "session_pins": {"a\u0000session": False}, "pinned_projects": [], "project_order": ["q", "p"]}
        desktop.patch(path, json=removed)
        desktop.post(path + "/import", json=legacy)
        app.state.store.engine.dispose()
        app.state.store = Store(settings)
        result = phone.get(path).json()
        assert result == {"appearance": {}, "session_pins": {"a\u0000session": False, "other": True}, "pinned_projects": [], "project_order": ["q", "p"]}
        for invalid in ({"appearance": {"p": {"color": "url(evil)"}}}, {"session_pins": {"p": "true"}}, {"unknown": True}, {"project_order": [""]}):
            assert desktop.patch(path, json=invalid).status_code == 422
        assert phone.get(path).json() == result
        desktop.headers["Origin"] = "https://untrusted.example"
        assert desktop.patch(path, json={}).status_code == 403
    app.state.store.engine.dispose()
