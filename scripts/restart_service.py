import subprocess, sys, time
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

# 1. Kill old process listening on 30001
try:
    out = subprocess.check_output(['netstat', '-ano']).decode('latin-1', errors='ignore')
    for line in out.splitlines():
        if ':30001' in line and 'LISTENING' in line:
            pid = line.strip().split()[-1]
            print('Terminating old PID:', pid)
            subprocess.run(['taskkill', '/F', '/PID', pid], capture_output=True)
except Exception as e:
    print('Netstat error:', e)

time.sleep(1)

# 2. Start new production server
log = open(ROOT / '.runtime/production.log', 'ab', buffering=0)
python_exe = str(ROOT / 'backend/.venv/Scripts/python.exe')
DETACHED = 0x00000008
proc = subprocess.Popen([python_exe, 'scripts/run_production.py'], cwd=str(ROOT), stdout=log, stderr=log, creationflags=DETACHED)
print('Started new production process PID:', proc.pid)
