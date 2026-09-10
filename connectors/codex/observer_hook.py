"""Passive Codex hook: bounded metadata spool, no network, no model/approval output."""
from __future__ import annotations
import argparse
import hashlib
import json
import os
from pathlib import Path
import sys
import time
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
        return
    target=spool/(row['id']+'.json')
    if target.exists():
        return
    temp=spool/(uuid4().hex+'.tmp')
    try:
        temp.write_text(json.dumps(row),encoding='utf-8')
        try: temp.chmod(0o600)
        except OSError: pass
        os.replace(temp,target)
    finally:
        temp.unlink(missing_ok=True)

def main():
    try:
        parser=argparse.ArgumentParser()
        parser.add_argument('--spool',type=Path,default=Path(__file__).resolve().parent/'events')
        args=parser.parse_args()
        raw=sys.stdin.buffer.read(2*1024*1024+1)
        if len(raw)>2*1024*1024: return
        write_observation(json.loads(raw),args.spool)
    except Exception:
        # Observation must never block, approve, deny or alter the native turn.
        return

if __name__=='__main__':
    main()
