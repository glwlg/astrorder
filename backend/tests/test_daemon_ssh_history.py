from astrorder.daemon.ssh_control import DaemonSshController


def test_daemon_ssh_controller_reads_remote_history_page_through_exact_connection_binding():
    class FakeBridge:
        def __init__(self) -> None:
            self.calls: list[tuple[str, dict[str, object]]] = []

        async def request_control(self, action, fields):
            self.calls.append((action, dict(fields)))
            if action == "session.spawn":
                return {
                    "result": {
                        "status": "idle",
                        "agent_id": "ssh-hermes-remote-a",
                        "connection_id": "remote-a",
                    }
                }
            if action == "session.history_page":
                return {
                    "result": {
                        "status": "idle",
                        "items": [{"id": "remote-native-row", "type": "assistant"}],
                        "next_cursor": None,
                    }
                }
            raise AssertionError(f"unexpected daemon action: {action}")

    bridge = FakeBridge()
    settings = {"host": "remote.example", "port": 22}
    controller = DaemonSshController(bridge, connection_id="remote-a", ssh_settings=settings)
    controller.start()

    page = controller.load_native_history_page("native-remote-session", None, 30)

    assert page == {
        "items": [{"id": "remote-native-row", "type": "assistant"}],
        "next_cursor": None,
    }
    assert bridge.calls[1:] == [
        (
            "session.spawn",
            {
                "session_id": "native-remote-session",
                "agent_type": "ssh",
                "params": {"connection_id": "remote-a", "ssh_settings": settings},
            },
        ),
        (
            "session.history_page",
            {"session_id": "native-remote-session", "before": None, "limit": 30},
        ),
    ]
