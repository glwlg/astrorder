from astrorder.core.handoff import SUMMARY_PROMPT, build_handoff_prompt, visible_handoff_user_text


def test_handoff_uses_source_agent_summary():
    session = {"id": "source", "title": "修复窗口", "workspace": "C:/repo"}
    summary = "当前目标：修复窗口管理器。\n已完成：修改代码并通过测试。"

    prompt = build_handoff_prompt(session, "Codex", summary)

    assert "源 Agent 生成的交接摘要" in prompt
    assert summary in prompt
    assert "C:/repo" in prompt
    assert "完整会话上下文" in SUMMARY_PROMPT
    assert "不要执行命令" in SUMMARY_PROMPT


def test_handoff_context_is_hidden_from_projected_user_message():
    text = "内部交接上下文\n\n<用户的新消息>\n继续检查"

    assert visible_handoff_user_text(text) == "继续检查"
