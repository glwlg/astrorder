from astrorder.config import Settings
from astrorder.events import EventHub
from astrorder.service import ControlService
from astrorder.store import Store
from astrorder.native_codex import CodexConnection


def test_native_subagent_classification_survives_cache(tmp_path):
    settings = Settings(database_url=f'sqlite:///{tmp_path}/cache.db', auto_connect_local_hermes=False)
    store = Store(settings)
    connection = CodexConnection(settings, store, ControlService(store, EventHub(), settings))
    try:
        connection._agent('ready')
        connection._record_thread({'id': 'native-guardian', 'source': {'subAgent': {'other': 'guardian'}}, 'preview': 'internal', 'cwd': '/work/OpsCore'})
        connection._record_thread({'id': 'native-user', 'source': 'vscode', 'preview': 'internal', 'cwd': '/work/OpsCore'})
        assert store.get_session(connection.agent_id, 'native-guardian')['native_kind'] == 'subagent'
        assert store.get_session(connection.agent_id, 'native-user')['native_kind'] == 'vscode'
        assert len(store.list_sessions(connection.agent_id)) == 2
    finally:
        store.close()
