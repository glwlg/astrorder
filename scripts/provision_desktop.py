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
    runtime = ROOT / ".runtime"
    local_app_data = os.environ.get("LOCALAPPDATA")
    if not local_app_data:
        raise RuntimeError("LOCALAPPDATA is unavailable")
    durable = Path(local_app_data) / "Astrorder"
    config = runtime / "production.json"
    credentials = runtime / "production.credentials.dpapi"
    durable_config = durable / config.name
    durable_credentials = durable / credentials.name
    data = durable / "data"
    runtime.mkdir(exist_ok=True)
    data.mkdir(parents=True, exist_ok=True)
    legacy_database = ROOT / "astrorder.sqlite3"
    if legacy_database.is_file() and not (data / legacy_database.name).exists():
        shutil.copy2(legacy_database, data / legacy_database.name)
    legacy_attachments = ROOT / "data/attachments"
    if legacy_attachments.is_dir() and not (data / "attachments").exists():
        shutil.copytree(legacy_attachments, data / "attachments")

    python = ROOT / "backend/.venv/Lib/site-packages"
    (python / "_editable_impl_astrorder_server.pth").write_text(
        str(ROOT / "backend/src"), encoding="utf-8"
    )
    (python / "_editable_impl_astrorder_codex_connector.pth").write_text(
        str(ROOT / "connectors/codex"), encoding="utf-8"
    )

    if credentials.is_file():
        shutil.copy2(credentials, durable_credentials)
    elif durable_credentials.is_file():
        shutil.copy2(durable_credentials, credentials)
    else:
        secret_values = {
            "ASTRORDER_BROWSER_SECRET": secrets.token_urlsafe(32),
            "ASTRORDER_CONNECTOR_SECRET": secrets.token_urlsafe(32),
            "ASTRORDER_SESSION_DAEMON_SECRET": secrets.token_urlsafe(32),
        }
        credentials.write_bytes(protect_bytes(json.dumps(secret_values).encode("utf-8")))
        shutil.copy2(credentials, durable_credentials)

    if config.is_file():
        payload = json.loads(config.read_text(encoding="utf-8"))
    elif durable_config.is_file():
        payload = json.loads(durable_config.read_text(encoding="utf-8"))
    else:
        payload = {"environment": {}}

    codex = shutil.which("codex")
    hermes = shutil.which("hermes")
    grok = shutil.which("grok")
    allowed = [str(Path.home()), str(ROOT)]
    environment = payload["environment"]
    environment.update({
        "ASTRORDER_HOST": "0.0.0.0",
        "ASTRORDER_PORT": "30001",
        "ASTRORDER_DATABASE_URL": f"sqlite:///{(data / 'astrorder.sqlite3').as_posix()}",
        "ASTRORDER_ATTACHMENTS_DIR": str(data / "attachments"),
        "ASTRORDER_STATIC_DIR": str(ROOT / "frontend/dist"),
        "ASTRORDER_ALLOWED_ORIGINS": "http://127.0.0.1:30001,http://localhost:30001",
        "ASTRORDER_ALLOWED_WORKSPACES": ",".join(allowed),
        "ASTRORDER_SESSION_DAEMON_ENABLED": "1",
        "ASTRORDER_SESSION_DAEMON_ENDPOINT": "ws://127.0.0.1:30009",
        "ASTRORDER_DAEMON_PTY_ENABLED": "1",
        "ASTRORDER_DAEMON_SSH_ENABLED": "1",
        "ASTRORDER_DAEMON_GROK_ENABLED": "1" if grok else "0",
        "ASTRORDER_GROK_EXECUTABLE": grok or "",
        "ASTRORDER_DAEMON_GROK_WORKSPACE": str(ROOT),
        "ASTRORDER_AUTO_CONNECT_LOCAL_HERMES": "1" if hermes else "0",
        "ASTRORDER_DAEMON_HERMES_ENABLED": "1" if hermes else "0",
        "ASTRORDER_DAEMON_CODEX_ENABLED": "1" if codex else "0",
        "ASTRORDER_DAEMON_CODEX_WORKSPACE": str(ROOT),
    })
    if codex:
        environment["ASTRORDER_CODEX_EXECUTABLE"] = codex
    if hermes:
        environment["ASTRORDER_HERMES_EXECUTABLE"] = hermes
    config.write_text(json.dumps(payload, indent=2), encoding="utf-8")
    shutil.copy2(config, durable_config)


if __name__ == "__main__":
    provision()
