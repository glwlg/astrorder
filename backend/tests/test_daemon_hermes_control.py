from __future__ import annotations

import asyncio
from datetime import datetime
from types import SimpleNamespace

import pytest

from astrorder.daemon.runtimes.hermes.control import DaemonHermesController, hermes_runtime_control_id


def test_daemon_hermes_controller_connects_and_creates_from_daemon_confirmed_identity():
    class FakeBridge:
        def __init__(self) -> None:
            self.calls: list[tuple[str, dict[str, object]]] = []

        async def request_control(self, action, fields):
            self.calls.append((action, dict(fields)))
            if action == "session.spawn":
                return {
                    "result": {
                        "status": "idle",
                        "agent_id": "local-hermes-default",
                        "source_id": "hermes-local-opaque-source",
                        "profile_name": "default",
                        "runtime_id": "local-hermes-default",
                    }
                }
            if action == "session.create":
                return {
                    "result": {
                        "session_id": "native-hermes-session-id",
                        "status": "idle",
                        "agent_id": "local-hermes-default",
                        "source_id": "hermes-local-opaque-source",
                        "profile_name": "default",
                    }
                }
            raise AssertionError(f"unexpected daemon action: {action}")

    bridge = FakeBridge()
    controller = DaemonHermesController(bridge)

    assert controller.snapshot()["available"] is True

    connected = controller.connect()
    created = controller.create_session(
        "C:/workspace/project",
        "Daemon owned Hermes",
        provider="ocx",
        model="google-antigravity/gemini-3.8-flash",
        effort="high",
    )
    updated_at = created.pop("updated_at")

    assert datetime.fromisoformat(updated_at).tzinfo is not None
    assert connected["state"] == "connecting"
    assert connected["agent_id"] == "local-hermes-default"
    assert connected["source_id"] == "hermes-local-opaque-source"
    assert created == {
        "id": "native-hermes-session-id",
        "agent_id": "local-hermes-default",
        "title": "Daemon owned Hermes",
        "workspace": "C:/workspace/project",
        "status": "idle",
        "source_id": "hermes-local-opaque-source",
        "connection_id": None,
        "source_session_id": "native-hermes-session-id",
        "history_state": "live",
        "control_state": "owned",
    }
    assert bridge.calls == [
        (
            "session.spawn",
            {
                "session_id": hermes_runtime_control_id("local"),
                "agent_type": "hermes",
                "params": {"runtime_control": True},
            },
        ),
        (
            "session.create",
            {
                "agent_type": "hermes",
                "cwd": "C:/workspace/project",
                "title": "Daemon owned Hermes",
                "provider": "ocx",
                "model": "google-antigravity/gemini-3.8-flash",
                "effort": "high",
            },
        ),
    ]


@pytest.mark.asyncio
async def test_daemon_hermes_controller_submits_only_after_exact_session_binding():
    class FakeBridge:
        def __init__(self) -> None:
            self.calls: list[tuple[str, dict[str, object]]] = []

        async def request_control(self, action, fields):
            self.calls.append((action, dict(fields)))
            if action == "session.spawn":
                return {
                    "result": {
                        "status": "idle",
                        "agent_id": "local-hermes-default",
                        "source_id": "hermes-local-opaque-source",
                    }
                }
            if action == "session.send":
                return {"result": {"status": "running", "accepted": True}}
            raise AssertionError(f"unexpected daemon action: {action}")

    bridge = FakeBridge()
    controller = DaemonHermesController(bridge)
    await asyncio.to_thread(controller.connect)

    state, detail = await controller.submit_tui_command(
        {
            "id": "command-id",
            "agent_id": "local-hermes-default",
            "session_id": "native-hermes-session-id",
            "action": "send",
            "text": "exact daemon-bound prompt",
            "attachments": [],
        }
    )

    assert (state, detail) == ("accepted", None)
    assert bridge.calls[1:] == [
        (
            "session.spawn",
            {
                "session_id": "native-hermes-session-id",
                "agent_type": "hermes",
                "params": {},
            },
        ),
        (
            "session.send",
            {
                "session_id": "native-hermes-session-id",
                "text": "exact daemon-bound prompt",
                "command_id": "command-id",
            },
        ),
    ]


