import json
import sqlite3
import pytest
from pathlib import Path
from unittest.mock import MagicMock, patch

from astrorder.config import Settings
from astrorder.store import Store
from astrorder.core.session_usage import (
    resolve_model_context_window,
    _parse_hermes_row,
    _parse_codex_rollout_file,
    _parse_grok_session,
    resolve_session_usage,
)


@pytest.fixture(autouse=True)
def isolated_model_metadata(monkeypatch):
    # These values belong to this fixture, not the user's changing native config.
    module = "astrorder.core.session_usage"
    for reader in ("_read_hermes_context_window", "_read_codex_context_window", "_read_grok_context_window"):
        monkeypatch.setattr(f"{module}.{reader}", lambda *args, **kwargs: None)
    monkeypatch.setattr(f"{module}._get_gateway_models_context_windows", lambda *args: {
        "google-antigravity/gemini-3.8-flash": 350_000,
        "kimi-code/k3": 262_144,
    })


def test_resolve_model_context_window():
    assert resolve_model_context_window("google-antigravity/gemini-3.8-flash") == 350_000
    assert resolve_model_context_window("gemini-3.8-flash") == 350_000
    assert resolve_model_context_window("kimi-code/k3") == 262_144
    assert resolve_model_context_window("nonexistent-model-xyz") is None
    assert resolve_model_context_window(None) is None


def test_parse_hermes_row_with_usage_anchor():
    row = {
        "model": "google-antigravity/gemini-3.8-flash",
        "model_config": json.dumps({
            "_usage_anchor": {
                "prompt_tokens": 150000,
                "completion_tokens": 500,
            }
        }),
        "input_tokens": 5000000,
        "cache_read_tokens": 45000000,
        "output_tokens": 60000,
        "reasoning_tokens": 80000,
    }
    parsed = _parse_hermes_row(row)
    assert parsed["model"] == "google-antigravity/gemini-3.8-flash"
    assert parsed["context_window"] == 350_000
    assert parsed["last_input_tokens"] == 150000
    assert parsed["used_percentage"] == round(150000 / 350000 * 100, 2)
    assert parsed["input_tokens"] == 5000000
    assert parsed["cached_tokens"] == 45000000
    assert parsed["output_tokens"] == 60000
    assert parsed["reasoning_tokens"] == 80000
    assert parsed["total_tokens"] == 5000000 + 45000000 + 60000
    assert parsed["cache_hit_rate"] == 90.0


def test_parse_codex_rollout_file(tmp_path: Path):
    rollout_file = tmp_path / "rollout-test.jsonl"
    lines = [
        json.dumps({"payload": {"model": "gpt-5.6-sol"}, "type": "turn_context"}),
        json.dumps({
            "payload": {
                "info": {
                    "total_token_usage": {
                        "input_tokens": 100000,
                        "cached_input_tokens": 80000,
                        "output_tokens": 2000,
                        "reasoning_output_tokens": 500,
                        "total_tokens": 102000,
                    },
                    "model_context_window": 258400,
                    "last_token_usage": {"input_tokens": 30000},
                }
            }
        }),
    ]
    rollout_file.write_text("\n".join(lines), encoding="utf-8")
    parsed = _parse_codex_rollout_file(rollout_file)
    assert parsed is not None
    assert parsed["model"] == "gpt-5.6-sol"
    assert parsed["context_window"] == 258400
    assert parsed["last_input_tokens"] == 30000
    assert parsed["input_tokens"] == 100000
    assert parsed["cached_tokens"] == 80000
    assert parsed["cache_hit_rate"] == 80.0
    assert parsed["total_tokens"] == 102000


