import base64

import pytest

from astrorder.daemon.hermes_runtime import HermesDaemonRuntime
from astrorder.daemon.session_daemon import SessionDaemon


@pytest.mark.asyncio
async def test_daemon_hermes_runtime_routes_inline_attachment_to_controller_boundary():
    class Controller:
        def __init__(self):
            self.inline_commands = []

        def connect(self):
            return {"state": "connected"}

        def snapshot(self):
            return {"state": "connected", "agent_id": "daemon-hermes"}

        def submit_tui_command(self, command):
            raise AssertionError(f"legacy submit must not receive inline attachment: {command}")

        def submit_tui_command_inline(self, command):
            self.inline_commands.append(dict(command))
            return "accepted", None

        def shutdown(self):
            return None

    controller = Controller()
    daemon = SessionDaemon(secret="test-only-daemon-secret")
    daemon.register_runtime("hermes", HermesDaemonRuntime(lambda: controller))
    await daemon._spawn_runtime(
        {
            "action": "session.spawn",
            "session_id": "native-session",
            "agent_type": "hermes",
            "params": {},
        }
    )

    response = await daemon._dispatch_runtime_action(
        "session.send",
        {
            "action": "session.send",
            "session_id": "native-session",
            "command_id": "command-id",
            "text": "inspect",
            "attachments": [
                {
                    "name": "notes.txt",
                    "media_type": "text/plain",
                    "content_base64": base64.b64encode(b"notes").decode("ascii"),
                }
            ],
        },
    )

    assert response["result"] == {"status": "running", "accepted": True}
    assert controller.inline_commands == [
        {
            "action": "send",
            "session_id": "native-session",
            "text": "inspect",
            "id": "command-id",
            "attachments": [
                {
                    "name": "notes.txt",
                    "media_type": "text/plain",
                    "content_base64": "bm90ZXM=",
                }
            ],
        }
    ]