@pytest.mark.asyncio
async def test_daemon_hermes_controller_packs_registered_attachment_without_path(tmp_path):
    blob = tmp_path / "stored.blob"
    blob.write_bytes(b"attachment-bytes")

    class Store:
        def get_attachment(self, attachment_id):
            assert attachment_id == "attachment-1"
            return {
                "id": attachment_id,
                "name": "notes.txt",
                "media_type": "text/plain",
                "size": len(b"attachment-bytes"),
                "storage_name": blob.name,
            }

    class FakeBridge:
        def __init__(self):
            self.calls = []

        async def request_control(self, action, fields):
            self.calls.append((action, dict(fields)))
            if action == "session.spawn":
                return {
                    "result": {
                        "status": "idle",
                        "agent_id": "local-hermes-default",
                        "source_id": "hermes-local-opaque-source",
                    }
                }
            if action == "session.send":
                return {"result": {"status": "running", "accepted": True}}
            raise AssertionError(action)

    bridge = FakeBridge()
    controller = DaemonHermesController(bridge)
    controller.store = Store()
    controller.app_settings = SimpleNamespace(
        attachments_dir=tmp_path,
        max_attachment_size=1024,
        allowed_attachment_types=("text/plain",),
    )
    await asyncio.to_thread(controller.connect)

    state, detail = await controller.submit_tui_command(
        {
            "id": "command-id",
            "agent_id": "local-hermes-default",
            "session_id": "native-hermes-session-id",
            "action": "send",
            "text": "inspect attachment",
            "attachments": [{"id": "attachment-1"}],
        }
    )

    assert (state, detail) == ("accepted", None)
    sent = bridge.calls[-1][1]
    assert sent["attachments"] == [
        {
            "name": "notes.txt",
            "media_type": "text/plain",
            "content_base64": "YXR0YWNobWVudC1ieXRlcw==",
        }
    ]
    assert "path" not in str(sent)


def test_daemon_hermes_controller_renames_only_after_native_readback():
    class FakeBridge:
        def __init__(self):
            self.calls = []

        async def request_control(self, action, fields):
            self.calls.append((action, dict(fields)))
            if action == "session.spawn":
                return {
                    "result": {
                        "status": "idle",
                        "agent_id": "local-hermes-default",
                        "source_id": "hermes-local-opaque-source",
                    }
                }
            if action == "session.rename":
                return {"result": {"status": "idle", "title": "Renamed"}}
            raise AssertionError(action)

    bridge = FakeBridge()
    controller = DaemonHermesController(bridge)
    controller.connect()

    controller.mutate_session("native-session", {"title": "Renamed"})

    assert bridge.calls[-2:] == [
        (
            "session.spawn",
            {
                "session_id": "native-session",
                "agent_type": "hermes",
                "params": {},
            },
        ),
        (
            "session.rename",
            {"session_id": "native-session", "title": "Renamed"},
        ),
    ]

def test_daemon_hermes_controller_branches_through_daemon_create():
    class FakeBridge:
        def __init__(self):
            self.calls = []

        async def request_control(self, action, fields):
            self.calls.append((action, dict(fields)))
            if action == "session.spawn":
                return {
                    "result": {
                        "status": "idle",
                        "agent_id": "local-hermes-default",
                        "source_id": "hermes-source",
                    }
                }
            return {
                "result": {
                    "session_id": "summary-fork",
                    "status": "idle",
                    "agent_id": "local-hermes-default",
                    "source_id": "hermes-source",
                }
            }

    bridge = FakeBridge()
    controller = DaemonHermesController(bridge)
    controller.connect()

    fork = controller.branch_session("source-session", "转交摘要")

    assert fork["id"] == "summary-fork"
    assert bridge.calls[-1] == (
        "session.create",
        {
            "agent_type": "hermes",
            "parent_session_id": "source-session",
            "title": "转交摘要",
        },
    )
