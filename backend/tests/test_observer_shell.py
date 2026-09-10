import json
import subprocess
import sys
from pathlib import Path
from unittest.mock import Mock
import pytest
from astrorder import observer_plugin as plugin


@pytest.mark.parametrize('shell',[['bash','-c'],['powershell.exe','-NoProfile','-NonInteractive','-Command']])
def test_generated_hook_executes_through_shell_with_spaced_windows_paths(tmp_path,monkeypatch,shell):
    home=tmp_path/'codex home'
    monkeypatch.setattr(plugin.subprocess,'run',Mock(return_value=Mock(returncode=0)))
    monkeypatch.setattr(plugin,'verify_plugin',Mock(return_value={'plugin_hooks':10,'source':'plugin'}))
    source=(Path(__file__).resolve().parents[2]/'connectors/codex/observer_hook.py').read_text(encoding='utf-8')
    with monkeypatch.context() as scoped:
        # Installation uses the mock, execution below uses the actual shell.
        plugin.install_plugin(home,sys.executable,source,'codex')
    command=json.loads((home/'astrorder-observer/marketplace/plugins/astrorder/hooks/hooks.json').read_text(encoding='utf-8'))['hooks']['Stop'][0]['hooks'][0]['command']
    monkeypatch.undo()
    result=subprocess.run([*shell,command],input=json.dumps({'hook_event_name':'Stop','session_id':'01992890-4444-7777-8888-000000000001','turn_id':'shell-test'}),text=True,errors='replace',capture_output=True,check=False)
    assert result.returncode==0 and result.stdout=='' and result.stderr==''
    events=list((home/'astrorder-observer/events').glob('*.json'))
    assert len(events)==1
    assert json.loads(events[0].read_text())['event']=='Stop'
