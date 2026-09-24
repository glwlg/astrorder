"""Daemon-owned Codex App-side control projection tests."""
from __future__ import annotations

import asyncio
import threading

import pytest

from astrorder.connections import ConnectionError
from astrorder.daemon.bridge import DaemonBridgeError
from astrorder.daemon.runtimes.codex.control import DaemonCodexController


class FakeBridge:
    def __init__(self) -> None:
        self.calls: list[tuple[str, dict[str, object]]] = []
        self.status_handler = None

    def register_status_handler(self, handler):
        self.status_handler = handler
        return lambda: None

    async def request_control(self, action: str, fields: dict[str, object]) -> dict[str, object]:
        self.calls.append((action, dict(fields)))
        if action == "session.spawn":
            return {"result": {"status": "idle", "attached": True}}
        if action == "session.send":
            return {"result": {"status": "running", "turn_id": "daemon-turn-1", "accepted": True}}
        if action == "session.steer":
            return {"result": {"status": "running", "turn_id": "daemon-turn-1", "accepted": True}}
        if action == "session.interrupt":
            return {"result": {"status": "running", "turn_id": "daemon-turn-1", "accepted": True}}
        if action == "session.approve":
            return {"result": {"status": "waiting_approval", "accepted": True}}
        if action == "session.settings":
            return {
                "result": {
                    "status": "idle",
                    "accepted": True,
                    **{key: value for key, value in fields.items() if key != "session_id"},
                }
            }
        if action == "session.delete":
            return {"result": {"status": "idle", "deleted": fields["session_id"]}}
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


class ActiveDesktopBridge(FakeBridge):
    async def request_control(self, action: str, fields: dict[str, object]) -> dict[str, object]:
        self.calls.append((action, dict(fields)))
        if action == "session.spawn":
            raise DaemonBridgeError("Codex thread already has an active writer")
        if action == "runtime.request":
            return {"result": {"accepted": True, "transport": "codex-desktop-cdp"}}
        raise AssertionError(action)


class RejectedQueryBridge(FakeBridge):
    async def request_control(self, action: str, fields: dict[str, object]) -> dict[str, object]:
        raise DaemonBridgeError(
            "Codex rejected request (-32601; thread/items/list is not supported yet)"
        )


class UnavailableDesktopBridge(FakeBridge):
    async def request_control(self, action: str, fields: dict[str, object]) -> dict[str, object]:
        self.calls.append((action, dict(fields)))
        if action == "session.spawn":
            raise DaemonBridgeError("Codex thread already has an active writer")
        raise DaemonBridgeError("Codex Desktop debugging endpoint is unavailable")


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
        self._owned_threads = {"thread-1"}
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
            {
                "provider": "fixture-provider",
                "model": "fixture-model",
                "label": "Fixture model",
                "supported_efforts": ["low", "medium", "high"],
            },
            {
                "provider": "fixture-provider",
                "model": "fixture-next-model",
                "label": "Fixture next model",
                "supported_efforts": ["low", "medium", "high"],
            },
        ]

    def model(self, _session_id: str):
        return dict(self._bindings["thread-1"])

    def _pages(self, method: str, _params: dict[str, object]):
        assert method == "model/list"
        for model in ("fixture-model", "fixture-next-model"):
            yield {
                "id": model,
                "model": model,
                "supportedReasoningEfforts": [
                    {"reasoningEffort": "low"},
                    {"reasoningEffort": "medium"},
                    {"reasoningEffort": "high"},
                ],
            }

    def validate_workspace(self, workspace: str | None) -> str:
        return workspace or "C:/allowed"

    def _scope(self, session_id: str) -> None:
        if session_id not in self._threads:
            raise AssertionError(session_id)


def test_daemon_codex_controller_clears_stale_runtime_turn_after_status_refresh():
    bridge = FakeBridge()
    connection = FakeCodexConnection()
    connection._active["thread-1"] = "stale-turn"
    connection._commands[("thread-1", "stale-turn")] = {"id": "send-1"}
    controller = DaemonCodexController(bridge, FakeRouter(), connection)
    controller.activate()

    bridge.status_handler({})

    assert connection._active == {}
    assert connection._commands == {}


