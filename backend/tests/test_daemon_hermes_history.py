from astrorder.daemon.runtimes.hermes.control import DaemonHermesController


def test_daemon_hermes_controller_reads_native_history_page_through_exact_session_binding():
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
            if action == "session.history_page":
                return {
                    "result": {
                        "status": "idle",
                        "items": [{"id": "native-row-1", "type": "assistant"}],
                        "next_cursor": "native:next",
                    }
                }
            raise AssertionError(f"unexpected daemon action: {action}")

    bridge = FakeBridge()
    controller = DaemonHermesController(bridge)
    controller.connect()

    page = controller.load_native_history_page(
        "native-hermes-session-id",
        "native:before",
        30,
    )

    assert page == {
        "items": [{"id": "native-row-1", "type": "assistant"}],
        "next_cursor": "native:next",
    }
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
            "session.history_page",
            {
                "session_id": "native-hermes-session-id",
                "before": "native:before",
                "limit": 30,
            },
        ),
    ]
