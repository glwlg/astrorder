from astrorder.config import Settings
from astrorder.store import Store
from astrorder.timeutil import parse_timestamp


def test_model_route_ledger_is_idempotent_and_sums_daily_units(tmp_path):
    store = Store(Settings(database_url=f"sqlite:///{tmp_path / 'routes.sqlite3'}"))
    route = {
        "agent_id": "agent-a",
        "session_id": "session-a",
        "command_id": "command-a",
        "tier": "large",
        "provider": "ocx",
        "model": "gpt-5.6-sol",
        "effort": "high",
        "reason": "attachment",
        "cost_units": 5,
    }
    when = parse_timestamp("2026-09-12T01:00:00Z")
    day_start = parse_timestamp("2026-09-12T00:00:00Z")

    first, created = store.record_model_route(route, created_at=when)
    second, duplicate_created = store.record_model_route(route, created_at=when)

    assert created is True
    assert duplicate_created is False
    assert first == second
    assert store.model_routing_units("agent-a", day_start) == 5
    assert store.model_routing_units("agent-b", day_start) == 0
    assert store.latest_model_route("agent-a", "session-a")["command_id"] == "command-a"
