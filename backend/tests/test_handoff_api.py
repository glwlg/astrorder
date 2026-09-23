import asyncio
from types import SimpleNamespace

from fastapi.testclient import TestClient

from astrorder.config import Settings
from astrorder.main import create_app


def test_local_codex_session_handoffs_to_hermes_through_temporary_summary_fork(tmp_path):
    app = create_app(
        Settings(
            database_url=f"sqlite:///{tmp_path / 'handoff.sqlite3'}",
            attachments_dir=tmp_path / "attachments",
            browser_secret="browser-test",
            auto_connect_local_hermes=False,
        )
    )
    submitted = []
    configured = []
    lifecycle = []
    summary_ready = False
    history_reads = 0

    class SourceCodex:
        def create(self, workspace, title, *, ephemeral=False, parent_session_id=None):
            lifecycle.append(("fork", parent_session_id, ephemeral))
            assert (workspace, title) == ("C:/repo", "转交摘要")
            return {
                "id": "summary-fork",
                "agent_id": "local-codex",
                "title": title,
                "workspace": workspace,
                "status": "idle",
                "updated_at": "2026-09-15T00:00:00Z",
                "history_state": "available",
                "control_state": "owned",
            }

        def messages(self, session_id, before, limit):
            nonlocal history_reads
            history_reads += 1
            assert (session_id, before, limit) == ("summary-fork", None, 100)
            items = [
                {
                    "id": "old-answer",
                    "role": "assistant",
                    "kind": "message",
                    "text": "旧回复",
                    "attachments": [],
                }
            ]
            if summary_ready and history_reads >= 3:
                items.append(
                    {
                        "id": "summary-answer",
                        "role": "assistant",
                        "kind": "message",
                        "text": "完整交接摘要：早期约束与当前进度",
                        "attachments": [],
                    }
                )
            return {"items": items, "next_cursor": None}

        def mutate(self, session_id, updates):
            assert (session_id, updates) == ("summary-fork", None)
            lifecycle.append(("delete-fork", session_id))

    class TargetRuntime:
        daemon_owned = True

        def set_model(self, session_id, provider, model):
            configured.append(("model", session_id, provider, model))
            return {"provider": provider, "model": model}

        def set_effort(self, session_id, effort):
            configured.append(("effort", session_id, effort))
            return {"effort": effort}

    def create_target(
        agent_id,
        workspace=None,
        title=None,
        *,
        parent_session_id=None,
        provider=None,
        model=None,
        effort=None,
    ):
        try:
            asyncio.get_running_loop()
        except RuntimeError:
            pass
        else:
            raise AssertionError("native session creation must run outside the API event loop")
        assert parent_session_id is None
        assert (agent_id, workspace, title) == ("local-hermes", "C:/repo", "窗口管理")
        assert (provider, model, effort) == ("ocx", "gpt-5.6-sol", "high")
        lifecycle.append(("create-target", agent_id))
        return {
            "id": "target-session",
            "agent_id": agent_id,
            "title": title,
            "workspace": workspace,
            "status": "idle",
            "updated_at": "2026-09-15T00:00:00Z",
            "history_state": "live",
            "control_state": "owned",
        }

    async def restore(agent_id, session_id, provider, model, effort):
        configured.append(("restore", agent_id, session_id, provider, model, effort))

    async def submit(command):
        nonlocal summary_ready
        submitted.append(command)
        if command["session_id"] == "summary-fork":
            assert "完整会话上下文" in command["text"]
            summary_ready = True
            app.state.store.set_command_state(
                command["agent_id"], command["session_id"], command["id"], "completed", None
            )
        return "accepted", None

    with TestClient(app) as client:
        for agent in (
            {"id": "local-codex", "kind": "codex", "name": "Codex", "status": "ready"},
            {"id": "local-hermes", "kind": "hermes", "name": "Hermes", "status": "ready"},
        ):
            app.state.store.upsert_agent({**agent, "capabilities": ["chat"]})
        app.state.store.upsert_session(
            {
                "id": "source-session",
                "agent_id": "local-codex",
                "title": "窗口管理",
                "workspace": "C:/repo",
                "status": "idle",
                "updated_at": "2026-09-15T00:00:00Z",
            }
        )
        source = SourceCodex()
        app.state.environments = SimpleNamespace(
            for_agent=lambda agent_id: source if agent_id == "local-codex" else None,
            shutdown=lambda: None,
        )
        app.state.connections.get_runtime_by_agent_id = (
            lambda agent_id: TargetRuntime() if agent_id == "local-hermes" else None
        )
        app.state.connections.create_session_for_agent = create_target
        app.state.service.register_model_binding_restorer("hermes", restore)
        app.state.service.register_native_command_handler("local-codex", submit)
        app.state.service.register_native_command_handler("local-hermes", submit)

        response = client.post(
            "/api/v1/sessions/source-session/handoff",
            json={
                "operation_id": "handoff-test",
                "source_agent_id": "local-codex",
                "target_agent_id": "local-hermes",
                "provider": "ocx",
                "model": "gpt-5.6-sol",
                "effort": "high",
            },
            headers={"Authorization": "Bearer browser-test"},
        )

        assert len(submitted) == 1
        user_response = client.post(
            "/api/v1/commands",
            json={
                "id": "user-continues",
                "agent_id": "local-hermes",
                "session_id": "target-session",
                "action": "send",
                "text": "请检查当前状态",
                "attachment_ids": [],
                "target_id": None,
            },
            headers={"Authorization": "Bearer browser-test"},
        )
        assert user_response.status_code == 200, user_response.text

    assert response.status_code == 200, response.text
    target = response.json()
    assert lifecycle == [
        ("fork", "source-session", True),
        ("delete-fork", "summary-fork"),
        ("create-target", "local-hermes"),
    ]
    assert app.state.store.get_session("local-codex", "summary-fork") is None
    assert target["handoff_from_agent_id"] == "local-codex"
    assert target["handoff_from_session_id"] == "source-session"
    assert configured == [
        ("restore", "local-hermes", "target-session", "ocx", "gpt-5.6-sol", "high")
    ]
    assert len(submitted) == 2
    assert submitted[0]["session_id"] == "summary-fork"
    assert submitted[1]["session_id"] == "target-session"
    assert "完整交接摘要：早期约束与当前进度" in submitted[1]["text"]
    assert "<用户的新消息>\n请检查当前状态" in submitted[1]["text"]
    assert app.state.store.pending_session_handoff_context("local-hermes", "target-session") is None
    assert app.state.store.get_message("local-hermes", "target-session", "handoff-context")["role"] == "system"


