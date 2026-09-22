import json
import time
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock

from fastapi.testclient import TestClient

from astrorder.config import Settings
from astrorder.main import create_app
from astrorder.native_observers import NativeObservers
from astrorder.observer_io import install_observer, read_spool

SID='01992890-4444-7777-8888-000000000001'
def test_install_is_idempotent_preserves_other_hooks_and_drains_metadata(tmp_path):
    original={'hooks':{'Stop':[{'hooks':[{'type':'command','command':'existing-hook'}]}]}}
    (tmp_path/'hooks.json').write_text(json.dumps(original))
    source=(Path(__file__).resolve().parents[2]/'connectors/codex/observer_hook.py').read_text()
    install_observer(tmp_path,'python',source)
    first=(tmp_path/'hooks.json').read_bytes()
    install_observer(tmp_path,'python',source)
    assert (tmp_path/'hooks.json').read_bytes()==first
    assert json.loads(first)['hooks']['Stop'][0]==original['hooks']['Stop'][0]
    spool=tmp_path/'astrorder-observer/events'; spool.mkdir()
    row={'id':'a'*64,'session_id':SID,'event':'Stop','turn_id':'one','observed_at':time.time(),'prompt':'must not survive'}
    (spool/('a'*64+'.json')).write_text(json.dumps(row))
    settings=Settings(database_url=f'sqlite:///{tmp_path}/cache.db',attachments_dir=tmp_path/'attachments',auto_connect_local_hermes=False,browser_secret='test-browser',connector_secret='test-connector')
    app=create_app(settings)
    with TestClient(app) as client:
        app.state.store.upsert_agent({'id':'local-codex','kind':'codex','name':'fixture','status':'ready','capabilities':[]})
        native=SimpleNamespace(_home=tmp_path,state='connected',agent_id='local-codex',_request=Mock(side_effect=RuntimeError()))
        observer=app.state.observers
        observer.collect(native); observer.collect(native)
        rows=observer.recent('local-codex')
        assert len(rows)==1 and 'prompt' not in rows[0]
        assert app.state.store.list_sessions()==[]
        assert read_spool(tmp_path)['items']==[]
        assert client.get('/api/v1/agents/local-codex/observations').status_code==401
        response=client.get('/api/v1/agents/local-codex/observations',headers={'Authorization':'Bearer test-browser'})
        assert response.status_code==200 and len(response.json()['items'])==1

def test_observed_permission_is_visible_and_decidable(tmp_path):
    settings=Settings(database_url=f'sqlite:///{tmp_path}/cache.db',attachments_dir=tmp_path/'attachments',auto_connect_local_hermes=False,browser_secret='test-browser',connector_secret='test-connector')
    app=create_app(settings); headers={'Authorization':'Bearer test-browser'}
    with TestClient(app) as client:
        app.state.store.upsert_agent({'id':'local-codex','kind':'codex','name':'fixture','status':'ready','capabilities':['chat']})
        app.state.store.upsert_session({'id':SID,'agent_id':'local-codex','title':'fixture','status':'idle','workspace':None,'updated_at':'2026-09-12T00:00:00Z'})
        spool=tmp_path/'astrorder-observer/events'; spool.mkdir(parents=True)
        approval_id='b'*32
        (spool/(approval_id+'.json')).write_text(json.dumps({'id':approval_id,'session_id':SID,'event':'PermissionRequest','turn_id':'turn','tool_name':'Bash','detail':'echo hello','approval_pending':True,'observed_at':time.time()}))
        native=SimpleNamespace(_home=tmp_path,state='connected',agent_id='local-codex',_request=Mock())
        app.state.observers.collect(native)
        bootstrap=client.get('/api/v1/bootstrap',headers=headers).json()
        assert bootstrap['approvals'][0]['detail']=='echo hello'
        assert 'approvals' in next(agent for agent in bootstrap['agents'] if agent['id']=='local-codex')['capabilities']
        response=client.post('/api/v1/commands',headers=headers,json={'id':'approve-observed','agent_id':'local-codex','session_id':SID,'action':'approve','text':'','attachment_ids':[],'target_id':approval_id})
        assert response.status_code==200 and response.json()['state']=='completed'
        decision=json.loads((tmp_path/'astrorder-observer/decisions'/(approval_id+'.json')).read_text())
        assert decision=={'id':approval_id,'decision':'allow'}

def test_local_trust_status_rechecks_native_config_when_catalog_is_stale(tmp_path, monkeypatch):
    (tmp_path/'config.toml').write_text('[plugins.fixture]\nenabled = true\n')
    hook={'pluginId':'astrorder@astrorder-local','trustStatus':'untrusted','enabled':True}
    native=SimpleNamespace(_home=tmp_path,state='connected',agent_id='local-codex',_request=Mock(return_value={'data':[{'hooks':[hook]}]}),_executable=lambda:'codex')
    verify=Mock(return_value={'trusted':True,'needs_review':False})
    monkeypatch.setattr('astrorder.observer_plugin.verify_plugin',verify)
    observer=NativeObservers(SimpleNamespace(state=SimpleNamespace(environments=SimpleNamespace(codex=native,remote={}))))
    assert observer.status('local-codex')['trusted'] is True
    assert observer.status('local-codex')['needs_review'] is False
    verify.assert_called_once_with(tmp_path,'codex')

