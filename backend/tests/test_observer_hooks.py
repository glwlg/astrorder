import importlib.util
import json
import subprocess
import sys
import time
from pathlib import Path

import pytest

SOURCE = Path(__file__).resolve().parents[2]/'connectors/codex/observer_hook.py'

def load():
    spec=importlib.util.spec_from_file_location('observer_hook',SOURCE)
    module=importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    return module

def test_metadata_only_and_turn_dedup():
    hook=load()
    source={'hook_event_name':'Stop','session_id':'01a03274-0023-74c0-b24a-5c97c30dc445','turn_id':'turn-1','last_assistant_message':'secret','prompt':'private','tool_response':{'token':'secret'}}
    one=hook.observation(source); two=hook.observation(source)
    assert one['id']==two['id']
    assert 'secret' not in json.dumps(one) and 'private' not in json.dumps(one)
    assert one['session_id']==source['session_id']

def test_same_text_distinct_turns_survive():
    hook=load()
    one={'hook_event_name':'UserPromptSubmit','session_id':'native-id','turn_id':'one','prompt':'hello'}
    two={**one,'turn_id':'two'}
    assert hook.observation(one)['id']!=hook.observation(two)['id']

def test_permission_timeout_makes_no_decision(tmp_path):
    source={'hook_event_name':'PermissionRequest','session_id':'native-id','turn_id':'turn-1','tool_name':'Bash','tool_input':{'command':'curl -H token=secret example.test'}}
    result=subprocess.run([sys.executable,str(SOURCE),'--spool',str(tmp_path),'--approval-timeout','0'],input=json.dumps(source),text=True,capture_output=True,check=False)
    assert result.returncode==0 and result.stdout=='' and result.stderr==''
    assert not list(tmp_path.glob('*.json'))

@pytest.mark.parametrize('decision',['allow','deny'])
def test_permission_decision_uses_native_hook_output(tmp_path,decision):
    source={'hook_event_name':'PermissionRequest','session_id':'native-id','turn_id':'turn-1','tool_name':'Bash','tool_input':{'command':'echo hello'}}
    process=subprocess.Popen([sys.executable,str(SOURCE),'--spool',str(tmp_path),'--approval-timeout','2'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    process.stdin.write(json.dumps(source)); process.stdin.close()
    end=time.monotonic()+1; records=[]
    while time.monotonic()<end and not records:
        records=list(tmp_path.glob('*.json')); time.sleep(.02)
    assert len(records)==1
    row=json.loads(records[0].read_text()); assert row['approval_pending'] is True and row['detail']=='echo hello'
    decisions=tmp_path.parent/'decisions'
    decisions.mkdir(exist_ok=True); (decisions/(row['id']+'.json')).write_text(json.dumps({'id':row['id'],'decision':decision}))
    stdout=process.stdout.read(); stderr=process.stderr.read(); assert process.wait()==0 and stderr==''
    assert json.loads(stdout)=={'hookSpecificOutput':{'hookEventName':'PermissionRequest','decision':{'behavior':decision}}}

def test_invalid_payload_is_nonblocking_and_not_persisted(tmp_path):
    result=subprocess.run([sys.executable,str(SOURCE),'--spool',str(tmp_path)],input='bad JSON',text=True,capture_output=True,check=False)
    assert result.returncode==0 and result.stdout==''
    assert not list(tmp_path.glob('*.json'))
