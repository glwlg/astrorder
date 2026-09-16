from types import SimpleNamespace

import pytest
from pydantic import ValidationError

from astrorder.api import _agent_runtime
from astrorder.schemas import AgentModel


def test_agent_kind_accepts_new_adapters_and_rejects_protocol_delimiters():
    data = {
        "id": "claude-local",
        "kind": "claude-code",
        "name": "Claude Code",
        "status": "ready",
        "capabilities": ["chat"],
    }

    assert AgentModel.model_validate(data).kind == "claude-code"
    with pytest.raises(ValidationError):
        AgentModel.model_validate({**data, "kind": "claude|code"})


def test_agent_runtime_uses_registered_resolver_and_keeps_legacy_lookup():
    registered = object()
    request = SimpleNamespace(
        app=SimpleNamespace(
            state=SimpleNamespace(
                environments=SimpleNamespace(runtime_for_agent=lambda agent_id: registered),
                connections=SimpleNamespace(get_runtime_by_agent_id=lambda agent_id: None),
                codex=None,
            )
        )
    )
    assert _agent_runtime(request, "new-agent") is registered

    legacy = object()
    request.app.state.environments = SimpleNamespace(
        for_agent=lambda agent_id: legacy if agent_id == "legacy" else None
    )
    assert _agent_runtime(request, "legacy") is legacy
