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

    # 1. 确保将最新开发版/打包版数据库与附件同步到持久化数据目录
    dest_db = data / "astrorder.sqlite3"
    dev_db = Path("P:/workspace/glwlg/ai/astrorder/astrorder.sqlite3")
    bundled_db = ROOT / "data" / "astrorder.sqlite3"
    legacy_database = ROOT / "astrorder.sqlite3"

    source_db = (
        dev_db if dev_db.is_file()
        else bundled_db if bundled_db.is_file()
        else legacy_database if legacy_database.is_file()
        else None
    )

    if source_db and source_db.is_file():
        # 如果持久化目标库不存在，或者源库体积更大/修改时间更新，则覆盖同步
        should_copy = not dest_db.exists() or dest_db.stat().st_size < 10000000
        if not should_copy and source_db != dest_db:
            try:
                if source_db.stat().st_size > dest_db.stat().st_size:
                    should_copy = True
            except Exception:
                pass
        if should_copy:
            for ext in ["-wal", "-shm"]:
                wal = data / f"astrorder.sqlite3{ext}"
                if wal.exists():
                    try:
                        wal.unlink()
                    except Exception:
                        pass
            shutil.copy2(source_db, dest_db)

    # 同步附件目录
    dest_attachments = data / "attachments"
    dest_attachments.mkdir(parents=True, exist_ok=True)
    dev_attachments = Path("P:/workspace/glwlg/ai/astrorder/data/attachments")
    legacy_attachments = ROOT / "data/attachments"
    source_attachments = dev_attachments if dev_attachments.is_dir() else legacy_attachments if legacy_attachments.is_dir() else None
    if source_attachments and source_attachments.is_dir() and source_attachments != dest_attachments:
        for f in source_attachments.iterdir():
            if f.is_file():
                df = dest_attachments / f.name
                if not df.exists():
                    try:
                        shutil.copy2(f, df)
                    except Exception:
                        pass

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

    # 3. 凭据同步
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

    # 4. 配置合并与刷新
    payload = {"environment": {}}
    if config.is_file():
        try:
            payload = json.loads(config.read_text(encoding="utf-8"))
        except Exception:
            pass
    elif durable_config.is_file():
        try:
            payload = json.loads(durable_config.read_text(encoding="utf-8"))
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
    shutil.copy2(config, durable_config)


if __name__ == "__main__":
    provision()
