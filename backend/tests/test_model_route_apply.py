from types import SimpleNamespace

import pytest

from astrorder.main import apply_model_route
from astrorder.model_routing import ModelRouteTarget


@pytest.mark.asyncio
async def test_apply_model_route_uses_exact_hermes_runtime():
    calls = []

    class Runtime:
        def set_model(self, session_id, provider, model):
            calls.append(("model", session_id, provider, model))
            return {"provider": provider, "model": model}

        def set_effort(self, session_id, effort):
            calls.append(("effort", session_id, effort))
            return {"effort": effort}

    runtime = Runtime()
    app = SimpleNamespace(
        state=SimpleNamespace(
            store=SimpleNamespace(get_agent=lambda agent_id: {"id": agent_id, "kind": "hermes"}),
            connections=SimpleNamespace(get_runtime_by_agent_id=lambda agent_id: runtime),
            codex=None,
        )
    )

    await apply_model_route(
        app,
        "agent-1",
        "session-1",
        ModelRouteTarget("ocx", "gpt-5.6-sol", "high"),
    )

    assert calls == [
        ("model", "session-1", "ocx", "gpt-5.6-sol"),
        ("effort", "session-1", "high"),
    ]


@pytest.mark.asyncio
async def test_apply_model_route_uses_exact_remote_codex_runtime():
    calls = []

    class Runtime:
        def set_model(self, session_id, provider, model):
            calls.append(("model", session_id, provider, model))
            return {"provider": provider, "model": model}

        def set_effort(self, session_id, effort):
            calls.append(("effort", session_id, effort))
            return {"effort": effort}

    remote = Runtime()
    local = SimpleNamespace(
        set_model=lambda *_args: pytest.fail("local Codex must not receive remote route")
    )
    app = SimpleNamespace(
        state=SimpleNamespace(
            store=SimpleNamespace(
                get_agent=lambda agent_id: {"id": agent_id, "kind": "codex"}
            ),
            environments=SimpleNamespace(
                for_agent=lambda agent_id: remote if agent_id == "remote-codex" else None
            ),
            connections=None,
            codex=local,
        )
    )

    await apply_model_route(
        app,
        "remote-codex",
        "thread-1",
        ModelRouteTarget("opencodex", "gpt-6-astra", "high"),
    )

    assert calls == [
        ("model", "thread-1", "opencodex", "gpt-6-astra"),
        ("effort", "thread-1", "high"),
    ]
