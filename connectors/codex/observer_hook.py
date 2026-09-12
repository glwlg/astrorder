"""Codex observation hook; permission requests may be decided by Astrorder."""
from __future__ import annotations
import argparse
import hashlib
import json
import os
import re
import sys
import time
from pathlib import Path
from uuid import uuid4

EVENTS = ('SessionStart','SessionEnd','UserPromptSubmit','PreToolUse','PostToolUse','PermissionRequest','SubagentStart','SubagentStop','Stop','Interrupt')

def observation(payload):
    if not isinstance(payload, dict) or payload.get('hook_event_name') not in EVENTS:
        raise ValueError('Unsupported event')
    sid = payload.get('session_id')
    if not isinstance(sid,str) or not sid or len(sid)>256 or any(ord(c)<32 for c in sid):
        raise ValueError('Native session ID missing')
    result = {'session_id':sid, 'event':payload['hook_event_name'], 'observed_at':time.time()}
    for key in ('turn_id','tool_call_id','tool_name','agent_id'):
        value=payload.get(key)
        if isinstance(value,str) and value and len(value)<=160 and all(ord(c)>=32 for c in value):
            result[key if key!='agent_id' else 'subagent_id']=value
    identity={key:value for key,value in result.items() if key!='observed_at'}
    # Turn terminal/user events have a native identity; tool events without call IDs do not.
    stable = bool(result.get('turn_id')) and (result['event'] in ('UserPromptSubmit','Stop','Interrupt') or bool(result.get('tool_call_id')))
    result['id']=hashlib.sha256(json.dumps(identity,sort_keys=True).encode()).hexdigest() if stable else uuid4().hex
    if result['event']=='PermissionRequest':
        tool_input=payload.get('tool_input') if isinstance(payload.get('tool_input'),dict) else {}
        detail=payload.get('description') or payload.get('reason') or payload.get('command') or tool_input.get('command') or ''
        detail=re.sub(r'(?i)(api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|passwd|secret|authorization)\s*[:=]\s*\S+',r'\1=[REDACTED]',str(detail))
        result['detail']=re.sub(r'\s+',' ',detail).strip()[:2000]
        result['approval_pending']=True
    return result

def write_observation(payload, spool):
    row=observation(payload)
    spool.mkdir(mode=0o700,parents=True,exist_ok=True)
    # A stopped server must not cause unbounded growth. No message bodies are retained.
    old=sorted(spool.glob('*.json'),key=lambda p:p.stat().st_mtime)
    for path in old:
        if path.stat().st_mtime < time.time()-86400:
            path.unlink(missing_ok=True)
    if sum(1 for _ in spool.glob('*.json'))>=2000:
        return None
    target=spool/(row['id']+'.json')
    if target.exists():
        return row
    temp=spool/(uuid4().hex+'.tmp')
    try:
        temp.write_text(json.dumps(row),encoding='utf-8')
        try: temp.chmod(0o600)
        except OSError: pass
        os.replace(temp,target)
    finally:
        temp.unlink(missing_ok=True)
    return row

def wait_for_decision(row, spool, timeout):
    if not row or not row.get('approval_pending'): return None
    directory=spool.parent/'decisions'; target=directory/(row['id']+'.json')
    end=time.monotonic()+timeout
    while time.monotonic()<end:
        try:
            decision=json.loads(target.read_text(encoding='utf-8'))
            if decision.get('id')==row['id'] and decision.get('decision') in ('allow','deny'):
                return decision['decision']
        except (FileNotFoundError,OSError,ValueError,AttributeError): pass
        time.sleep(.1)
    return None

def main():
    try:
        parser=argparse.ArgumentParser()
        parser.add_argument('--spool',type=Path,default=Path(__file__).resolve().parent/'events')
        parser.add_argument('--approval-timeout',type=float,default=590)
        args=parser.parse_args()
        raw=sys.stdin.buffer.read(2*1024*1024+1)
        if len(raw)>2*1024*1024: return
        row=write_observation(json.loads(raw),args.spool)
        decision=wait_for_decision(row,args.spool,max(0,args.approval_timeout))
        if row and row.get('approval_pending'):
            (args.spool/(row['id']+'.json')).unlink(missing_ok=True)
            (args.spool.parent/'decisions'/(row['id']+'.json')).unlink(missing_ok=True)
        if decision:
            print(json.dumps({'hookSpecificOutput':{'hookEventName':'PermissionRequest','decision':{'behavior':decision}}}))
    except Exception:
        # A failed bridge makes no decision, so Codex falls back to native approval.
        return

if __name__=='__main__':
    main()
