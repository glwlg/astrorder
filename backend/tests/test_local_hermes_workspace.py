from __future__ import annotations

import sqlite3
from pathlib import Path

from astrorder.config import Settings
from astrorder.core import session_usage
from astrorder.core.events import EventHub
from astrorder.service import ControlService
from astrorder.store import Store


def test_local_hermes_connector_cannot_replace_native_workspace(tmp_path: Path, monkeypatch) -> None:
    native_db = tmp_path / 'state.db'
    with sqlite3.connect(native_db) as conn:
        conn.execute('CREATE TABLE sessions (id TEXT PRIMARY KEY, cwd TEXT)')
        conn.execute('INSERT INTO sessions VALUES (?, ?)', ('native-session', 'P:/workspace/project'))
    monkeypatch.setattr(session_usage, '_find_local_hermes_state_dbs', lambda _profile: [native_db])

    settings = Settings(database_url=f'sqlite:///{(tmp_path / "app.db").as_posix()}')
    store = Store(settings)
    store.upsert_agent({
        'id': 'local-hermes-default', 'kind': 'hermes', 'name': 'Hermes',
        'status': 'ready', 'capabilities': [], 'limitation': None,
        'profile_name': 'default', 'connection_id': None,
    })
    service = ControlService(store, EventHub(), settings)
    payload = {
        'id': 'native-session', 'agent_id': 'local-hermes-default',
        'title': 'Real session', 'workspace': 'P:/DevApp/Astrorder/server',
        'status': 'running', 'updated_at': '2026-09-28T00:00:00Z',
    }
    event = service.accept_connector_event('local-hermes-default', {
        'id': 'event-wrong-workspace', 'type': 'session.upsert',
        'agent_id': 'local-hermes-default', 'session_id': 'native-session', 'data': payload,
    })
    assert event is not None
    assert event['data']['workspace'] == 'P:/workspace/project'
    assert store.get_session('local-hermes-default', 'native-session')['workspace'] == 'P:/workspace/project'

    # An event for a session not yet in native state must not invent a server workspace.
    payload['id'] = 'not-yet-indexed'
    service.accept_connector_event('local-hermes-default', {
        'id': 'event-pending-native', 'type': 'session.upsert',
        'agent_id': 'local-hermes-default', 'session_id': 'not-yet-indexed', 'data': payload,
    })
    assert store.get_session('local-hermes-default', 'not-yet-indexed')['workspace'] is None
    store.engine.dispose()


def test_local_hermes_connector_hides_internal_recall_in_live_message(tmp_path: Path) -> None:
    settings = Settings(database_url=f'sqlite:///{(tmp_path / "app.db").as_posix()}')
    store = Store(settings)
    agent = 'local-hermes-default'
    store.upsert_agent({'id': agent, 'kind': 'hermes', 'name': 'Hermes', 'status': 'ready', 'capabilities': []})
    store.upsert_session({
        'id': 'native-session', 'agent_id': agent, 'title': 'Test', 'status': 'idle',
        'updated_at': '2026-09-28T00:00:00Z',
    })
    service = ControlService(store, EventHub(), settings)
    note = '[System note: The following is recalled memory context, NOT new user input. Treat as authoritative reference data — reference.]'
    text = f'真实问题\n\n<memory-context>\n{note}\nprivate recall\n</memory-context>'
    event = service.accept_connector_event(agent, {
        'id': 'leaked-recall', 'type': 'message.upsert', 'agent_id': agent,
        'session_id': 'native-session', 'data': {
            'id': 'message-1', 'agent_id': agent, 'session_id': 'native-session',
            'role': 'user', 'kind': 'message', 'text': text,
            'created_at': '2026-09-28T00:00:00Z',
        },
    })
    assert event['data']['text'] == '真实问题'
    assert store.get_message(agent, 'native-session', 'message-1')['text'] == '真实问题'
    store.engine.dispose()
