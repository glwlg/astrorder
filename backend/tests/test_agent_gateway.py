from fastapi.testclient import TestClient

import json
import pytest

from astrorder.agents.gateway import AgentApiError, AgentContext, _resolve_browser_session_key, invoke, parse_session_key
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


def test_browser_session_binding_rejects_ambiguous_calls():
    class Store:
        def get_session(self, agent_id, session_id):
            return {"agent_id": agent_id, "id": session_id} if session_id == "one" else None

        def list_sessions(self, _agent_id):
            return [{"id": "one", "status": "running"}, {"id": "two", "status": "running"}]

    ctx = AgentContext(store=Store())
    assert _resolve_browser_session_key({"session_key": "agent::one"}, ctx) == "agent::one"
    with pytest.raises(AgentApiError, match="session_key"):
        _resolve_browser_session_key({"caller_agent_id": "agent"}, ctx)


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

        # Test blackboard.set & get & delete
        bb_set = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 12,
                "method": "tools/call",
                "params": {"name": "blackboard_set", "arguments": {"namespace": "session:local-codex::sess-1", "key": "auth_spec", "value": {"endpoint": "/refresh", "ttl": 3600}}},
            },
        )
        assert bb_set.status_code == 200
        assert json.loads(bb_set.json()["result"]["content"][0]["text"])["ok"] is True

        bb_get = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 13,
                "method": "tools/call",
                "params": {"name": "blackboard_get", "arguments": {"namespace": "session:local-codex::sess-1", "key": "auth_spec"}},
            },
        )
        assert bb_get.status_code == 200
        get_data = json.loads(bb_get.json()["result"]["content"][0]["text"])
        assert get_data["exists"] is True
        assert get_data["value"]["ttl"] == 3600

        bb_list = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 14,
                "method": "tools/call",
                "params": {"name": "blackboard_get", "arguments": {"namespace": "session:local-codex::sess-1"}},
            },
        )
        assert bb_list.status_code == 200
        list_data = json.loads(bb_list.json()["result"]["content"][0]["text"])
        assert "auth_spec" in list_data["items"]

        bb_del = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 15,
                "method": "tools/call",
                "params": {"name": "blackboard_delete", "arguments": {"namespace": "session:local-codex::sess-1", "key": "auth_spec"}},
            },
        )
        assert bb_del.status_code == 200
        assert json.loads(bb_del.json()["result"]["content"][0]["text"])["ok"] is True

        # Test swarm.milestone declare, list, resolve
        ms_dec = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 16,
                "method": "tools/call",
                "params": {"name": "swarm_milestone_declare", "arguments": {"milestone_id": "backend_ready", "title": "后端接口已就绪", "wake_session_key": new_key, "wake_prompt": "启动前端联调"}},
            },
        )
        assert ms_dec.status_code == 200
        assert json.loads(ms_dec.json()["result"]["content"][0]["text"])["ok"] is True

        ms_list = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 17,
                "method": "tools/call",
                "params": {"name": "swarm_milestone_list", "arguments": {}},
            },
        )
        assert ms_list.status_code == 200
        ms_items = json.loads(ms_list.json()["result"]["content"][0]["text"])["items"]
        assert any(item["id"] == "backend_ready" and item["status"] == "pending" for item in ms_items)

        ms_res = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 18,
                "method": "tools/call",
                "params": {"name": "swarm_milestone_resolve", "arguments": {"milestone_id": "backend_ready", "result": {"api_url": "/api/v1/login"}}},
            },
        )
        assert ms_res.status_code == 200
        res_data = json.loads(ms_res.json()["result"]["content"][0]["text"])
        assert res_data["ok"] is True
        assert res_data["milestone"]["status"] == "resolved"
        assert res_data["milestone"]["result"]["api_url"] == "/api/v1/login"

        # Test swarm.telemetry report & get
        tel_rep = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 19,
                "method": "tools/call",
                "params": {
                    "name": "swarm_telemetry_report",
                    "arguments": {
                        "key": new_key,
                        "progress": 75,
                        "phase": "running_tests",
                        "status": "ok",
                        "summary": "7/7 tests passed, building bundle",
                        "artifacts": ["dist/bundle.js"],
                    },
                },
            },
        )
        assert tel_rep.status_code == 200
        assert json.loads(tel_rep.json()["result"]["content"][0]["text"])["ok"] is True

        tel_get = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 20,
                "method": "tools/call",
                "params": {"name": "swarm_telemetry_get", "arguments": {"key": new_key}},
            },
        )
        assert tel_get.status_code == 200
        rep_data = json.loads(tel_get.json()["result"]["content"][0]["text"])
        assert rep_data["exists"] is True
        assert rep_data["report"]["progress"] == 75
        assert rep_data["report"]["phase"] == "running_tests"

        # Test swarm.sos escalate, list, resolve
        sos_esc = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 21,
                "method": "tools/call",
                "params": {
                    "name": "swarm_sos_escalate",
                    "arguments": {
                        "key": new_key,
                        "reason": "Port 3000 collision",
                        "context": "EADDRINUSE 0.0.0.0:3000",
                    },
                },
            },
        )
        assert sos_esc.status_code == 200
        sos_data = json.loads(sos_esc.json()["result"]["content"][0]["text"])
        assert sos_data["ok"] is True
        sos_id = sos_data["sos"]["id"]
        assert sos_data["sos"]["status"] == "active"

        sos_list = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 22,
                "method": "tools/call",
                "params": {"name": "swarm_sos_list", "arguments": {}},
            },
        )
        assert sos_list.status_code == 200
        assert any(s["id"] == sos_id for s in json.loads(sos_list.json()["result"]["content"][0]["text"])["items"])

        sos_res = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 23,
                "method": "tools/call",
                "params": {"name": "swarm_sos_resolve", "arguments": {"sos_id": sos_id, "resolution": "Assigned port 3005"}},
            },
        )
        assert sos_res.status_code == 200
        res_sos = json.loads(sos_res.json()["result"]["content"][0]["text"])
        assert res_sos["ok"] is True
        assert res_sos["sos"]["status"] == "resolved"

        # Test swarm.lock acquire, list, release
        lock_acq = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 24,
                "method": "tools/call",
                "params": {"name": "swarm_lock_acquire", "arguments": {"resource": "db:schema", "owner_key": new_key, "ttl_seconds": 60}},
            },
        )
        assert lock_acq.status_code == 200
        assert json.loads(lock_acq.json()["result"]["content"][0]["text"])["acquired"] is True

        # Conflicting acquire by another worker
        lock_conflict = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 25,
                "method": "tools/call",
                "params": {"name": "swarm_lock_acquire", "arguments": {"resource": "db:schema", "owner_key": "other_worker"}},
            },
        )
        assert lock_conflict.status_code == 200
        assert json.loads(lock_conflict.json()["result"]["content"][0]["text"])["acquired"] is False

        lock_list = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 26,
                "method": "tools/call",
                "params": {"name": "swarm_lock_list", "arguments": {}},
            },
        )
        assert lock_list.status_code == 200
        assert any(l["resource"] == "db:schema" for l in json.loads(lock_list.json()["result"]["content"][0]["text"])["items"])

        lock_rel = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 27,
                "method": "tools/call",
                "params": {"name": "swarm_lock_release", "arguments": {"resource": "db:schema", "owner_key": new_key}},
            },
        )
        assert lock_rel.status_code == 200
        assert json.loads(lock_rel.json()["result"]["content"][0]["text"])["released"] is True

        # Test sessions.create with ephemeral and parent_key (Satellite DAG)
        satellite = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 28,
                "method": "tools/call",
                "params": {
                    "name": "sessions_create",
                    "arguments": {
                        "agent_id": "local-codex",
                        "title": "Satellite Probe",
                        "parent_key": new_key,
                        "ephemeral": True,
                    },
                },
            },
        )
        assert satellite.status_code == 200
        sat_data = json.loads(satellite.json()["result"]["content"][0]["text"])
        assert sat_data["session"]["ephemeral"] is True
        assert sat_data["session"]["parent_session_key"] == new_key
        sat_key = sat_data["session"]["key"]

        # Test sessions.list with pagination & ephemeral filter
        paginated = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 29,
                "method": "tools/call",
                "params": {
                    "name": "sessions_list",
                    "arguments": {"agent_id": "local-codex", "limit": 1, "offset": 0},
                },
            },
        )
        assert paginated.status_code == 200
        page_data = json.loads(paginated.json()["result"]["content"][0]["text"])
        assert len(page_data["items"]) == 1
        assert page_data["limit"] == 1
        assert page_data["offset"] == 0
        assert page_data["has_more"] is True

        eph_filter = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 30,
                "method": "tools/call",
                "params": {
                    "name": "sessions_list",
                    "arguments": {"agent_id": "local-codex", "ephemeral": True},
                },
            },
        )
        assert eph_filter.status_code == 200
        eph_items = json.loads(eph_filter.json()["result"]["content"][0]["text"])["items"]
        assert any(item["key"] == sat_key for item in eph_items)

        # Test sessions.tree (DAG topology)
        tree_resp = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 31,
                "method": "tools/call",
                "params": {
                    "name": "sessions_tree",
                    "arguments": {"key": new_key},
                },
            },
        )
        assert tree_resp.status_code == 200
        tree_data = json.loads(tree_resp.json()["result"]["content"][0]["text"])
        assert tree_data["ok"] is True
        assert tree_data["tree"]["key"] == new_key
        assert tree_data["tree"]["satellite_count"] == 1
        assert tree_data["tree"]["children"][0]["key"] == sat_key

        # Test blackboard.components.list & schema for DataTable
        comp_list = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 32,
                "method": "tools/call",
                "params": {"name": "blackboard_components_list", "arguments": {}},
            },
        )
        assert comp_list.status_code == 200
        comps = json.loads(comp_list.json()["result"]["content"][0]["text"])["items"]
        assert any(c["id"] == "DataTable" for c in comps)

        dt_schema = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 33,
                "method": "tools/call",
                "params": {"name": "blackboard_component_schema", "arguments": {"component_id": "DataTable"}},
            },
        )
        assert dt_schema.status_code == 200
        dt_data = json.loads(dt_schema.json()["result"]["content"][0]["text"])
        assert dt_data["ok"] is True
        assert "columns" in dt_data["schema"]

        # Test sessions.delete
        del_sat = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 34,
                "method": "tools/call",
                "params": {"name": "sessions_delete", "arguments": {"key": sat_key}},
            },
        )
        assert del_sat.status_code == 200
        del_res = json.loads(del_sat.json()["result"]["content"][0]["text"])
        assert del_res["ok"] is True
        assert len(del_res["deleted"]) == 1

        # Batch delete root
        batch_del = client.post(
            "/api/v1/agent/mcp",
            headers={"Authorization": "Bearer connector-test"},
            json={
                "jsonrpc": "2.0",
                "id": 35,
                "method": "tools/call",
                "params": {"name": "sessions_delete", "arguments": {"keys": [new_key]}},
            },
        )
        assert batch_del.status_code == 200
        batch_res = json.loads(batch_del.json()["result"]["content"][0]["text"])
        assert batch_res["ok"] is True
        assert len(batch_res["deleted"]) == 1

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
