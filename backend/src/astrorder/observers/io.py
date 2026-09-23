"""Observer installation and spool IO; stdlib-only for execution on SSH hosts."""
import hashlib
import json
import os
import re
import shlex
import shutil
import subprocess
import time
from pathlib import Path

EVENTS = ('SessionStart','SessionEnd','UserPromptSubmit','PreToolUse','PostToolUse','PermissionRequest','SubagentStart','SubagentStop','Stop','Interrupt')
MARKER = 'Astrorder passive observation'

def install_observer(home, python, source):
    home=Path(home); directory=home/'astrorder-observer'; directory.mkdir(parents=True,exist_ok=True,mode=0o700)
    script=directory/'observer.py'
    config=home/'hooks.json'
    before=config.read_bytes() if config.exists() else None
    data=json.loads(before) if before else {}
    if not isinstance(data,dict) or not isinstance(data.get('hooks',{}),dict):
        raise ValueError('Invalid existing hooks config; not overwritten')
    data.setdefault('hooks',{})
    argv=[str(python),str(script)]
    command=subprocess.list2cmdline(argv) if os.name=='nt' else shlex.join(argv)
    for event in EVENTS:
        groups=data['hooks'].setdefault(event,[])
        if not isinstance(groups,list): raise ValueError('Invalid hook group')
        kept=[]
        for group in groups:
            # Remove only this installer's own handlers, preserving every unrelated hook.
            own=lambda h: h.get('statusMessage')==MARKER and 'astrorder-observer' in h.get('command','')
            handlers=[h for h in group.get('hooks',[]) if not own(h)]
            if handlers or not group.get('hooks'): kept.append({**group,'hooks':handlers})
        kept.append({'hooks':[{'type':'command','command':command,'timeout':600 if event=='PermissionRequest' else 3,'statusMessage':MARKER}]})
        data['hooks'][event]=kept
    after=(json.dumps(data,ensure_ascii=False,indent=2)+'\n').encode('utf-8')
    if before and before!=after:
        backup=directory/('hooks-before-'+str(time.time_ns())+'.json')
        backup.write_bytes(before); backup.chmod(0o600)
    if script.exists() and script.read_text(encoding='utf-8')!=source:
        shutil.copy2(script,directory/('observer-before-'+str(time.time_ns())+'.py'))
    script.write_text(source,encoding='utf-8'); script.chmod(0o700)
    temp=directory/'hooks-next.json'; temp.write_bytes(after); temp.chmod(0o600); os.replace(temp,config)
    assert json.loads(config.read_text(encoding='utf-8'))==data and script.read_text(encoding='utf-8')==source
    return {'installed':True,'events':list(EVENTS),'script_sha256':hashlib.sha256(source.encode()).hexdigest(),'trust':'requires_native_review'}

def read_spool(home, acknowledge=(), pending=()):
    directory=Path(home)/'astrorder-observer'
    spool=directory/'events'
    for key in acknowledge:
        if isinstance(key,str) and re.fullmatch(r'[a-f0-9]{32}(?:[a-f0-9]{32})?',key):
            (spool/(key+'.json')).unlink(missing_ok=True)
    items=[]
    for path in sorted(spool.glob('*.json'),key=lambda p:p.stat().st_mtime)[:100]:
        if path.is_symlink() or path.stat().st_size>8192: continue
        try:
            row=json.loads(path.read_text(encoding='utf-8'))
            if isinstance(row,dict) and path.stem==row.get('id'): items.append(row)
        except (ValueError,OSError): continue
    present=[key for key in pending if isinstance(key,str) and re.fullmatch(r'[a-f0-9]{32}(?:[a-f0-9]{32})?',key) and (spool/(key+'.json')).is_file()]
    return {'installed':(directory/'observer.py').is_file(),'items':items,'pending':present}

def write_decision(home, approval_id, decision):
    if not isinstance(approval_id,str) or not re.fullmatch(r'[a-f0-9]{32}(?:[a-f0-9]{32})?',approval_id): return False
    if decision not in ('allow','deny'): return False
    directory=Path(home)/'astrorder-observer'; event=directory/'events'/(approval_id+'.json')
    try:
        row=json.loads(event.read_text(encoding='utf-8'))
        if row.get('id')!=approval_id or row.get('event')!='PermissionRequest' or row.get('approval_pending') is not True: return False
        decisions=directory/'decisions'; decisions.mkdir(mode=0o700,parents=True,exist_ok=True)
        temp=decisions/(approval_id+'.tmp'); target=decisions/(approval_id+'.json')
        temp.write_text(json.dumps({'id':approval_id,'decision':decision}),encoding='utf-8'); temp.chmod(0o600); os.replace(temp,target)
        return True
    except (OSError,ValueError,AttributeError): return False
