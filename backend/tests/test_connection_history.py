from __future__ import annotations

import json

from astrorder.config import Settings
from astrorder.store import Store


def test_connection_history_is_redacted_and_cursor_paginated(tmp_path):
    store = Store(Settings(database_url=f"sqlite:///{tmp_path / 'history.sqlite3'}"))
    connection = store.save_ssh_connection(
        {"display_name": "remote", "profile_name": "default", "host": "127.0.0.1", "port": 22, "user": "tester"},
        state="saved",
        detail="saved",
    )
    connection_id = connection["id"]
    store.append_connection_history(connection_id, "deploy", "running", "token=secret-value should not be persisted")
    store.append_connection_history(connection_id, "deploy", "failed", "failed at bootstrap")
    store.append_connection_history(connection_id, "handshake", "connected", "bridge connected")

    first, cursor = store.list_connection_history(connection_id, None, 2)
    assert len(first) == 2
    assert cursor is not None
    assert first[0]["stage"] == "deploy"
    assert all("secret-value" not in entry["detail"] for entry in first)

    second, next_cursor = store.list_connection_history(connection_id, cursor, 2)
    assert next_cursor is None
    assert [entry["stage"] for entry in second] == ["deploy"]
    assert "[REDACTED]" in second[0]["detail"]
    store.close()


def test_connection_history_recursively_redacts_structured_diagnostics(tmp_path):
    store = Store(Settings(database_url=f"sqlite:///{tmp_path / 'structured-history.sqlite3'}"))
    connection = store.save_ssh_connection(
        {"display_name": "remote", "profile_name": "default", "host": "127.0.0.1", "port": 22, "user": "tester"},
        state="saved",
        detail="saved",
    )
    details = {
        "probe": {
            "token": "nested-token-value",
            "safe": "known host checked",
        },
        "attempts": [
            {"authorization": "nested-authorization-value"},
            {"message": "token=inline-token-value password: inline-password-value"},
        ],
        "private_key": "private-key-material",
    }

    row = store.append_connection_history(
        connection["id"], "handshake", "failed", "diagnostic captured", details
    )

    assert row["details"]["probe"]["token"] == "[REDACTED]"
    assert row["details"]["probe"]["safe"] == "known host checked"
    assert row["details"]["attempts"][0]["authorization"] == "[REDACTED]"
    assert row["details"]["attempts"][1]["message"] == (
        "token=[REDACTED] password=[REDACTED]"
    )
    assert row["details"]["private_key"] == "[REDACTED]"
    serialized = json.dumps(row, ensure_ascii=False)
    for secret in (
        "nested-token-value",
        "nested-authorization-value",
        "inline-token-value",
        "inline-password-value",
        "private-key-material",
    ):
        assert secret not in serialized
    store.close()
