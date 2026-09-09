from pathlib import Path

from test_codex_connection import SID, FakeClient

from astrorder.config import Settings
from astrorder.environment_connections import RemoteCodex
from astrorder.events import EventHub
from astrorder.service import ControlService
from astrorder.store import Store


def test_remote_codex_keeps_connection_native_identity_and_never_reads_local_model(tmp_path):
    settings = Settings(database_url=f'sqlite:///{tmp_path}/cache.db', auto_connect_local_hermes=False)
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    row = {'id': 'ssh-test', 'display_name': 'WSL', 'settings': {'host': 'fixture', 'port': 22}}
    connection = RemoteCodex(settings, store, service, row, '/usr/bin/codex')
    connection.client_factory = FakeClient
    try:
        assert connection.connect()['state'] == 'connected'
        assert store.get_session(connection.agent_id, SID)['connection_id'] == 'ssh-test'
        assert connection.agent_id == 'ssh-codex-ssh-test'
        connection.remote_json = lambda source: {'model': 'remote-model', 'provider': 'remote-provider'}
        connection._home = Path('/remote/home')
        assert connection.model(SID)['model'] == 'remote-model'
        assert not any(method == 'thread/resume' for method, _ in connection.client.calls)
        assert 'StrictHostKeyChecking=yes' in connection.ssh_argv()
        assert 'BatchMode=yes' in connection.ssh_argv()
    finally:
        connection.disconnect()
        store.close()
