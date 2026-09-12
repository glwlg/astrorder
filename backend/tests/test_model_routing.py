import pytest

from astrorder.model_routing import ModelRouteTarget, ModelRoutingPolicy


@pytest.fixture
def policy():
    return ModelRoutingPolicy(
        small=ModelRouteTarget("ocx", "gpt-5.6-luna--fast", "low"),
        large=ModelRouteTarget("ocx", "gpt-5.6-sol", "high"),
        daily_budget_units=10,
        small_cost_units=1,
        large_cost_units=5,
        large_text_threshold=100,
    )


def test_model_routing_uses_small_for_simple_turn(policy):
    decision = policy.decide(
        {"action": "send", "text": "short", "attachments": []},
        spent_units=0,
        previous_state="completed",
    )

    assert decision.tier == "small"
    assert decision.target.model == "gpt-5.6-luna--fast"
    assert decision.cost_units == 1
    assert decision.reason == "simple"


def test_model_routing_upgrades_for_attachment_or_long_turn(policy):
    attachment = policy.decide(
        {"action": "send", "text": "inspect", "attachments": [{"id": "a"}]},
        spent_units=0,
        previous_state="completed",
    )
    long_turn = policy.decide(
        {"action": "send", "text": "x" * 100, "attachments": []},
        spent_units=0,
        previous_state="completed",
    )

    assert (attachment.tier, attachment.reason) == ("large", "attachment")
    assert (long_turn.tier, long_turn.reason) == ("large", "long_input")


def test_model_routing_upgrades_next_turn_after_failed_or_unknown(policy):
    for state in ("failed", "unknown"):
        decision = policy.decide(
            {"action": "send", "text": "retry context", "attachments": []},
            spent_units=0,
            previous_state=state,
        )
        assert (decision.tier, decision.reason) == ("large", "previous_unconfirmed")


def test_model_routing_downgrades_to_small_when_large_would_exceed_budget(policy):
    decision = policy.decide(
        {"action": "send", "text": "x" * 100, "attachments": []},
        spent_units=6,
        previous_state="completed",
    )

    assert decision.tier == "small"
    assert decision.reason == "budget_guard"
    assert decision.cost_units == 1


def test_model_routing_rejects_invalid_budget_and_ignores_non_send():
    with pytest.raises(ValueError, match="budget"):
        ModelRoutingPolicy(
            small=ModelRouteTarget("p", "small", "low"),
            large=ModelRouteTarget("p", "large", "high"),
            daily_budget_units=0,
        )
    policy = ModelRoutingPolicy(
        small=ModelRouteTarget("p", "small", "low"),
        large=ModelRouteTarget("p", "large", "high"),
        daily_budget_units=10,
    )
    assert policy.decide(
        {"action": "stop", "text": "", "attachments": []},
        spent_units=0,
        previous_state=None,
    ) is None