def test_failed_handoff_deletes_the_created_target_session(tmp_path, monkeypatch):
    from astrorder import api as api_module
    from astrorder.connections import ConnectionError

    app = create_app(
        Settings(
            database_url=f"sqlite:///{tmp_path / 'handoff-cleanup.sqlite3'}",
            attachments_dir=tmp_path / "attachments",
            browser_secret="browser-test",
            auto_connect_local_hermes=False,
        )
    )
    deleted = []

    async def summarize(*_args):
        return "完整交接摘要"

    def create_target(_payload, _request):
        return {
            "id": "failed-target",
            "agent_id": "local-hermes",
            "title": "窗口管理",
            "workspace": "C:/repo",
            "status": "idle",
            "updated_at": "2026-09-15T00:00:00Z",
            "history_state": "live",
            "control_state": "owned",
        }

    def reject_context(*_args):
        raise ConnectionError("目标上下文保存失败。", 502)

    monkeypatch.setattr(api_module, "_summarize_handoff", summarize)
    monkeypatch.setattr(api_module, "create_session", create_target)

    with TestClient(app) as client:
        for agent in (
            {"id": "local-codex", "kind": "codex", "name": "Codex", "status": "ready"},
            {"id": "local-hermes", "kind": "hermes", "name": "Hermes", "status": "ready"},
        ):
            app.state.store.upsert_agent({**agent, "capabilities": ["chat"]})
        app.state.store.upsert_session(
            {
                "id": "source-session",
                "agent_id": "local-codex",
                "title": "窗口管理",
                "workspace": "C:/repo",
                "status": "idle",
                "updated_at": "2026-09-15T00:00:00Z",
            }
        )
        app.state.environments = SimpleNamespace(for_agent=lambda _agent_id: None, shutdown=lambda: None)
        app.state.connections.mutate_session_for_agent = (
            lambda agent_id, session_id, updates: deleted.append((agent_id, session_id, updates))
        )
        app.state.store.set_session_handoff_context = reject_context

        response = client.post(
            "/api/v1/sessions/source-session/handoff",
            json={
                "operation_id": "handoff-cleanup-test",
                "source_agent_id": "local-codex",
                "target_agent_id": "local-hermes",
                "provider": "ocx",
                "model": "google-antigravity/gemini-3.8-flash",
                "effort": "high",
            },
            headers={"Authorization": "Bearer browser-test"},
        )

    assert response.status_code == 502
    assert deleted == [("local-hermes", "failed-target", None)]
    assert app.state.store.get_session("local-hermes", "failed-target") is None


