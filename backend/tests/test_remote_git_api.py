from fastapi.testclient import TestClient

from astrorder.config import Settings
from astrorder.main import create_app


def test_run_git_remote_ssh_resolves_from_store_and_executes_via_ssh(tmp_path, monkeypatch):
    app = create_app(
        Settings(
            database_url=f"sqlite:///{tmp_path}/test.sqlite3",
            attachments_dir=tmp_path / "attachments",
            browser_secret="test-secret",
            connector_secret="test-connector",
            auto_connect_local_hermes=False,
            session_daemon_enabled=False,
        )
    )

    client = TestClient(app)
    with client:
        # Save a fake SSH connection
        ssh_conn = app.state.store.save_ssh_connection(
            {
                "host": "remote.test",
                "port": 2222,
                "user": "ubuntu",
                "display_name": "Test Server",
            },
            state="configured",
            detail="ready",
        )
        cid = ssh_conn["id"]

        # Create a session belonging to this SSH connection
        app.state.store.upsert_session(
            {
                "id": "session-remote-1",
                "agent_id": f"ssh-hermes-{cid}",
                "title": "Remote Session",
                "workspace": "/home/ubuntu/project",
                "connection_id": cid,
                "status": "idle",
                "updated_at": "2026-09-15T00:00:00Z",
            }
        )

        captured_cmds = []

        def fake_subprocess_run(cmd, **kwargs):
            captured_cmds.append(cmd)
            class Completed:
                returncode = 0
                stdout = "diff --git a/test.py b/test.py\n+hello"
                stderr = ""
            return Completed()

        monkeypatch.setattr("subprocess.run", fake_subprocess_run)

        headers = {"Authorization": "Bearer test-secret"}

        # 1. Call diff-raw with connection_id and workspace
        resp = client.get(
            f"/api/v1/git/diff-raw?workspace=/home/ubuntu/project&connection_id={cid}",
            headers=headers,
        )
        assert resp.status_code == 200
        assert "diff --git" in resp.text
        assert len(captured_cmds) > 0
        # verify that ssh command was used, not local git
        first_cmd = captured_cmds[0]
        assert "ssh" in first_cmd[0] or "ssh.exe" in first_cmd[0]
        assert "remote.test" in first_cmd
        assert "-p" in first_cmd and "2222" in first_cmd
        assert "-l" in first_cmd and "ubuntu" in first_cmd

        # 2. Call diff-raw with only session_id (no explicit connection_id or workspace)
        captured_cmds.clear()
        resp_sess = client.get(
            "/api/v1/git/diff-raw?session_id=session-remote-1",
            headers=headers,
        )
        assert resp_sess.status_code == 200
        assert "diff --git" in resp_sess.text
        assert len(captured_cmds) > 0
        assert "remote.test" in captured_cmds[0]

        # 3. Call git/status with connection_id and workspace
        captured_cmds.clear()
        resp_status = client.get(
            f"/api/v1/git/status?workspace=/home/ubuntu/project&connection_id={cid}",
            headers=headers,
        )
        assert resp_status.status_code == 200
        assert resp_status.json()["workspace"] == "/home/ubuntu/project"
        assert len(captured_cmds) > 0
        assert "remote.test" in captured_cmds[0]
