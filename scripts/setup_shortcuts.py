import os
import sys
from pathlib import Path
import win32com.client

ROOT = Path(__file__).resolve().parent.parent
VBS_PATH = ROOT / "scripts" / "start_silent.vbs"
ICON_PATH = ROOT / "frontend" / "public" / "favicon.ico"

USER_HOME = Path.home()
STARTUP_DIR = USER_HOME / "AppData" / "Roaming" / "Microsoft" / "Windows" / "Start Menu" / "Programs" / "Startup"
DESKTOP_DIR = USER_HOME / "Desktop"

def create_shortcut(target_lnk: Path, name: str):
    shell = win32com.client.Dispatch("WScript.Shell")
    shortcut = shell.CreateShortcut(str(target_lnk))
    # 目标为系统自带的 wscript.exe，参数为 start_silent.vbs
    shortcut.TargetPath = "wscript.exe"
    shortcut.Arguments = f'"{VBS_PATH}"'
    shortcut.WorkingDirectory = str(ROOT)
    shortcut.Description = "星序 · Astrorder (后台静默运行)"
    if ICON_PATH.exists():
        shortcut.IconLocation = f"{ICON_PATH},0"
    shortcut.Save()
    print(f"Created shortcut: {target_lnk}")

def main():
    print(f"Project root: {ROOT}")
    print(f"VBS launcher: {VBS_PATH}")
    
    # 1. 开机自启动快捷方式
    if STARTUP_DIR.exists():
        startup_lnk = STARTUP_DIR / "星序 · Astrorder.lnk"
        create_shortcut(startup_lnk, "开机启动")
    else:
        print(f"Startup dir not found: {STARTUP_DIR}")

    # 2. 桌面快捷方式 (带 Astrorder 专属 favicon 图标)
    if DESKTOP_DIR.exists():
        desktop_lnk = DESKTOP_DIR / "星序 · Astrorder.lnk"
        create_shortcut(desktop_lnk, "桌面启动")
    else:
        print(f"Desktop dir not found: {DESKTOP_DIR}")

if __name__ == "__main__":
    main()
