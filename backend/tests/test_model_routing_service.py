import pytest

from astrorder.config import Settings
from astrorder.events import EventHub
from astrorder.service import CommandRejected, ControlService
from astrorder.store import Store


def setup_service(tmp_path):
    settings = Settings(database_url=f"sqlite:///{tmp_path / 'service.sqlite3'}")
    store = Store(settings)
    store.upsert_agent(
        {
            "id": "agent-1",
            "kind": "hermes",
            "name": "Hermes",
            "status": "ready",
            "capabilities": ["chat"],
            "limitation": None,
        }
    )
    store.upsert_session(
        {
            "id": "session-1",
            "agent_id": "agent-1",
            "title": "Session",
            "workspace": None,
            "status": "idle",
            "updated_at": "2026-09-12T00:00:00Z",
        }
    )
    return store, ControlService(store, EventHub(), settings)


def payload():
    return {
        "id": "command-1",
        "agent_id": "agent-1",
        "session_id": "session-1",
        "action": "send",
        "text": "hello",
        "attachment_ids": [],
        "target_id": None,
    }


@pytest.mark.asyncio
async def test_control_service_routes_model_before_native_send(tmp_path):
    _store, service = setup_service(tmp_path)
    order = []

    class Router:
        async def route(self, command, *, agent_kind):
            order.append(("route", command["id"], agent_kind))
            return {"tier": "small"}

    async def native(command):
        order.append(("native", command["id"]))
        return "accepted", None

    service.model_router = Router()
    service.register_native_command_handler("agent-1", native)

    result = await service.submit_browser_command(payload())

    assert result["state"] == "accepted"
    assert order == [
        ("route", "command-1", "hermes"),
        ("native", "command-1"),
    ]


@pytest.mark.asyncio
async def test_control_service_fails_command_without_native_send_when_route_fails(tmp_path):
    store, service = setup_service(tmp_path)
    native_calls = []

    class Router:
        async def route(self, command, *, agent_kind):
            del command, agent_kind
            raise RuntimeError("native model readback failed")

    async def native(command):
        native_calls.append(command)
        return "accepted", None

    service.model_router = Router()
    service.register_native_command_handler("agent-1", native)

    with pytest.raises(CommandRejected, match="模型路由未通过原生读回确认"):
        await service.submit_browser_command(payload())

    assert native_calls == []
    assert store.get_command("agent-1", "session-1", "command-1")["state"] == "failed"
