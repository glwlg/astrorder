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


def test_daemon_hermes_controller_falls_back_to_local_db_when_daemon_fails(tmp_path, monkeypatch):
    import sqlite3
    from astrorder.daemon.runtimes.hermes.control import DaemonHermesController

    state_db = tmp_path / "state.db"
    with sqlite3.connect(state_db) as conn:
        conn.execute("CREATE TABLE sessions (id TEXT PRIMARY KEY, title TEXT, cwd TEXT, started_at REAL, archived INTEGER DEFAULT 0, hidden INTEGER DEFAULT 0)")
        conn.execute("INSERT INTO sessions VALUES ('s1', 'Test', '/tmp', 1.0, 0, 0)")
        conn.execute("CREATE TABLE messages (id INTEGER PRIMARY KEY, session_id TEXT, role TEXT, content TEXT, timestamp REAL)")
        conn.execute("INSERT INTO messages VALUES (1, 's1', 'user', 'hello', 1.0)")
        conn.execute("INSERT INTO messages VALUES (2, 's1', 'assistant', 'hi there', 2.0)")

    monkeypatch.setattr(
        "astrorder.core.session_usage._find_local_hermes_state_dbs",
        lambda profile_name=None: [state_db],
    )

    class FailingBridge:
        async def request_control(self, action, fields):
            raise RuntimeError("daemon unavailable")

    controller = DaemonHermesController(FailingBridge())
    page = controller.load_native_history_page("s1", None, 50)
    assert len(page["items"]) == 2
    assert page["items"][0]["content"] == "hello"
    assert page["items"][1]["content"] == "hi there"

