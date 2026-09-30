from datetime import date
from pathlib import Path
from astrorder.core.rtk_service import aggregate_rtk_gain

def test_aggregate_rtk_gain_filters_and_aligns():
    raw_gain = {
        "summary": {
            "total_commands": 112,
            "total_input": 48674835,
            "total_output": 47926,
            "total_saved": 48626940, # 原生不一致字段
            "avg_savings_pct": 99.9,
        },
        "daily": [
            {
                "date": "2026-09-01",
                "commands": 2,
                "input_tokens": 1000,
                "output_tokens": 200,
                "saved_tokens": 800,
            },
            {
                "date": "2026-09-29",
                "commands": 1,
                "input_tokens": 81,
                "output_tokens": 33,
                "saved_tokens": 48,
            },
            {
                "date": "2026-09-30",
                "commands": 111,
                "input_tokens": 48674754,
                "output_tokens": 47893,
                "saved_tokens": 48626861,
            }
        ]
    }

    # 当天为 2026-09-30，测试 days=30 范围过滤
    today = date(2026, 9, 30)
    res_30 = aggregate_rtk_gain(raw_gain, days="30", reference_date=today)

    assert len(res_30["daily"]) == 3
    # 汇总计算 input - output，确保严格与 daily 聚合一致
    expected_saved = 800 + 48 + 48626861
    assert res_30["summary"]["total_saved"] == expected_saved
    assert res_30["summary"]["total_commands"] == 114
    assert res_30["summary"]["total_input"] == 1000 + 81 + 48674754
    assert res_30["summary"]["total_output"] == 200 + 33 + 47893

    # 过滤测试：days=1
    res_1 = aggregate_rtk_gain(raw_gain, days="1", reference_date=today)
    assert len(res_1["daily"]) == 1
    assert res_1["daily"][0]["date"] == "2026-09-30"
    assert res_1["summary"]["total_saved"] == 48626861


def test_get_agent_command_cli_mapping():
    from astrorder.core.rtk_service import get_agent_command
    # Codex CLI: --codex (NOT --agent codex)
    assert get_agent_command("codex", enable=True) == ["init", "-g", "--codex"]
    assert get_agent_command("codex", enable=False) == ["init", "-g", "--codex", "--uninstall"]

    # Hermes: --agent hermes
    assert get_agent_command("hermes", enable=True) == ["init", "-g", "--agent", "hermes"]
    assert get_agent_command("hermes", enable=False) == ["init", "-g", "--agent", "hermes", "--uninstall"]

    # Gemini: --gemini
    assert get_agent_command("gemini", enable=True) == ["init", "-g", "--gemini", "--auto-patch", "--no-trust-filters"]
    assert get_agent_command("gemini", enable=False) == ["init", "-g", "--gemini", "--uninstall"]

    # OpenCode: --opencode
    assert get_agent_command("opencode", enable=True) == ["init", "-g", "--opencode"]
    assert get_agent_command("opencode", enable=False) == ["init", "-g", "--uninstall"]

    # Copilot: --copilot
    assert get_agent_command("copilot", enable=True) == ["init", "-g", "--copilot"]
    assert get_agent_command("copilot", enable=False) == ["init", "-g", "--copilot", "--uninstall"]


def test_check_agent_status_local_with_isolated_env(tmp_path):
    from astrorder.core.rtk_service import check_agent_status_local
    # 隔离测试 Hermes 与 Codex 配置检测
    fake_hermes_home = tmp_path / "hermes_test"
    fake_hermes_home.mkdir()

    # 1. 尚未配置时
    status_hermes = check_agent_status_local("hermes", hermes_home=fake_hermes_home)
    assert status_hermes["enabled"] is False
    assert status_hermes["supported"] is True

    # 写入假插件（__init__.py 与 plugin.yaml 均需要）与 config.yaml
    plugin_dir = fake_hermes_home / "plugins" / "rtk-rewrite"
    plugin_dir.mkdir(parents=True)
    (plugin_dir / "__init__.py").write_text("# rtk hook", encoding="utf-8")
    (plugin_dir / "plugin.yaml").write_text("name: rtk-rewrite\n", encoding="utf-8")
    (fake_hermes_home / "config.yaml").write_text("plugins:\n  enabled:\n    - rtk-rewrite\n", encoding="utf-8")

    status_hermes_enabled = check_agent_status_local("hermes", hermes_home=fake_hermes_home)
    assert status_hermes_enabled["enabled"] is True

    # 2. Codex 检测
    fake_codex_dir = tmp_path / ".codex"
    fake_codex_dir.mkdir()
    status_codex = check_agent_status_local("codex", codex_dir=fake_codex_dir)
    assert status_codex["enabled"] is False

    (fake_codex_dir / "RTK.md").write_text("# RTK Instructions", encoding="utf-8")
    (fake_codex_dir / "hooks.json").write_text('{"PreToolUse": "rtk hook codex"}', encoding="utf-8")
    (fake_codex_dir / "AGENTS.md").write_text("@RTK.md\n", encoding="utf-8")

    status_codex_enabled = check_agent_status_local("codex", codex_dir=fake_codex_dir)
    assert status_codex_enabled["enabled"] is True


