"""Strict native model/reasoning/approval controls for daemon-owned Hermes runtimes."""
from __future__ import annotations

from collections.abc import Mapping
from typing import Any

from ..native_controls import (
    current_session_approval_mode,
    current_session_model,
    current_session_reasoning,
    model_choices,
    set_session_approval_mode,
    set_session_model,
    set_session_reasoning,
)
from .session_daemon import DaemonProtocolError

NATIVE_SESSION_CONTROL_ACTIONS = frozenset(
    {
        "session.models",
        "session.model.read",
        "session.model.set",
        "session.reasoning.set",
        "session.approval.read",
        "session.approval.set",
    }
)


def execute_native_session_control(
    controller: Any, action: str, request: Mapping[str, Any]
) -> dict[str, Any]:
    if action not in NATIVE_SESSION_CONTROL_ACTIONS:
        raise DaemonProtocolError("Hermes native control action is unsupported")
    rpc = getattr(controller, "_rpc", None)
    if not callable(rpc):
        rpc = getattr(controller, "rpc", None)
    if not callable(rpc):
        raise DaemonProtocolError("Hermes native control transport is unavailable")
    session_id = request.get("session_id")
    if not isinstance(session_id, str) or not session_id:
        raise DaemonProtocolError("Hermes native control session identity is invalid")
    if action == "session.models":
        return {"status": "idle", "items": model_choices(rpc)}
    if action == "session.model.read":
        binding = current_session_model(rpc, session_id)
        binding["effort"] = current_session_reasoning(rpc, session_id)
        return {"status": "idle", **binding}
    if action == "session.model.set":
        provider, model = request.get("provider"), request.get("model")
        if not isinstance(provider, str) or not isinstance(model, str):
            raise DaemonProtocolError("Hermes model selection is invalid")
        return {"status": "idle", **set_session_model(rpc, session_id, provider, model)}
    if action == "session.reasoning.set":
        effort = request.get("effort")
        if not isinstance(effort, str):
            raise DaemonProtocolError("Hermes reasoning selection is invalid")
        return {"status": "idle", **set_session_reasoning(rpc, session_id, effort)}
    if action == "session.approval.read":
        return {"status": "idle", "mode": current_session_approval_mode(rpc, session_id)}
    if action == "session.approval.set":
        mode = request.get("mode")
        if not isinstance(mode, str):
            raise DaemonProtocolError("Hermes approval selection is invalid")
        return {"status": "idle", **set_session_approval_mode(rpc, session_id, mode)}
    raise AssertionError("unreachable native control action")
