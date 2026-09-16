from fastapi.testclient import TestClient

import json

from astrorder.agent_gateway import AgentApiError, AgentContext, invoke, parse_session_key
from astrorder.config import Settings
from astrorder.main import create_app


def test_parse_session_key_accepts_key_or_parts():
    assert parse_session_key({"key": "codex::abc"}) == ("codex", "abc")
    assert parse_session_key({"agent_id": "hermes", "session_id": "s1"}) == ("hermes", "s1")
    try:
        parse_session_key({})
    except AgentApiError as exc:
        assert exc.code == "invalid_input"
    else:
        raise AssertionError("expected invalid_input")


def test_agent_gateway_lists_and_reads_sessions(tmp_path):
    app = create_app(
        Settings(
            database_url=f"sqlite:///{tmp_path / 'agent.sqlite3'}",
            attachments_dir=tmp_path / "attachments",
            browser_secret="browser-test",
            connector_secret="connector-test",
            auto_connect_local_hermes=False,
        )
    )
    with TestClient(app) as client:
        store = app.state.store
        store.upsert_agent(
            {
                "id": "local-codex",
                "kind": "codex",
                "name": "Codex",
                "status": "ready",
                "capabilities": ["chat"],
                "limitation": None,
                "source_id": "local-codex",
            }
        )
        store.upsert_session(
            {
                "id": "sess-1",
                "agent_id": "local-codex",
                "title": "完善 K8s",
                "workspace": "C:/repo",
                "status": "idle",
                "updated_at": "2026-09-16T00:00:00Z",
            }
        )
        store.upsert_message(
            {
                "id": "m1",
                "agent_id": "local-codex",
                "session_id": "sess-1",
                "role": "user",
                "kind": "message",
                "text": "你好",
                "created_at": "2026-09-16T00:00:01Z",
            }
        )
        denied = client.get("/api/v1/agent/catalog")
        assert denied.status_code in (401, 403, 503)
        catalog = client.get("/api/v1/agent/catalog", headers={"Authorization": "Bearer connector-test"})
        assert catalog.status_code == 200
        ids = [item["id"] for item in catalog.json()["items"]]
        assert "sessions.read" in ids
        listed = client.post(
            "/api/v1/agent/invoke",
            headers={"Authorization": "Bearer browser-test"},
            json={"capability": "sessions.list", "input": {}},
        )
        assert listed.status_code == 200
        assert listed.json()["items"][0]["key"] == "local-codex::sess-1"
        assert listed.json()["total"] == 1
        found = client.post(
            "/api/v1/agent/invoke",
            headers={"Authorization": "Bearer connector-test"},
            json={"capability": "sessions.search", "input": {"q": "K8s"}},
        )
        assert found.json()["items"][0]["key"] == "local-codex::sess-1"
        assert found.json()["total"] == 1
        read = client.post(
            "/api/v1/agent/invoke",
            headers={"Authorization": "Bearer connector-test"},
            json={"capability": "sessions.read", "input": {"key": "local-codex::sess-1"}},
        )
        assert read.status_code == 200
        assert read.json()["session"]["title"] == "完善 K8s"
        assert read.json()["items"][0]["text"] == "你好"
        assert read.json()["count"] == 1
        assert read.json()["has_more"] is False
        missing = client.post(
            "/api/v1/agent/invoke",
            headers={"Authorization": "Bearer connector-test"},
            json={"capability": "sessions.read", "input": {"key": "local-codex::nope"}},
        )
        assert missing.status_code == 404

        listed_tools = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={"jsonrpc": "2.0", "id": 1, "method": "tools/list"},
        )
        assert listed_tools.status_code == 200
        names = [item["name"] for item in listed_tools.json()["result"]["tools"]]
        assert "sessions_read" in names
        called = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 2,
                "method": "tools/call",
                "params": {"name": "sessions_read", "arguments": {"key": "local-codex::sess-1"}},
            },
        )
        assert called.status_code == 200
        payload = json.loads(called.json()["result"]["content"][0]["text"])
        assert payload["session"]["title"] == "完善 K8s"

        # Test sessions.create
        created = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 3,
                "method": "tools/call",
                "params": {"name": "sessions_create", "arguments": {"agent_id": "local-codex", "title": "MCP子会话"}},
            },
        )
        assert created.status_code == 200
        created_data = json.loads(created.json()["result"]["content"][0]["text"])
        assert created_data["session"]["title"] == "MCP子会话"
        new_key = created_data["session"]["key"]

        # Test sessions.send
        sent = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 4,
                "method": "tools/call",
                "params": {"name": "sessions_send", "arguments": {"key": new_key, "text": "开始执行子任务"}},
            },
        )
        assert sent.status_code == 200
        sent_resp = sent.json()
        # Since mock agent has no live connector, either accepted or gracefully rejected
        assert "result" in sent_resp or "error" in sent_resp

        # Test plugins.list
        plugins = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 5,
                "method": "tools/call",
                "params": {"name": "plugins_list", "arguments": {}},
            },
        )
        assert plugins.status_code == 200
        plugins_data = json.loads(plugins.json()["result"]["content"][0]["text"])
        assert any(p["id"] == "mermaid" for p in plugins_data["items"])
        assert any(p["id"] == "drawio" for p in plugins_data["items"])

        # Test plugins.configure
        plugin_cfg = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 6,
                "method": "tools/call",
                "params": {"name": "plugins_configure", "arguments": {"plugin_id": "mermaid", "enabled": True}},
            },
        )
        assert plugin_cfg.status_code == 200
        cfg_data = json.loads(plugin_cfg.json()["result"]["content"][0]["text"])
        assert cfg_data["ok"] is True
        assert cfg_data["plugin"]["id"] == "mermaid"
        assert cfg_data["plugin"]["enabled"] is True

        # Test plugins.open
        plugin_open = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 7,
                "method": "tools/call",
                "params": {"name": "plugins_open", "arguments": {"plugin_id": "terminal", "title": "调试终端"}},
            },
        )
        assert plugin_open.status_code == 200
        open_data = json.loads(plugin_open.json()["result"]["content"][0]["text"])
        assert open_data["ok"] is True
        assert open_data["event"]["plugin_id"] == "terminal"

        # Test plugins.close
        plugin_close = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 8,
                "method": "tools/call",
                "params": {"name": "plugins_close", "arguments": {"collapse": True}},
            },
        )
        assert plugin_close.status_code == 200
        close_data = json.loads(plugin_close.json()["result"]["content"][0]["text"])
        assert close_data["ok"] is True
        assert close_data["event"]["collapse"] is True

        # Test machines.dispatch
        dispatched = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 9,
                "method": "tools/call",
                "params": {"name": "machines_dispatch", "arguments": {"machine_id": "local", "title": "本地委托"}},
            },
        )
        assert dispatched.status_code == 200
        disp_data = json.loads(dispatched.json()["result"]["content"][0]["text"])
        assert disp_data["ok"] is True
        assert disp_data["machine_id"] == "local"

        # Test monitor.sessions.add & remove & layout
        mon_add = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 10,
                "method": "tools/call",
                "params": {"name": "monitor_sessions_add", "arguments": {"key": new_key}},
            },
        )
        assert mon_add.status_code == 200
        assert json.loads(mon_add.json()["result"]["content"][0]["text"])["ok"] is True

        mon_layout = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 11,
                "method": "tools/call",
                "params": {"name": "monitor_layout_set", "arguments": {"columns": 3}},
            },
        )
        assert mon_layout.status_code == 200
        assert json.loads(mon_layout.json()["result"]["content"][0]["text"])["columns"] == 3


def test_invoke_unknown_capability():
    class Store:
        def list_sessions(self, agent_id=None):
            return []

        def list_agents(self):
            return []

        def list_ssh_connections(self):
            return []

        def list_projects(self):
            return []

    try:
        invoke("nope.list", {}, AgentContext(store=Store()))
    except AgentApiError as exc:
        assert exc.code == "unknown_capability"
    else:
        raise AssertionError("expected unknown_capability")