def test_rtk_api_endpoints_local_and_ssh(tmp_path: Path, monkeypatch) -> None:
    from fastapi.testclient import TestClient
    from astrorder.config import Settings
    from astrorder.main import create_app
    from astrorder import core
    from astrorder.core import rtk_service

    app = create_app(Settings(
        database_url=f"sqlite:///{(tmp_path / 'state.sqlite3').as_posix()}",
        browser_secret="test-secret",
        connector_secret="connector-secret",
        attachments_dir=tmp_path / "attachments",
        auto_connect_local_hermes=False,
    ))

    headers = {"Authorization": "Bearer test-secret"}

    # Mock 本机 RTK
    mock_status_local = {
        "installed": True,
        "version": "0.50.0",
        "executable": "C:\\fake\\rtk.exe",
        "agents": [
            {"kind": "codex", "name": "OpenAI Codex", "enabled": True, "supported": True},
            {"kind": "hermes", "name": "Hermes Agent", "enabled": False, "supported": True},
            {"kind": "grok", "name": "xAI Grok", "enabled": False, "supported": False, "detail": "RTK 暂不支持 Grok"},
        ],
        "daily": [{"date": "2026-09-30", "commands": 10, "input_tokens": 100, "output_tokens": 20, "saved_tokens": 80}],
        "summary": {"total_commands": 10, "total_input": 100, "total_output": 20, "total_saved": 80, "avg_savings_pct": 80.0},
        "detail": "本机 RTK 环境正常",
    }
    from astrorder.routers import rtk as rtk_router
    monkeypatch.setattr(rtk_router, "get_local_rtk_status", lambda days="30": mock_status_local)

    toggled = []
    def mock_toggle(kind, enabled):
        if kind not in {"hermes", "codex"}:
            from astrorder.connections import ConnectionError
            raise ConnectionError(f"Agent [{kind}] 暂未支持安全热切换，请手工配置或等待支持。", 422)
        toggled.append((kind, enabled))
    monkeypatch.setattr(rtk_router, "execute_local_agent_toggle", mock_toggle)

    with TestClient(app) as client:
        # 1. GET /api/v1/rtk/local
        resp = client.get("/api/v1/rtk/local?days=30", headers=headers)
        assert resp.status_code == 200
        data = resp.json()
        assert data["installed"] is True
        assert data["version"] == "0.50.0"
        assert len(data["agents"]) == 3
        agent_kinds = [a["kind"] for a in data["agents"]]
        assert agent_kinds == ["codex", "hermes", "grok"]
        grok_agent = next(a for a in data["agents"] if a["kind"] == "grok")
        assert grok_agent["supported"] is False
        assert "RTK 暂不支持" in grok_agent["detail"]
        assert data["summary"]["total_saved"] == 80

        # 2. PUT /api/v1/rtk/local/agents/hermes
        put_resp = client.put("/api/v1/rtk/local/agents/hermes", json={"enabled": True}, headers=headers)
        assert put_resp.status_code == 200
        assert toggled == [("hermes", True)]

        # 3. 非法参数校验
        bad_days = client.get("/api/v1/rtk/local?days=invalid", headers=headers)
        assert bad_days.status_code == 422

        # 4. POST /api/v1/rtk/local/install
        installed_called = []
        monkeypatch.setattr(rtk_router, "execute_local_rtk_install", lambda: installed_called.append(True))
        inst_resp = client.post("/api/v1/rtk/local/install", headers=headers)
        assert inst_resp.status_code == 200
        assert installed_called == [True]

        # 5. SSH 连接路由验证：保留原始 opaque connection_id（如 ssh-12345），不发生非法剥离
        ssh_passed_id = []
        def mock_ssh_status(_store, _settings, _service, cid, days="30"):
            ssh_passed_id.append((cid, days))
            return {
                "installed": True,
                "version": "0.50.0",
                "executable": "/home/user/.local/bin/rtk",
                "agents": [{"kind": "codex", "name": "OpenAI Codex", "enabled": True, "supported": True}],
                "daily": [],
                "summary": {"total_commands": 0, "total_input": 0, "total_output": 0, "total_saved": 0, "avg_savings_pct": 0.0},
                "detail": "SSH 远端 RTK 环境正常",
            }
        monkeypatch.setattr(rtk_router, "get_ssh_rtk_status", mock_ssh_status)

        ssh_resp = client.get("/api/v1/rtk/ssh-target-node-99?days=90", headers=headers)
        assert ssh_resp.status_code == 200
        assert ssh_passed_id == [("ssh-target-node-99", "90")]
        assert ssh_resp.json()["installed"] is True

        # 6. 未支持 Agent 切换热保护：抛出 422 错误且不执行破坏性动作
        unsupported_toggle = client.put("/api/v1/rtk/local/agents/claude", json={"enabled": True}, headers=headers)
        assert unsupported_toggle.status_code == 422
        assert "暂未支持安全热切换" in unsupported_toggle.json()["detail"]
