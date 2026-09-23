import pytest
from fastapi.testclient import TestClient

from astrorder.config import Settings
from astrorder.core.events import EventHub
from astrorder.main import create_app
from astrorder.service import CommandRejected, ControlService
from astrorder.store import Store


def setup(tmp_path, kind="hermes"):
    settings = Settings(database_url=f"sqlite:///{tmp_path / 'binding.sqlite3'}")
    store = Store(settings)
    store.upsert_agent({"id": "hermes", "kind": kind, "name": "Agent", "status": "ready", "capabilities": ["chat"], "limitation": None})
    store.upsert_session({"id": "session", "agent_id": "hermes", "title": "Session", "workspace": None, "status": "idle", "updated_at": "2026-09-12T00:00:00Z"})
    return store, ControlService(store, EventHub(), settings)


def payload(command_id="command"):
    return {"id": command_id, "agent_id": "hermes", "session_id": "session", "action": "send", "text": "hello", "attachment_ids": [], "target_id": None}


@pytest.mark.asyncio
async def test_registered_agent_model_is_restored_before_send(tmp_path):
    store, service = setup(tmp_path, kind="claude-code")
    store.set_session_model_binding("hermes", "session", "ocx", "gpt-5.6-sol")
    store.set_session_reasoning_binding("hermes", "session", "xhigh")
    order = []

    async def restore(agent_id, session_id, provider, model, effort):
        order.append(("restore", agent_id, session_id, provider, model, effort))

    async def send(command):
        order.append(("send", command["id"]))
        return "accepted", None

    service.register_model_binding_restorer("claude-code", restore)
    service.register_native_command_handler("hermes", send)
    result = await service.submit_browser_command(payload())

    assert result["state"] == "accepted"
    assert order == [("restore", "hermes", "session", "ocx", "gpt-5.6-sol", "xhigh"), ("send", "command")]
    store.close()
    reopened = Store(Settings(database_url=f"sqlite:///{tmp_path / 'binding.sqlite3'}"))
    assert reopened.get_session_model_binding("hermes", "session") == {
        "provider": "ocx",
        "model": "gpt-5.6-sol",
        "effort": "xhigh",
    }
    reopened.close()


@pytest.mark.asyncio
async def test_send_stops_when_saved_model_cannot_be_restored(tmp_path):
    store, service = setup(tmp_path)
    store.set_session_model_binding("hermes", "session", "ocx", "gpt-5.6-sol")
    service.register_model_binding_restorer(
        "hermes", lambda *_: (_ for _ in ()).throw(RuntimeError("failed"))
    )

    with pytest.raises(CommandRejected, match="保存的会话模型"):
        await service.submit_browser_command(payload())

    assert store.get_command("hermes", "session", "command")["state"] == "failed"


@pytest.mark.asyncio
async def test_failed_dispatch_keeps_handoff_context_for_the_next_send(tmp_path):
    store, service = setup(tmp_path)
    store.set_session_handoff_context("hermes", "session", "交接上下文")
    attempts = []

    async def send(command):
        attempts.append(command["text"])
        return ("failed", "not sent") if len(attempts) == 1 else ("accepted", None)

    service.register_native_command_handler("hermes", send)

    await service.submit_browser_command(payload("first"))
    await service.submit_browser_command(payload("second"))

    assert attempts == [
        "交接上下文\n\n<用户的新消息>\nhello",
        "交接上下文\n\n<用户的新消息>\nhello",
    ]
    assert store.pending_session_handoff_context("hermes", "session") is None