@pytest.mark.asyncio
async def test_daemon_codex_controller_injects_into_desktop_active_writer():
    bridge = ActiveDesktopBridge()
    controller = DaemonCodexController(bridge, FakeRouter(), FakeCodexConnection())
    command = {
        "id": "desktop-send",
        "agent_id": "daemon-codex",
        "session_id": "thread-1",
        "action": "send",
        "text": "继续",
        "attachments": [],
        "target_id": None,
    }

    assert await controller.submit(command) == ("accepted", None)
    assert bridge.calls[-1] == (
        "runtime.request",
        {
            "agent_type": "codex",
            "method": "desktop/submit",
            "request_params": {
                "threadId": "thread-1",
                "input": [{"type": "text", "text": "继续"}],
            },
            "params": {},
        },
    )


    assert controller.connection.notifications == [
        {
            "method": "turn/started",
            "params": {
                "threadId": "thread-1",
                "turn": {"id": "desktop-send", "status": "inProgress"},
            },
        }
    ]


@pytest.mark.asyncio
async def test_daemon_codex_controller_projects_file_mentions_for_existing_desktop_runtime():
    bridge = ActiveDesktopBridge()
    connection = FakeCodexConnection()
    connection.command_input = lambda _command: [
        {"type": "text", "text": "检查文件"},
        {"type": "mention", "name": "report.pdf", "path": "P:/repo/report.pdf"},
    ]
    controller = DaemonCodexController(bridge, FakeRouter(), connection)
    command = {
        "id": "desktop-file",
        "agent_id": "daemon-codex",
        "session_id": "thread-1",
        "action": "send",
        "text": "检查文件",
        "attachments": [{"id": "attachment-1"}],
        "target_id": None,
    }
    assert await controller.submit(command) == ("accepted", None)
    assert bridge.calls[-1][1]["request_params"]["input"] == [
        {"type": "text", "text": "[report.pdf](<P:/repo/report.pdf>)\n检查文件"}
    ]


@pytest.mark.asyncio
async def test_daemon_codex_controller_reports_unavailable_desktop_cdp():
    controller = DaemonCodexController(
        UnavailableDesktopBridge(), FakeRouter(), FakeCodexConnection()
    )
    command = {
        "id": "desktop-send",
        "agent_id": "daemon-codex",
        "session_id": "thread-1",
        "action": "send",
        "text": "继续",
        "attachments": [],
        "target_id": None,
    }

    assert await controller.submit(command) == (
        "failed",
        "Codex Desktop 当前未开放 CDP；请通过 Codex CDP 快捷方式启动。",
    )


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
                        "model": "fixture-model",
                    },
                },
            ),
        ]
        assert connection._commands[("thread-1", "daemon-turn-1")]["id"] == "send-1"
        assert connection._active["thread-1"] == "daemon-turn-1"
        assert connection._pending == {}

        steer = {**send, "id": "steer-1", "text": "补充要求"}
        assert await controller.submit(steer) == ("accepted", None)
        assert bridge.calls[2] == (
            "session.steer",
            {
                "session_id": "thread-1",
                "turn_id": "daemon-turn-1",
                "input": [{"type": "text", "text": "补充要求"}],
            },
        )

        stop = {**send, "id": "stop-1", "action": "stop", "text": "", "target_id": "thread-1"}
        assert await controller.submit(stop) == ("accepted", None)
        assert bridge.calls[3] == (
            "session.interrupt",
            {"session_id": "thread-1", "turn_id": "daemon-turn-1"},
        )
        assert connection._stop_commands[("thread-1", "daemon-turn-1")][0]["id"] == "stop-1"

        approve = {**send, "id": "approve-1", "action": "approve", "text": "", "target_id": "ui-approval"}
        assert await controller.submit(approve) == ("accepted", None)
        assert bridge.calls[4] == (
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
        asyncio.run(asyncio.sleep(0))
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
                    "model": "fixture-model",
                },
            },
        )
    finally:
        controller.close()


