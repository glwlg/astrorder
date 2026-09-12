"""Daemon-owned Codex App-side control projection tests."""
from __future__ import annotations

import threading

import pytest

from astrorder.daemon.codex_control import DaemonCodexController


class FakeBridge:
    def __init__(self) -> None:
        self.calls: list[tuple[str, dict[str, object]]] = []
        self.on_settings = None

    async def request_control(self, action: str, fields: dict[str, object]) -> dict[str, object]:
        self.calls.append((action, dict(fields)))
        if action == "session.spawn":
            return {"result": {"status": "idle", "attached": True}}
        if action == "session.send":
            return {"result": {"status": "running", "turn_id": "daemon-turn-1", "accepted": True}}
        if action == "session.interrupt":
            return {"result": {"status": "running", "turn_id": "daemon-turn-1", "accepted": True}}
        if action == "session.approve":
            return {"result": {"status": "waiting_approval", "accepted": True}}
        if action == "session.settings":
            if self.on_settings is not None:
                self.on_settings(fields)
            return {"result": {"status": "idle", "accepted": True}}
        if action == "session.create":
            return {
                "result": {
                    "session_id": "daemon-created-thread",
                    "status": "idle",
                    "model": "fixture-model",
                    "provider": "fixture-provider",
                }
            }
        raise AssertionError(action)


class FakeRouter:
    def __init__(self) -> None:
        self.agent_id: str | None = None
        self.handler = None

    def register(self, agent_id, handler):
        self.agent_id = agent_id
        self.handler = handler

        def unregister():
            self.handler = None

        return unregister


class FakeCodexConnection:
    agent_id = "daemon-codex"
    state = "connected"

    def __init__(self) -> None:
        self._lock = threading.RLock()
        self._threads = {"thread-1": {"cwd": "C:/allowed"}}
        self._pending: dict[str, dict[str, object]] = {}
        self._commands: dict[tuple[str, str], dict[str, object]] = {}
        self._active: dict[str, str] = {}
        self._stop_commands: dict[tuple[str, str], list[dict[str, object]]] = {}
        self._approvals = {
            "ui-approval": {
                "native_id": 71,
                "session_id": "thread-1",
                "data": {"id": "ui-approval"},
            }
        }
        self._approval_commands: dict[tuple[str, int], tuple[dict[str, object], dict[str, object]]] = {}
        self._bindings = {"thread-1": {"model": "fixture-model", "provider": "fixture-provider"}}
        self._efforts: dict[str, str] = {}
        self._binding_changed = threading.Condition(self._lock)
        self.notifications: list[dict[str, object]] = []

    def get_approval_mode(self, _session_id: str) -> str:
        return "auto"

    def _notification(self, frame: dict[str, object]) -> None:
        self.notifications.append(dict(frame))

    def models(self, _session_id: str):
        return [
            {"provider": "fixture-provider", "model": "fixture-model"},
            {"provider": "fixture-provider", "model": "fixture-next-model"},
        ]

    def validate_workspace(self, workspace: str | None) -> str:
        return workspace or "C:/allowed"


@pytest.mark.asyncio
async def test_daemon_codex_controller_routes_exact_command_and_projection_identity():
    bridge = FakeBridge()
    router = FakeRouter()
    connection = FakeCodexConnection()
    controller = DaemonCodexController(bridge, router, connection)
    controller.activate()
    send = {
        "id": "send-1",
        "agent_id": "daemon-codex",
        "session_id": "thread-1",
        "action": "send",
        "text": "same text is not an identity",
        "attachments": [],
        "target_id": None,
    }
    try:
        assert await controller.submit(send) == ("accepted", None)
        assert bridge.calls[:2] == [
            (
                "session.spawn",
                {
                    "session_id": "thread-1",
                    "agent_type": "codex",
                    "cwd": "C:/allowed",
                    "params": {},
                },
            ),
            (
                "session.send",
                {
                    "session_id": "thread-1",
                    "input": [{"type": "text", "text": "same text is not an identity"}],
                    "params": {
                        "approvalPolicy": "on-request",
                        "sandboxPolicy": {"type": "workspaceWrite"},
                        "approvalsReviewer": "auto_review",
                    },
                },
            ),
        ]
        assert connection._commands[("thread-1", "daemon-turn-1")]["id"] == "send-1"
        assert connection._active["thread-1"] == "daemon-turn-1"
        assert connection._pending == {}

        stop = {**send, "id": "stop-1", "action": "stop", "text": "", "target_id": "thread-1"}
        assert await controller.submit(stop) == ("accepted", None)
        assert bridge.calls[2] == (
            "session.interrupt",
            {"session_id": "thread-1", "turn_id": "daemon-turn-1"},
        )
        assert connection._stop_commands[("thread-1", "daemon-turn-1")][0]["id"] == "stop-1"

        approve = {**send, "id": "approve-1", "action": "approve", "text": "", "target_id": "ui-approval"}
        assert await controller.submit(approve) == ("accepted", None)
        assert bridge.calls[3] == (
            "session.approve",
            {"session_id": "thread-1", "approval_id": "codex:71", "decision": "accept"},
        )
        assert ("thread-1", 71) in connection._approval_commands

        assert router.agent_id == "daemon-codex"
        router.handler({"method": "turn/completed", "params": {"threadId": "thread-1"}})
        assert connection.notifications == [
            {"method": "turn/completed", "params": {"threadId": "thread-1"}}
        ]
    finally:
        controller.close()


