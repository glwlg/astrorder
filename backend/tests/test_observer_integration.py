import json
import time
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock

from fastapi.testclient import TestClient

from astrorder.config import Settings
from astrorder.main import create_app
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
