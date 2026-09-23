"""Manual probe: temporary cache and owned app-server; never sends prompts."""
import json
import tempfile
from pathlib import Path
from astrorder.config import Settings
from astrorder.core.events import EventHub
from astrorder.store import Store
from astrorder.service import ControlService
from astrorder.native.codex import CodexConnection

with tempfile.TemporaryDirectory(prefix='astrorder-codex-probe-') as directory:
    settings = Settings(database_url=f'sqlite:///{Path(directory) / "cache.db"}', auto_connect_local_hermes=False)
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    connection = CodexConnection(settings, store, service)
    process = None
    try:
        result = connection.connect()
        print(json.dumps(result, ensure_ascii=False))
        assert result['state'] == 'connected'
        process = connection.client.process
        rows = store.list_sessions(connection.agent_id)
        if rows:
            page = connection.messages(rows[0]['id'], None, 2)
            binding = connection.model(rows[0]['id'])
            assert binding['model']
            print(json.dumps({'native_items_returned': len(page['items']), 'has_more': bool(page['next_cursor']), 'model_read': True, 'scope': 'native read-only; no content printed'}))
    finally:
        connection.disconnect()
        assert connection.client is None
        assert process is None or process.poll() is not None
        store.close()