@pytest.mark.asyncio
async def test_daemon_codex_controller_stages_attachment_inputs_before_ipc(monkeypatch):
    bridge = FakeBridge()
    router = FakeRouter()
    connection = FakeCodexConnection()
    controller = DaemonCodexController(bridge, router, connection)
    captured: list[dict[str, object]] = []

    def input_for_command(_settings, _store, command):
        captured.append(dict(command))
        return [
            {"type": "text", "text": command["text"]},
            {"type": "image", "url": "data:image/png;base64,aGVsbG8="},
        ]

    monkeypatch.setattr("astrorder.daemon.codex_control.command_input", input_for_command)
    connection.settings = object()
    connection.store = object()
    command = {
        "id": "send-image",
        "agent_id": "daemon-codex",
        "session_id": "thread-1",
        "action": "send",
        "text": "inspect image",
        "attachments": [{"id": "attachment-1"}],
        "target_id": None,
    }
    try:
        assert await controller.submit(command) == ("accepted", None)
        assert captured == [command]
        assert bridge.calls[1] == (
            "session.send",
            {
                "session_id": "thread-1",
                "input": [
                    {"type": "text", "text": "inspect image"},
                    {"type": "image", "url": "data:image/png;base64,aGVsbG8="},
                ],
                "params": {
                    "approvalPolicy": "on-request",
                    "sandboxPolicy": {"type": "workspaceWrite"},
                    "approvalsReviewer": "auto_review",
                },
            },
        )
    finally:
        controller.close()


def test_daemon_codex_controller_updates_model_and_effort_only_after_projection():
    bridge = FakeBridge()
    router = FakeRouter()
    connection = FakeCodexConnection()
    controller = DaemonCodexController(bridge, router, connection)

    def project_settings(fields):
        with connection._binding_changed:
            if "model" in fields:
                connection._bindings["thread-1"] = {
                    "model": fields["model"],
                    "provider": "fixture-provider",
                }
            if "effort" in fields:
                connection._efforts["thread-1"] = fields["effort"]
            connection._binding_changed.notify_all()

    bridge.on_settings = project_settings
    assert controller.set_model("thread-1", "fixture-provider", "fixture-next-model") == {
        "model": "fixture-next-model",
        "provider": "fixture-provider",
    }
    assert controller.set_effort("thread-1", "high") == {"effort": "high"}
    assert bridge.calls == [
        (
            "session.spawn",
            {"session_id": "thread-1", "agent_type": "codex", "cwd": "C:/allowed", "params": {}},
        ),
        ("session.settings", {"session_id": "thread-1", "model": "fixture-next-model"}),
        (
            "session.spawn",
            {"session_id": "thread-1", "agent_type": "codex", "cwd": "C:/allowed", "params": {}},
        ),
        ("session.settings", {"session_id": "thread-1", "effort": "high"}),
    ]


def test_daemon_codex_controller_creates_a_session_from_daemon_native_identity():
    bridge = FakeBridge()
    connection = FakeCodexConnection()
    controller = DaemonCodexController(bridge, FakeRouter(), connection)

    created = controller.create("C:/allowed", "Daemon thread", ephemeral=True)

    assert created["id"] == "daemon-created-thread"
    assert created["agent_id"] == "daemon-codex"
    assert created["workspace"] == "C:/allowed"
    assert created["title"] == "Daemon thread"
    assert created["ephemeral"] is True
    assert connection._threads["daemon-created-thread"]["cwd"] == "C:/allowed"
    assert connection._bindings["daemon-created-thread"] == {
        "model": "fixture-model",
        "provider": "fixture-provider",
    }
    assert bridge.calls == [
        (
            "session.create",
            {
                "agent_type": "codex",
                "cwd": "C:/allowed",
                "ephemeral": True,
                "title": "Daemon thread",
            },
        )
    ]
