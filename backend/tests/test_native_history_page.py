import sqlite3
import pytest
from astrorder.native_history_page import read_native_page


def database(tmp_path):
    path = tmp_path / 'state.db'
    with sqlite3.connect(path) as db:
        db.executescript('CREATE TABLE sessions(id TEXT PRIMARY KEY); CREATE TABLE messages(id INTEGER PRIMARY KEY,session_id TEXT,role TEXT,content TEXT,timestamp REAL,active INTEGER DEFAULT 1,compacted INTEGER DEFAULT 0);')
        db.execute('INSERT INTO sessions VALUES (?)', ('native',))
        db.executemany('INSERT INTO messages(id,session_id,role,content,timestamp) VALUES (?,?,?,?,?)', [(i, 'native', 'assistant' if i % 2 == 0 else 'user', f'message-{i}', i) for i in range(1, 501)])
    return path


def test_native_read_is_bounded_and_pages_back_without_writes(tmp_path):
    path = database(tmp_path)
    before = path.read_bytes()
    page = read_native_page(path, 'native', 'source', None, 2)
    assert [row['id'] for row in page['items']] == [499, 500]
    assert page['next_cursor']
    older = read_native_page(path, 'native', 'source', page['next_cursor'], 20)
    assert [row['id'] for row in older['items']] == list(range(479, 499))
    assert path.read_bytes() == before
    with pytest.raises(ValueError):
        read_native_page(path, 'native', 'other-source', page['next_cursor'], 2)


def test_native_reader_does_not_create_missing_database(tmp_path):
    path = tmp_path / 'absent.db'
    with pytest.raises((OSError, sqlite3.Error)):
        read_native_page(path, 'native', 'source', None, 2)
    assert not path.exists()


def test_native_raw_projection_preserves_reasoning_and_tool_arguments():
    import json
    from astrorder.native_sessions import project_history_messages
    rows = [{'id': 1, 'role': 'assistant', 'content': None, 'timestamp': 1, 'reasoning': 'thought', 'tool_calls': json.dumps([{'id': 'call-1', 'function': {'name': 'terminal', 'arguments': '{"command":"pwd"}'}}])}, {'id': 2, 'role': 'tool', 'tool_name': 'terminal', 'content': 'result', 'timestamp': 2}]
    projected = project_history_messages(rows, durable_session_id='native', native_session_id='native', source_id='source', agent_id='source')
    assert any(row['kind'] == 'thinking' and row['text'] == 'thought' for row in projected)
    assert any(row['kind'] == 'tool' and row['tool']['name'] == 'terminal' and row['tool']['arguments'] == {'command': 'pwd'} for row in projected)
    assert any(row['kind'] == 'tool' and row['text'] == 'result' for row in projected)


def test_api_imports_only_requested_page_without_full_resume_or_live_replay(tmp_path):
    from types import SimpleNamespace
    from unittest.mock import AsyncMock
    from fastapi.testclient import TestClient
    from astrorder.main import create_app
    from astrorder.config import Settings
    path = database(tmp_path)
    app = create_app(Settings(database_url=f'sqlite:///{tmp_path}/cache.db', attachments_dir=tmp_path / 'attachments', browser_secret='test-only', auto_connect_local_hermes=False))
    with TestClient(app) as client:
        store = app.state.store
        store.upsert_agent(dict(id='source', kind='hermes', name='inert', status='ready', capabilities=[], limitation=None))
        store.upsert_session(dict(id='native', agent_id='source', source_id='source', title='native', status='idle', workspace=None, updated_at='2026-01-01T00:00:00Z', history_state='available'))
        app.state.service.load_native_history = AsyncMock(side_effect=AssertionError('Full transcript must not be requested'))
        app.state.connections.get_runtime_by_agent_id = lambda aid: SimpleNamespace(load_native_history_page=lambda sid, before, limit: read_native_page(path, sid, aid, before, limit))
        endpoint = '/api/v1/sessions/native/messages'
        assert client.get(endpoint, params={'agent_id': 'source'}).status_code == 401
        cursor = store.latest_cursor()
        response = client.get(endpoint, params={'agent_id': 'source', 'limit': 2}, headers={'Authorization': 'Bearer test-only'})
        assert response.status_code == 200
        page = response.json()
        assert [row['text'] for row in page['items']] == ['message-499', 'message-500']
        assert len(store.list_messages('source', 'native', None, 200)[0]) == 2
        assert store.latest_cursor() == cursor
        older = client.get(endpoint, params={'agent_id': 'source', 'before': page['next_cursor'], 'limit': 20}, headers={'Authorization': 'Bearer test-only'})
        assert older.status_code == 200
        assert len(older.json()['items']) == 20
        assert older.json()['items'][-1]['text'] == 'message-498'
        assert store.latest_cursor() == cursor
        app.state.service.load_native_history.assert_not_called()
