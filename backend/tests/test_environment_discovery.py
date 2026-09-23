from types import SimpleNamespace
from astrorder.config import Settings
from astrorder.core.environment_connections import EnvironmentConnections, RemoteCodex
from astrorder.core.events import EventHub
from astrorder.models import AgentConnectionChoice
from astrorder.service import ControlService
from astrorder.store import Store
from test_codex_connection import FakeClient


def test_discovery_does_not_connect_and_disconnect_choice_survives_restore(tmp_path, monkeypatch):
    settings = Settings(database_url=f'sqlite:///{tmp_path}/cache.db', auto_connect_local_hermes=False)
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    row = {'id': 'ssh-fixture', 'display_name': 'fixture', 'settings': {'host': 'host', 'port': 22}}
    monkeypatch.setattr(store, 'list_ssh_connections', lambda: [row])
    monkeypatch.setattr(store, 'get_ssh_connection', lambda cid: row if cid == row['id'] else None)
    local = {'available': False, 'state': 'offline', 'detail': ''}
    hermes = SimpleNamespace(snapshot=lambda service: {'local': local, 'ssh': {'items': []}})
    codex = SimpleNamespace(snapshot=lambda: {**local, 'agent_id': 'local-codex'}, agent_id='local-codex')
    registry = EnvironmentConnections(settings, store, service, hermes, codex)
    monkeypatch.setattr(RemoteCodex, 'remote_json', lambda self, source: {'os': 'Linux', 'items': [{'kind': kind, 'available': True, 'executable': '/usr/bin/' + kind} for kind in ['hermes', 'codex']]})
    monkeypatch.setattr(RemoteCodex, '_client', lambda self, *args, **kwargs: FakeClient(*args, **kwargs))
    try:
        result = registry.discover(row['id'])
        assert result['discovered']
        assert [a['kind'] for a in result['agents']] == ['hermes', 'codex']
        assert store.list_agents() == []
        registry.change(row['id'], 'codex', True)
        assert registry.remote[row['id']].state == 'connected'
        registry.change(row['id'], 'codex', False)
        with store.session() as db:
            assert db.get(AgentConnectionChoice, row['id'] + ':codex').enabled == 0
        registry.restore()
        assert registry.remote[row['id']].state == 'disconnected'
    finally:
        registry.shutdown()
        store.close()
