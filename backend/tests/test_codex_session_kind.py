from astrorder.config import Settings
from astrorder.core.events import EventHub
from astrorder.native.codex import CodexConnection
from astrorder.service import ControlService
from astrorder.store import Store


def test_native_subagent_classification_survives_cache(tmp_path):
    settings = Settings(database_url=f'sqlite:///{tmp_path}/cache.db', auto_connect_local_hermes=False)
    store = Store(settings)
    connection = CodexConnection(settings, store, ControlService(store, EventHub(), settings))
    try:
        connection._agent('ready')
        connection._record_thread({'id': 'native-guardian', 'source': {'subAgent': {'other': 'guardian'}}, 'name': 'x' * 2000, 'preview': 'internal', 'cwd': '/work/OpsCore'})
        native_user = {'id': 'native-user', 'source': 'vscode', 'preview': 'internal', 'cwd': '/work/OpsCore', 'updatedAt': 1}
        connection._record_thread(native_user)
        cursor = store.latest_cursor()
        connection._record_thread(native_user)
        assert store.latest_cursor() == cursor
        assert store.get_session(connection.agent_id, 'native-guardian')['native_kind'] == 'subagent'
        assert store.get_session(connection.agent_id, 'native-guardian')['title'] == 'x' * 160
        assert store.get_session(connection.agent_id, 'native-user')['native_kind'] == 'vscode'
        assert len(store.list_sessions(connection.agent_id)) == 2
        store.upsert_session(
            {
                'id': 'bounded-title',
                'agent_id': connection.agent_id,
                'title': 'y' * 2000,
                'status': 'idle',
                'updated_at': '2026-01-01T00:00:00Z',
            }
        )
        assert store.get_session(connection.agent_id, 'bounded-title')['title'] == 'y' * 512
    finally:
        store.close()