def test_remote_observer_poll_is_throttled_without_delaying_local_observer():
    local=SimpleNamespace(agent_id='local')
    remote=SimpleNamespace(agent_id='remote',remote_json=Mock())
    observer=NativeObservers(SimpleNamespace(state=SimpleNamespace(environments=SimpleNamespace(codex=local,remote={}))))
    assert observer._poll_due(local,100) is True
    assert observer._poll_due(remote,100) is True
    assert observer._poll_due(remote,159) is False
    assert observer._poll_due(remote,160) is True

def test_stale_permission_request_is_discarded_and_acknowledged(tmp_path):
    settings = Settings(
        database_url=f'sqlite:///{tmp_path}/cache.db',
        attachments_dir=tmp_path/'attachments',
        auto_connect_local_hermes=False,
        browser_secret='test-browser',
        connector_secret='test-connector',
    )
    app = create_app(settings)
    headers = {'Authorization': 'Bearer test-browser'}
    with TestClient(app) as client:
        app.state.store.upsert_agent({'id':'local-codex','kind':'codex','name':'fixture','status':'ready','capabilities':['chat']})
        app.state.store.upsert_session({'id':SID,'agent_id':'local-codex','title':'fixture','status':'idle','workspace':None,'updated_at':'2026-09-12T00:00:00Z'})
        spool = tmp_path/'astrorder-observer/events'
        spool.mkdir(parents=True)
        approval_id = 'c'*32
        # Event is older than 600s (e.g. 700s ago)
        (spool/(approval_id+'.json')).write_text(json.dumps({
            'id': approval_id,
            'session_id': SID,
            'event': 'PermissionRequest',
            'turn_id': 'turn',
            'tool_name': 'Bash',
            'detail': 'stale echo',
            'approval_pending': True,
            'observed_at': time.time() - 700,
        }))
        native = SimpleNamespace(_home=tmp_path, state='connected', agent_id='local-codex', _request=Mock())
        app.state.observers.collect(native)
        bootstrap = client.get('/api/v1/bootstrap', headers=headers).json()
        assert len(bootstrap['approvals']) == 0
        assert approval_id in app.state.observers.ack.get('local-codex', [])

def test_full_access_permission_is_auto_allowed_without_pending_approval(tmp_path):
    settings=Settings(database_url=f'sqlite:///{tmp_path}/cache.db',attachments_dir=tmp_path/'attachments',auto_connect_local_hermes=False,browser_secret='test-browser',connector_secret='test-connector')
    app=create_app(settings); headers={'Authorization':'Bearer test-browser'}
    with TestClient(app) as client:
        app.state.store.upsert_agent({'id':'local-codex','kind':'codex','name':'fixture','status':'ready','capabilities':['chat']})
        app.state.store.upsert_session({'id':SID,'agent_id':'local-codex','title':'fixture','status':'idle','workspace':None,'updated_at':'2026-09-12T00:00:00Z'})
        app.state.store.set_session_approval_mode_binding('local-codex',SID,'full_access')
        spool=tmp_path/'astrorder-observer/events'; spool.mkdir(parents=True)
        approval_id='d'*32
        (spool/(approval_id+'.json')).write_text(json.dumps({'id':approval_id,'session_id':SID,'event':'PermissionRequest','turn_id':'turn','tool_call_id':'call-1','tool_name':'Bash','detail':'whoami','approval_pending':True,'observed_at':time.time()}))
        native=SimpleNamespace(_home=tmp_path,state='connected',agent_id='local-codex',_request=Mock())
        app.state.observers.collect(native)
        assert client.get('/api/v1/bootstrap',headers=headers).json()['approvals']==[]
        assert json.loads((tmp_path/'astrorder-observer/decisions'/(approval_id+'.json')).read_text())=={'id':approval_id,'decision':'allow'}

def test_completed_tool_resolves_observer_approval(tmp_path):
    settings=Settings(database_url=f'sqlite:///{tmp_path}/cache.db',attachments_dir=tmp_path/'attachments',auto_connect_local_hermes=False,browser_secret='test-browser',connector_secret='test-connector')
    app=create_app(settings); headers={'Authorization':'Bearer test-browser'}
    with TestClient(app) as client:
        app.state.store.upsert_agent({'id':'local-codex','kind':'codex','name':'fixture','status':'ready','capabilities':['chat']})
        app.state.store.upsert_session({'id':SID,'agent_id':'local-codex','title':'fixture','status':'idle','workspace':None,'updated_at':'2026-09-12T00:00:00Z'})
        spool=tmp_path/'astrorder-observer/events'; spool.mkdir(parents=True)
        approval_id='e'*32
        permission={'id':approval_id,'session_id':SID,'event':'PermissionRequest','turn_id':'turn','tool_call_id':'call-1','tool_name':'Bash','detail':'whoami','approval_pending':True,'observed_at':time.time()}
        (spool/(approval_id+'.json')).write_text(json.dumps(permission))
        native=SimpleNamespace(_home=tmp_path,state='connected',agent_id='local-codex',_request=Mock())
        app.state.observers.collect(native)
        assert len(client.get('/api/v1/bootstrap',headers=headers).json()['approvals'])==1
        completed={**permission,'id':'f'*32,'event':'PostToolUse','approval_pending':False,'observed_at':time.time()}
        (spool/('f'*32+'.json')).write_text(json.dumps(completed))
        app.state.observers.collect(native)
        assert client.get('/api/v1/bootstrap',headers=headers).json()['approvals']==[]
