import json, sys, tempfile
from pathlib import Path
from astrorder.config import Settings
from astrorder.store import Store
from astrorder.events import EventHub
from astrorder.service import ControlService
from astrorder.environment_connections import RemoteCodex, DISCOVERY
row = json.load(sys.stdin)
with tempfile.TemporaryDirectory(prefix='astrorder-remote-codex-') as directory:
    settings = Settings(database_url=f'sqlite:///{Path(directory)/"cache.db"}', auto_connect_local_hermes=False)
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    connection = RemoteCodex(settings, store, service, row, '')
    try:
        discovery = connection.remote_json(DISCOVERY)
        print(json.dumps({'discovered': [(x['kind'], x['available']) for x in discovery['items']], 'os': discovery['os']}), flush=True)
        connection.remote_executable = next(x['executable'] for x in discovery['items'] if x['kind']=='codex' and x['available'])
        state = connection.connect()
        rows = store.list_sessions(connection.agent_id)
        ordinary = [r for r in rows if r.get('native_kind') != 'subagent']
        assert state['state']=='connected' and ordinary
        page = connection.messages(ordinary[0]['id'])
        model = connection.model(ordinary[0]['id'])
        print(json.dumps({'state':state['state'],'native_sessions':len(rows),'ordinary_sessions':len(ordinary),'latest_items':len(page['items']),'model_read':bool(model['model']),'scope':'real SSH read-only; no prompts'}), flush=True)
    finally:
        connection.disconnect()
        store.close()