def test_handoff_can_be_cancelled(tmp_path):
    app = create_app(
        Settings(
            database_url=f"sqlite:///{tmp_path / 'handoff-cancel.sqlite3'}",
            attachments_dir=tmp_path / "attachments",
            browser_secret="browser-test",
            auto_connect_local_hermes=False,
        )
    )

    with TestClient(app) as client:
        event = app.state.background_tasks.start("handoff-cancel-test")

        response = client.post(
            "/api/v1/handoffs/handoff-cancel-test/cancel",
            headers={"Authorization": "Bearer browser-test"},
        )

        assert response.status_code == 200
        assert response.json() == {"cancelled": True}
        assert event.is_set()


def test_remote_same_machine_handoff_allowed_and_cross_machine_rejected(tmp_path, monkeypatch):
    app = create_app(
        Settings(
            database_url=f"sqlite:///{tmp_path / 'handoff-remote.sqlite3'}",
            attachments_dir=tmp_path / "attachments",
            browser_secret="browser-test",
            auto_connect_local_hermes=False,
        )
    )

    with TestClient(app) as client:
        store = app.state.store
        store.upsert_agent({"id": "remote-codex", "name": "Remote Codex", "kind": "codex", "status": "ready", "connection_id": "conn-1"})
        store.upsert_agent({"id": "remote-hermes", "name": "Remote Hermes", "kind": "hermes", "status": "ready", "connection_id": "conn-1"})
        store.upsert_agent({"id": "other-hermes", "name": "Other Hermes", "kind": "hermes", "status": "ready", "connection_id": "conn-2"})
        store.upsert_session({"id": "remote-sess-1", "agent_id": "remote-codex", "title": "Remote Session", "status": "idle", "updated_at": "2026-09-23T10:00:00Z"})
        # 1. 跨机器转交应拒绝 (422)
        resp_cross = client.post(
            "/api/v1/sessions/remote-sess-1/handoff",
            json={
                "operation_id": "op-cross",
                "source_agent_id": "remote-codex",
                "target_agent_id": "other-hermes",
            },
            headers={"Authorization": "Bearer browser-test"},
        )
        assert resp_cross.status_code == 422
        assert "同一台机器" in resp_cross.json()["detail"]

        # 2. 同机器远程转交应允许通过机器校验
        summarized = []
        async def fake_summarize(*args, **kwargs):
            summarized.append(True)
            return "Remote summary"

        from astrorder import api as api_module
        monkeypatch.setattr(api_module, "_summarize_handoff", fake_summarize)
        monkeypatch.setattr(api_module, "create_session", lambda payload, req: {
            "id": "target-remote-sess",
            "agent_id": payload.agent_id,
            "title": payload.title,
            "status": "idle",
            "updated_at": "2026-09-23T10:01:00Z",
        })

        resp_same = client.post(
            "/api/v1/sessions/remote-sess-1/handoff",
            json={
                "operation_id": "op-same",
                "source_agent_id": "remote-codex",
                "target_agent_id": "remote-hermes",
                "provider": "anthropic",
                "model": "claude-3-5-sonnet",
            },
            headers={"Authorization": "Bearer browser-test"},
        )
        assert resp_same.status_code == 200
        assert summarized == [True]