def test_daemon_codex_controller_updates_model_and_effort_after_daemon_confirmation():
    bridge = FakeBridge()
    router = FakeRouter()
    connection = FakeCodexConnection()
    controller = DaemonCodexController(bridge, router, connection)

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


def test_daemon_codex_controller_updates_desktop_effort_for_active_writer():
    bridge = ActiveDesktopBridge()
    connection = FakeCodexConnection()
    controller = DaemonCodexController(bridge, FakeRouter(), connection)

    assert controller.set_effort("thread-1", "high") == {"effort": "high"}
    assert bridge.calls == [
        ("session.spawn", {"session_id": "thread-1", "agent_type": "codex", "cwd": "C:/allowed", "params": {}}),
        (
            "runtime.request",
            {
                "agent_type": "codex",
                "method": "desktop/settings",
                "request_params": {
                    "threadId": "thread-1",
                    "updates": {"effort": "high", "effortIndex": 2},
                },
                "params": {},
            },
        ),
    ]


def test_daemon_codex_controller_uses_desktop_model_picker_for_active_writer():
    bridge = ActiveDesktopBridge()
    controller = DaemonCodexController(bridge, FakeRouter(), FakeCodexConnection())

    assert controller.set_model("thread-1", "fixture-provider", "fixture-next-model") == {
        "model": "fixture-next-model",
        "provider": "fixture-provider",
    }
    assert bridge.calls == [
        ("session.spawn", {"session_id": "thread-1", "agent_type": "codex", "cwd": "C:/allowed", "params": {}}),
        (
            "runtime.request",
            {
                "agent_type": "codex",
                "method": "desktop/settings",
                "request_params": {
                    "threadId": "thread-1",
                    "updates": {"model": "fixture-next-model", "modelLabel": "Fixture next model"},
                },
                "params": {},
            },
        ),
    ]


def test_daemon_codex_controller_preserves_native_query_rejection():
    controller = DaemonCodexController(
        RejectedQueryBridge(), FakeRouter(), FakeCodexConnection()
    )

    with pytest.raises(ConnectionError, match=r"-32601.*not supported") as rejected:
        controller.request_native("thread/items/list", {"threadId": "thread-1"})

    assert rejected.value.status_code == 422


def test_daemon_codex_controller_deletes_through_the_owning_daemon():
    bridge = FakeBridge()
    connection = FakeCodexConnection()
    controller = DaemonCodexController(bridge, FakeRouter(), connection)

    controller.delete("thread-1")

    assert bridge.calls == [("session.delete", {"session_id": "thread-1"})]
    assert "thread-1" not in connection._threads


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


def test_daemon_codex_controller_routes_remote_connection_through_exact_ssh_runtime():
    bridge = FakeBridge()
    connection = FakeCodexConnection()
    connection.agent_id = "ssh-codex-ssh-debian"
    connection.connection_id = "ssh-debian"
    connection.display_name = "Debian"
    connection.remote_executable = "/home/operator/.local/bin/codex"
    connection.ssh_settings = {
        "display_name": "Debian",
        "host": "debian.example",
        "port": 22,
        "user": "operator",
    }
    connection._threads = {"thread-1": {"cwd": "/home/operator/workspace/project"}}
    connection.validate_workspace = lambda workspace: workspace or "/home/operator/workspace/project"
    controller = DaemonCodexController(bridge, FakeRouter(), connection)

    created = controller.create("/home/operator/workspace/project", "Remote daemon thread")

    assert created["agent_id"] == "ssh-codex-ssh-debian"
    assert created["connection_id"] == "ssh-debian"
    assert bridge.calls == [
        (
            "session.create",
            {
                "agent_type": "codex-ssh",
                "cwd": "/home/operator/workspace/project",
                "ephemeral": False,
                "title": "Remote daemon thread",
                "params": {
                    "connection_id": "ssh-debian",
                    "ssh_settings": {
                        "display_name": "Debian",
                        "host": "debian.example",
                        "port": 22,
                        "user": "operator",
                        "codex_executable": "/home/operator/.local/bin/codex",
                    },
                },
            },
        )
    ]
