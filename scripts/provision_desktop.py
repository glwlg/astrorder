"""Create machine-local production settings for a bundled desktop server."""
from __future__ import annotations

import json
import os
import secrets
import shutil
from pathlib import Path

from run_production import protect_bytes

ROOT = Path(__file__).resolve().parents[1]


def provision() -> None:
    local_app_data = os.environ.get("LOCALAPPDATA")
    if not local_app_data:
        raise RuntimeError("LOCALAPPDATA is unavailable")
    durable = Path(local_app_data) / "Astrorder"
    config = durable / "production.json"
    credentials = durable / "production.credentials.dpapi"
    data = durable / "data"
    data.mkdir(parents=True, exist_ok=True)
    dest_attachments = data / "attachments"
    dest_attachments.mkdir(parents=True, exist_ok=True)

    # 2. 配置 Python 模块查找路径
    python = ROOT / "backend/.venv/Lib/site-packages"
    if python.is_dir():
        (python / "_editable_impl_astrorder_server.pth").write_text(
            str(ROOT / "backend/src"), encoding="utf-8"
        )
        (python / "_editable_impl_astrorder_codex_connector.pth").write_text(
            str(ROOT / "connectors/codex"), encoding="utf-8"
        )
        (python / "_editable_impl_astrorder_hermes_connector.pth").write_text(
            str(ROOT / "connectors/hermes"), encoding="utf-8"
        )

    # 3. 凭据只保存在用户正式数据目录
    if not credentials.is_file():
        secret_values = {
            "ASTRORDER_BROWSER_SECRET": secrets.token_urlsafe(32),
            "ASTRORDER_CONNECTOR_SECRET": secrets.token_urlsafe(32),
            "ASTRORDER_SESSION_DAEMON_SECRET": secrets.token_urlsafe(32),
        }
        credentials.write_bytes(protect_bytes(json.dumps(secret_values).encode("utf-8")))

    # 4. 配置合并与刷新
    payload = {"environment": {}}
    if config.is_file():
        try:
            payload = json.loads(config.read_text(encoding="utf-8"))
        except Exception:
            pass

    codex = shutil.which("codex")
    hermes = shutil.which("hermes")
    grok = shutil.which("grok")
    allowed = ["P:/workspace", "C:/Users/luwei", str(Path.home()), str(ROOT)]
    environment = payload.setdefault("environment", {})
    environment.update({
        "ASTRORDER_HOST": "0.0.0.0",
        "ASTRORDER_PORT": "30001",
        "ASTRORDER_DATABASE_URL": f"sqlite:///{(data / 'astrorder.sqlite3').as_posix()}",
        "ASTRORDER_ATTACHMENTS_DIR": str(data / "attachments"),
        "ASTRORDER_STATIC_DIR": str(ROOT / "frontend/dist"),
        "ASTRORDER_ALLOWED_ORIGINS": "http://127.0.0.1:30001,http://localhost:30001,http://192.168.1.11:30001,http://ao.651971564.xyz,https://ao.651971564.xyz",
        "ASTRORDER_PUBLIC_URL": "https://ao.651971564.xyz",
        "ASTRORDER_ALLOWED_WORKSPACES": ",".join(allowed),
        "ASTRORDER_SESSION_DAEMON_ENABLED": "1",
        "ASTRORDER_SESSION_DAEMON_ENDPOINT": "ws://127.0.0.1:30009",
        "ASTRORDER_DAEMON_PTY_ENABLED": "1",
        "ASTRORDER_DAEMON_SSH_ENABLED": "1",
        "ASTRORDER_DAEMON_GROK_ENABLED": "1" if grok else "0",
        "ASTRORDER_GROK_EXECUTABLE": grok or "",
        "ASTRORDER_DAEMON_GROK_WORKSPACE": "P:\\workspace\\glwlg\\ai\\astrorder" if Path("P:/workspace/glwlg/ai/astrorder").is_dir() else str(ROOT),
        "ASTRORDER_AUTO_CONNECT_LOCAL_HERMES": "1" if hermes else "0",
        "ASTRORDER_DAEMON_HERMES_ENABLED": "1" if hermes else "0",
        "ASTRORDER_DAEMON_CODEX_ENABLED": "1" if codex else "0",
        "ASTRORDER_DAEMON_CODEX_WORKSPACE": "P:\\workspace\\glwlg\\ai\\astrorder" if Path("P:/workspace/glwlg/ai/astrorder").is_dir() else str(ROOT),
    })
    if codex:
        environment["ASTRORDER_CODEX_EXECUTABLE"] = codex
    if hermes:
        environment["ASTRORDER_HERMES_EXECUTABLE"] = hermes
    config.write_text(json.dumps(payload, indent=2, ensure_ascii=False), encoding="utf-8")


if __name__ == "__main__":
    provision()
