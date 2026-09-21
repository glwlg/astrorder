from fastapi.testclient import TestClient
from astrorder.config import Settings
from astrorder.main import create_app

AUTH_HEADERS = {"Authorization": "Bearer browser-test"}


def test_bot_groups_crud_and_messages(tmp_path):
    app = create_app(
        Settings(
            database_url=f"sqlite:///{tmp_path / 'bot_groups.sqlite3'}",
            attachments_dir=tmp_path / "attachments",
            browser_secret="browser-test",
            auto_connect_local_hermes=False,
        )
    )

    with TestClient(app) as client:
        resp = client.get("/api/v1/bot-groups", headers=AUTH_HEADERS)
        assert resp.status_code == 200
        assert resp.json()["count"] == 0

        payload = {
            "name": "跨机协同专家群",
            "description": "本地 Codex 与远端 Hermes 协作群",
            "members": [
                {
                    "machine_id": "local",
                    "agent_id": "local-codex",
                    "name": "Codex",
                    "alias": "前端工程师",
                    "model_provider": "openai",
                    "model_name": "gpt-5",
                    "thinking_effort": "high",
                    "approval_policy": "manual",
                    "workspace": "P:/workspace/demo",
                },
                {
                    "machine_id": "ssh-debian",
                    "agent_id": "ssh-hermes-deb",
                    "name": "Hermes",
                    "alias": "运维专家",
                },
            ],
            "max_hops": 4,
        }
        create_resp = client.post("/api/v1/bot-groups", json=payload, headers=AUTH_HEADERS)
        assert create_resp.status_code == 200
        group = create_resp.json()["group"]
        gid = group["id"]
        assert group["name"] == "跨机协同专家群"
        assert len(group["members"]) == 2
        assert group["members"][0]["model_name"] == "gpt-5"
        assert group["members"][0]["workspace"] == "P:/workspace/demo"
        assert group["member_states"]["local-codex"]["status"] == "idle"

        get_resp = client.get(f"/api/v1/bot-groups/{gid}", headers=AUTH_HEADERS)
        assert get_resp.status_code == 200

        msg_payload = {
            "text": "请 @Hermes 检查远程系统负载，然后通知 @Codex",
        }
        msg_resp = client.post(
            f"/api/v1/bot-groups/{gid}/messages", json=msg_payload, headers=AUTH_HEADERS
        )
        assert msg_resp.status_code == 200
        data = msg_resp.json()
        assert data["ok"] is True
        assert data["target_agent_id"] == "ssh-hermes-deb"

        list_msgs = client.get(f"/api/v1/bot-groups/{gid}/messages", headers=AUTH_HEADERS)
        assert list_msgs.status_code == 200
        assert len(list_msgs.json()["items"]) == 1

        stop_resp = client.post(f"/api/v1/bot-groups/{gid}/stop", headers=AUTH_HEADERS)
        assert stop_resp.status_code == 200
        assert stop_resp.json()["group"]["active_hop"] == 0

        del_resp = client.delete(f"/api/v1/bot-groups/{gid}", headers=AUTH_HEADERS)
        assert del_resp.status_code == 200