def test_parse_grok_session(tmp_path: Path):
    sess_dir = tmp_path / "grok_sess"
    sess_dir.mkdir()
    summary = {
        "current_model_id": "grok-4.6",
        "num_messages": 10,
    }
    updates_lines = [
        json.dumps({
            "method": "session/update",
            "params": {
                "update": {
                    "context_window_tokens": 42000,
                    "tokens_used": {
                        "input_tokens": 40000,
                        "cached_tokens": 30000,
                        "output_tokens": 1500,
                        "reasoning_tokens": 200,
                    }
                }
            }
        })
    ]
    (sess_dir / "updates.jsonl").write_text("\n".join(updates_lines), encoding="utf-8")
    parsed = _parse_grok_session(sess_dir, summary)
    assert parsed["model"] == "grok-4.6"
    assert parsed["last_input_tokens"] == 42000
    assert parsed["input_tokens"] == 40000
    assert parsed["cached_tokens"] == 30000
    assert parsed["output_tokens"] == 1500
    assert parsed["total_tokens"] == 71500


def test_resolve_session_usage_hermes_local(tmp_path: Path):
    db_file = tmp_path / "state.db"
    conn = sqlite3.connect(db_file)
    conn.execute(
        "CREATE TABLE sessions ("
        "id TEXT PRIMARY KEY, model TEXT, model_config TEXT, "
        "input_tokens INTEGER, output_tokens INTEGER, cache_read_tokens INTEGER, reasoning_tokens INTEGER)"
    )
    conn.execute(
        "INSERT INTO sessions VALUES (?, ?, ?, ?, ?, ?, ?)",
        (
            "sess_test_123",
            "google-antigravity/gemini-3.8-flash",
            json.dumps({"_usage_anchor": {"prompt_tokens": 64000}}),
            10000,
            2000,
            90000,
            5000,
        )
    )
    conn.commit()
    conn.close()

    store = MagicMock()
    store.find_session_by_id.return_value = {
        "id": "sess_test_123",
        "agent_id": "local-hermes-default",
        "connection_id": None,
    }
    store.get_agent.return_value = {
        "id": "local-hermes-default",
        "kind": "hermes",
        "profile_name": "default",
    }

    with patch("astrorder.core.session_usage._find_local_hermes_state_dbs", return_value=[db_file]):
        res = resolve_session_usage("sess_test_123", store)
        assert res is not None
        assert res["model"] == "google-antigravity/gemini-3.8-flash"
        assert res["last_input_tokens"] == 64000
        assert res["context_window"] == 350_000
        assert res["cached_tokens"] == 90000
        assert res["cache_hit_rate"] == 90.0


def test_api_session_usage_route(tmp_path: Path):
    from fastapi.testclient import TestClient
    from astrorder.main import create_app

    db_file = tmp_path / "state.db"
    conn = sqlite3.connect(db_file)
    conn.execute(
        "CREATE TABLE sessions ("
        "id TEXT PRIMARY KEY, model TEXT, model_config TEXT, "
        "input_tokens INTEGER, output_tokens INTEGER, cache_read_tokens INTEGER, reasoning_tokens INTEGER)"
    )
    conn.execute(
        "INSERT INTO sessions VALUES (?, ?, ?, ?, ?, ?, ?)",
        (
            "sess_route_test",
            "anthropic/claude-sonnet-4-20250514",
            json.dumps({"_usage_anchor": {"prompt_tokens": 50000}}),
            20000,
            1000,
            30000,
            500,
        )
    )
    conn.commit()
    conn.close()

    settings = Settings(
        database_url=f"sqlite:///{tmp_path}/astrorder.sqlite3",
        browser_secret="test-token",
        auto_connect_local_hermes=False,
        session_daemon_enabled=False,
    )
    app = create_app(settings)
    with patch("astrorder.core.session_usage._find_local_hermes_state_dbs", return_value=[db_file]):
        with TestClient(app) as client:
            resp = client.get("/api/v1/sessions/sess_route_test/usage", headers={"Authorization": "Bearer test-token"})
            assert resp.status_code == 200
            data = resp.json()
            assert data["ok"] is True
            assert data["session_id"] == "sess_route_test"
            assert data["model"] == "anthropic/claude-sonnet-4-20250514"
            assert data["context_window"] is None
            assert data["used_percentage"] is None
            assert data["last_input_tokens"] == 50000
            assert data["cached_tokens"] == 30000
            assert data["cache_hit_rate"] == 60.0

