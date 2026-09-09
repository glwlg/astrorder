import sqlite3
import sys
from astrorder.config import Settings
from astrorder.events import EventHub
from astrorder.service import ControlService
from astrorder.store import Store
from astrorder.native_codex import CodexConnection
from test_codex_connection import FakeClient, SID


def test_active_native_model_read_is_scoped_read_only_and_never_resumes(tmp_path):
    native = tmp_path / 'state_5.sqlite'
    with sqlite3.connect(native) as db:
        db.execute('CREATE TABLE threads (id TEXT, model TEXT, model_provider TEXT, git_branch TEXT)')
        db.execute('INSERT INTO threads VALUES (?,?,?,?)', (SID, 'persisted-model', 'persisted-provider', 'feature/native'))
    before = native.read_bytes()
    class ActiveClient(FakeClient):
        def request(self, method, params, timeout=30):
            assert method != 'thread/resume', 'Metadata reads must not take over an active native thread'
            if method == 'initialize': return {'codexHome': str(tmp_path), 'userAgent': 'fixture'}
            return super().request(method, params, timeout)
    settings = Settings(database_url=f'sqlite:///{tmp_path}/cache.db', auto_connect_local_hermes=False, codex_executable=sys.executable)
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    connection = CodexConnection(settings, store, service, client_factory=ActiveClient)
    try:
        connection.connect()
        assert connection.model(SID) == {'model': 'persisted-model', 'provider': 'persisted-provider', 'branch': 'feature/native'}
        assert native.read_bytes() == before
    finally:
        connection.disconnect()
        store.close()
