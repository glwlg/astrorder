from __future__ import annotations
import sys
try:
    sys.stdout.reconfigure(encoding="utf-8")
except Exception:
    pass
"""Hot-link installed Astrorder server to live development repository.

Zero-reinstall, zero-repack development workflow:
1. Links Python .pth in installed server to live workspace backend/src and connectors.
2. Directs ASTRORDER_STATIC_DIR to live frontend/dist.
3. Reloads App Server cleanly without stopping Session Daemon or interrupting active sessions.
"""

import json
import os
import shutil
import subprocess
import sys
import time
from pathlib import Path

DEV_ROOT = Path(__file__).resolve().parents[1]
INSTALLED_ROOT = Path("P:/DevApp/Astrorder/server")

def hot_sync(*, restart_backend: bool = True) -> None:
    print(f"[HotSync] Linking installed server ({INSTALLED_ROOT}) to dev workspace ({DEV_ROOT})...")
    if not INSTALLED_ROOT.is_dir():
        print(f"[HotSync] Installed server directory {INSTALLED_ROOT} not found.")
        return

    # 1. Update Python .pth in installed environment
    site_packages = INSTALLED_ROOT / "backend/.venv/Lib/site-packages"
    if site_packages.is_dir():
        (site_packages / "_editable_impl_astrorder_connectors_root.pth").write_text(
            str(DEV_ROOT), encoding="utf-8"
        )
        (site_packages / "_editable_impl_astrorder_server.pth").write_text(
            str(DEV_ROOT / "backend/src"), encoding="utf-8"
        )
        (site_packages / "_editable_impl_astrorder_codex_connector.pth").write_text(
            str(DEV_ROOT / "connectors/codex"), encoding="utf-8"
        )
        (site_packages / "_editable_impl_astrorder_hermes_connector.pth").write_text(
            str(DEV_ROOT / "connectors/hermes"), encoding="utf-8"
        )
        print("  [OK] Linked Python .pth to live backend/src & connectors")

    # 2. Sync runtime scripts, connectors & .hermes
    shutil.copytree(DEV_ROOT / "scripts", INSTALLED_ROOT / "scripts", dirs_exist_ok=True)
    shutil.copytree(DEV_ROOT / "backend/src", INSTALLED_ROOT / "backend/src", dirs_exist_ok=True)
    shutil.copytree(DEV_ROOT / "connectors", INSTALLED_ROOT / "connectors", dirs_exist_ok=True)
    shutil.copytree(DEV_ROOT / ".hermes", INSTALLED_ROOT / ".hermes", dirs_exist_ok=True)
    print("  [OK] Synced live scripts, plugins & connectors metadata")

    # 3. Point static dir to dev frontend/dist
    conf_paths = [Path(os.environ["LOCALAPPDATA"]) / "Astrorder/production.json"]
    for cp in conf_paths:
        if cp.is_file():
            try:
                data = json.loads(cp.read_text(encoding="utf-8"))
                env = data.setdefault("environment", {})
                env["ASTRORDER_STATIC_DIR"] = str(DEV_ROOT / "frontend/dist")
                database = Path(os.environ["LOCALAPPDATA"]) / "Astrorder/data/astrorder.sqlite3"
                env["ASTRORDER_DATABASE_URL"] = f"sqlite:///{database.as_posix()}"
                env["ASTRORDER_ALLOWED_WORKSPACES"] = "P:/workspace,C:/Users/luwei,P:\\DevApp\\Astrorder\\server"
                env["ASTRORDER_DAEMON_CODEX_WORKSPACE"] = "P:\\workspace\\glwlg\\ai\\astrorder"
                env["ASTRORDER_DAEMON_GROK_WORKSPACE"] = "P:\\workspace\\glwlg\\ai\\astrorder"
                cp.write_text(json.dumps(data, indent=2, ensure_ascii=False), encoding="utf-8")
            except Exception:
                pass
    print("  [OK] Configured live frontend/dist & unified database")

    # 4. Restart backend app server if requested
    if restart_backend:
        print('[HotSync] Restarting App Server in 1 second (Session Daemon stays alive)...')
        venv_python = INSTALLED_ROOT / 'backend/.venv/Scripts/python.exe'
        desktop_service_script = INSTALLED_ROOT / 'scripts/desktop_service.py'
        try:
            # 获取当前运行中的 Astrorder 进程 PID
            run_env = os.environ.copy()
            try:
                ps = 'Get-Process Astrorder -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Id'
                pids = [int(line.strip()) for line in subprocess.check_output(['powershell', '-Command', ps]).decode().split() if line.strip()]
                if pids:
                    run_env['ASTRORDER_DESKTOP_PID'] = str(pids[0])
            except Exception:
                pass
            run_res = subprocess.run(
                [str(venv_python), str(desktop_service_script), 'app', 'restart'],
                cwd=str(INSTALLED_ROOT),
                capture_output=True,
                text=True,
                encoding='utf-8',
                check=False,
                env=run_env,
            )
            out = run_res.stdout.strip() or run_res.stderr.strip()
            if run_res.returncode == 0:
                print(f'  [OK] App Server restarted successfully: {out}')
            else:
                print(f'  ! App Server restart failed (code {run_res.returncode}): {out}')
        except Exception as e:
            print(f'  ! App Server restart warning: {e}')

    print('[HotSync] Complete! In Desktop client press Ctrl+R to reload UI.')

if __name__ == '__main__':
    hot_sync()
