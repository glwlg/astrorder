import subprocess, sys, time, os
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

# 1. 如果已有进程占用 30001，先结束
try:
    out = subprocess.check_output(['netstat', '-ano']).decode('latin-1', errors='ignore')
    for line in out.splitlines():
        if ':30001' in line and 'LISTENING' in line:
            pid = line.strip().split()[-1]
            subprocess.run(['taskkill', '/F', '/PID', pid], capture_output=True)
except Exception:
    pass

time.sleep(1)

# 2. 静默启动生产服务
log = open(ROOT / '.runtime/production.log', 'ab', buffering=0)
python_exe = str(ROOT / 'backend/.venv/Scripts/pythonw.exe')
if not Path(python_exe).exists():
    python_exe = str(ROOT / 'backend/.venv/Scripts/python.exe')

DETACHED_PROCESS = 0x00000008
CREATE_NO_WINDOW = 0x08000000

proc = subprocess.Popen(
    [python_exe, 'scripts/run_production.py'],
    cwd=str(ROOT),
    stdout=log,
    stderr=log,
    env=os.environ.copy(),
    creationflags=DETACHED_PROCESS | CREATE_NO_WINDOW
)
