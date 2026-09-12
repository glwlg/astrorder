import pytest

from astrorder.config import Settings
from astrorder.model_routing import ModelRouter
from astrorder.store import Store


def routed_settings(tmp_path):
    return Settings(
        database_url=f"sqlite:///{tmp_path / 'router.sqlite3'}",
        model_routing_enabled=True,
        model_routing_daily_budget_units=5,
        model_routing_large_text_threshold=10,
        hermes_small_model="ocx/small/model",
        hermes_large_model="ocx/large/model",
        codex_small_model="opencodex/small",
        codex_large_model="opencodex/large",
    )


@pytest.mark.asyncio
async def test_model_router_applies_confirmed_target_then_records_route(tmp_path):
    settings = routed_settings(tmp_path)
    store = Store(settings)
    applied = []

    async def apply(agent_id, session_id, target):
        applied.append((agent_id, session_id, target))

    router = ModelRouter(store, settings, apply)
    command = {
        "id": "command-1",
        "agent_id": "agent-1",
        "session_id": "session-1",
        "action": "send",
        "text": "short",
        "attachments": [],
    }

    route = await router.route(command, agent_kind="hermes")

    assert route["tier"] == "small"
    assert route["provider"] == "ocx"
    assert route["model"] == "small/model"
    assert len(applied) == 1
    assert store.latest_model_route("agent-1", "session-1")["command_id"] == "command-1"


@pytest.mark.asyncio
async def test_model_router_does_not_charge_failed_native_switch(tmp_path):
    settings = routed_settings(tmp_path)
    store = Store(settings)

    async def apply(*_args):
        raise RuntimeError("native readback failed")

    router = ModelRouter(store, settings, apply)
    command = {
        "id": "command-1",
        "agent_id": "agent-1",
        "session_id": "session-1",
        "action": "send",
        "text": "long input",
        "attachments": [],
    }

    with pytest.raises(RuntimeError, match="readback"):
        await router.route(command, agent_kind="hermes")

    assert store.latest_model_route("agent-1", "session-1") is None


@pytest.mark.asyncio
async def test_model_router_retries_only_native_control_before_command_dispatch(tmp_path):
    settings = routed_settings(tmp_path)
    store = Store(settings)
    attempts = 0

    async def apply(*_args):
        nonlocal attempts
        attempts += 1
        if attempts == 1:
            raise RuntimeError("session not ready")

    router = ModelRouter(store, settings, apply, retry_delay=0)
    command = {
        "id": "command-1",
        "agent_id": "agent-1",
        "session_id": "session-1",
        "action": "send",
        "text": "short",
        "attachments": [],
    }

    route = await router.route(command, agent_kind="hermes")

    assert attempts == 2
    assert route["tier"] == "small"
    assert store.model_routing_units("agent-1", route_day(route)) == 1


def route_day(route):
    from astrorder.timeutil import parse_timestamp

    created = parse_timestamp(route["created_at"])
    return created.replace(hour=0, minute=0, second=0, microsecond=0)
