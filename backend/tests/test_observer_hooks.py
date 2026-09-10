import importlib.util
import json
import subprocess
import sys
from pathlib import Path

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

def test_command_never_writes_model_context_or_permission_decisions(tmp_path):
    source={'hook_event_name':'PermissionRequest','session_id':'native-id','turn_id':'turn-1','tool_name':'Bash','tool_input':{'command':'private'}}
    result=subprocess.run([sys.executable,str(SOURCE),'--spool',str(tmp_path)],input=json.dumps(source),text=True,capture_output=True)
    assert result.returncode==0 and result.stdout=='' and result.stderr==''
    records=list(tmp_path.glob('*.json')); assert len(records)==1
    assert json.loads(records[0].read_text())['event']=='PermissionRequest'
    assert 'private' not in records[0].read_text()

def test_invalid_payload_is_nonblocking_and_not_persisted(tmp_path):
    result=subprocess.run([sys.executable,str(SOURCE),'--spool',str(tmp_path)],input='bad JSON',text=True,capture_output=True)
    assert result.returncode==0 and result.stdout==''
    assert not list(tmp_path.glob('*.json'))