def test_model_api_saves_hermes_model_and_all_confirmed_reasoning_bindings(tmp_path):
    app = create_app(Settings(
        database_url=f"sqlite:///{tmp_path / 'api.sqlite3'}",
        attachments_dir=tmp_path / "attachments",
        browser_secret="test-token",
        auto_connect_local_hermes=False,
    ))

    class Runtime:
        def __init__(self, agent_id):
            self.agent_id = agent_id

        daemon_owned = True

        def set_model(self, _session_id, provider, model):
            return {"provider": provider, "model": model}

        def model(self, _session_id):
            if self.agent_id == "hermes":
                raise AssertionError("saved Hermes binding should not require native model state")
            return {"provider": "openai", "model": "gpt-5.6-sol", "effort": "low"}

        def set_effort(self, _session_id, effort):
            return {"effort": effort}

    with TestClient(app) as client:
        for agent_id, kind in (("hermes", "hermes"), ("codex", "codex")):
            app.state.store.upsert_agent({
                "id": agent_id, "kind": kind, "name": agent_id,
                "status": "ready", "capabilities": ["chat"], "limitation": None,
            })
            app.state.store.upsert_session({
                "id": "session", "agent_id": agent_id, "title": "Session",
                "workspace": None, "status": "idle", "updated_at": "2026-09-12T00:00:00Z",
            })
        app.state.connections.get_runtime_by_agent_id = lambda agent_id: Runtime(agent_id)
        headers = {"Authorization": "Bearer test-token"}

        response = client.post(
            "/api/v1/sessions/session/model",
            json={"agent_id": "hermes", "provider": "ocx", "model": "gpt-5.6-sol"},
            headers=headers,
        )
        assert response.status_code == 200
        assert app.state.store.get_session_model_binding("hermes", "session") == {
            "provider": "ocx", "model": "gpt-5.6-sol",
        }
        assert client.get(
            "/api/v1/sessions/session/model?agent_id=hermes", headers=headers
        ).json() == {"provider": "ocx", "model": "gpt-5.6-sol", "effort": None}
        response = client.post(
            "/api/v1/sessions/session/reasoning",
            json={"agent_id": "hermes", "effort": "xhigh"},
            headers=headers,
        )
        assert response.status_code == 200
        assert client.get(
            "/api/v1/sessions/session/model?agent_id=hermes", headers=headers
        ).json() == {"provider": "ocx", "model": "gpt-5.6-sol", "effort": "xhigh"}

        response = client.post(
            "/api/v1/sessions/session/model",
            json={"agent_id": "codex", "provider": "openai", "model": "gpt-5.6-sol"},
            headers=headers,
        )
        assert response.status_code == 200
        assert app.state.store.get_session_model_binding("codex", "session") is None
        response = client.post(
            "/api/v1/sessions/session/reasoning",
            json={"agent_id": "codex", "effort": "medium"},
            headers=headers,
        )
        assert response.status_code == 200
        assert app.state.store.get_session_reasoning_binding("codex", "session") == "medium"
        assert client.get(
            "/api/v1/sessions/session/model?agent_id=codex", headers=headers
        ).json() == {"provider": "openai", "model": "gpt-5.6-sol", "effort": "medium"}

    reopened = Store(Settings(database_url=f"sqlite:///{tmp_path / 'api.sqlite3'}"))
    assert reopened.get_session_reasoning_binding("codex", "session") == "medium"
    reopened.close()


@pytest.mark.asyncio
async def test_restore_skips_reapplying_an_already_selected_model():
    from astrorder.main import restore_hermes_model

    class Runtime:
        def __init__(self):
            self.set_calls = []
            self.effort_calls = []

        def model(self, _session_id):
            return {"provider": "ocx", "model": "gpt-5.6-sol", "effort": "medium"}

        def set_model(self, *args):
            self.set_calls.append(args)

        def set_effort(self, *args):
            self.effort_calls.append(args)
            return {"effort": args[-1]}

    runtime = Runtime()
    app = type("App", (), {"state": type("State", (), {
        "connections": type("Connections", (), {
            "get_runtime_by_agent_id": lambda _self, _agent_id: runtime,
        })(),
    })()})()
    await restore_hermes_model(app, "hermes", "session", "ocx", "gpt-5.6-sol", "xhigh")
    assert runtime.set_calls == []
    assert runtime.effort_calls == [("session", "xhigh")]
