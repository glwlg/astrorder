from __future__ import annotations

from astrorder import agent_mcp


def test_agent_mcp_generates_concrete_input_schemas():
    tools = {tool["name"]: tool for tool in agent_mcp.tools()}

    assert "blackboard_set" in tools
    bb_set = tools["blackboard_set"]

    assert bb_set["inputSchema"]["type"] == "object"
    assert "properties" in bb_set["inputSchema"]
    assert "key" in bb_set["inputSchema"]["properties"]
    assert "value" in bb_set["inputSchema"]["properties"]
    assert "session_key" in bb_set["inputSchema"]["properties"]
    assert "required" in bb_set["inputSchema"]
    assert "key" in bb_set["inputSchema"]["required"]
    assert "value" in bb_set["inputSchema"]["required"]
    assert "session_key" in bb_set["inputSchema"]["required"]


def test_blackboard_ns_resolution_with_polymorphic_session_keys():
    from unittest.mock import MagicMock

    from astrorder.agent_gateway import AgentContext, _resolve_blackboard_ns

    ctx = AgentContext(store=MagicMock(), service=None, runtime_resolver=None)

    # 1. 群聊专属会话作用域
    assert _resolve_blackboard_ns({"session_key": "group::dev_ops_team"}, ctx) == "group:dev_ops_team"

    # 2. 星图伴星任务作用域
    assert _resolve_blackboard_ns({"session_key": "swarm::lead_star_01"}, ctx) == "swarm:lead_star_01"

    # 3. 单聊普通会话
    ctx.store.get_session_blackboard_namespace.return_value = None
    assert _resolve_blackboard_ns({"session_key": "local-codex::sess_123"}, ctx) == "session:local-codex::sess_123"


def test_blackboard_set_triggers_group_message_broadcast():
    from unittest.mock import MagicMock
    from astrorder.agent_gateway import _blackboard_set, AgentContext

    store = MagicMock()
    service = MagicMock()
    ctx = AgentContext(store=store, service=service, runtime_resolver=None)

    payload = {
        "session_key": "group::dev_team",
        "key": "api_spec",
        "value": {"endpoint": "/users"},
        "caller_agent_id": "local-codex",
    }

    res = _blackboard_set(payload, ctx)
    assert res["ok"] is True
    assert res["namespace"] == "group:dev_team"

    # 验证向服务总线广播了 blackboard.change
    service._server_event.assert_any_call(
        "blackboard.change",
        agent_id=None,
        session_id=None,
        data={"action": "set", "namespace": "group:dev_team", "key": "api_spec", "value": {"endpoint": "/users"}},
    )


def test_agent_mcp_blackboard_descriptions_include_chinese_trigger_keywords():
    tools = {tool["name"]: tool for tool in agent_mcp.tools()}

    bb_set_desc = tools["blackboard_set"]["description"]
    assert "黑板" in bb_set_desc
    assert "写入" in bb_set_desc or "发布" in bb_set_desc
    assert "严禁" in bb_set_desc or "不要" in bb_set_desc or "直接调用" in bb_set_desc

    bb_get_desc = tools["blackboard_get"]["description"]
    assert "黑板" in bb_get_desc
