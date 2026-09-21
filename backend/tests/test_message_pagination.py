import pytest
from astrorder.config import Settings
from astrorder.store import Store


def test_latest_tail_and_older_pages_share_timestamp_and_identity_order(tmp_path):
    store = Store(Settings(database_url=f'sqlite:///{tmp_path}/pagination.db', auto_connect_local_hermes=False))
    try:
        store.upsert_agent(dict(id='agent', kind='hermes', name='inert', status='ready', capabilities=[], limitation=None))
        for sid in ('native', 'other'):
            store.upsert_session(dict(id=sid, agent_id='agent', title=sid, status='idle', workspace=None, updated_at='2026-09-08T00:00:00Z'))
        for name, second in [('newest', 4), ('oldest', 1), ('tie-a', 2), ('tie-b', 2), ('middle', 3)]:
            store.upsert_message(dict(id=name, session_id='native', agent_id='agent', role='assistant', kind='message', text=name, attachments=[], created_at=f'2026-09-08T00:00:0{second}Z', command_id=None, tool=None))
        latest, cursor = store.list_messages('agent', 'native', None, 2)
        assert [row['id'] for row in latest] == ['middle', 'newest']
        with pytest.raises(ValueError):
            store.list_messages('agent', 'other', cursor, 2)
        older, cursor = store.list_messages('agent', 'native', cursor, 2)
        assert [row['id'] for row in older] == ['tie-a', 'tie-b']
        first, cursor = store.list_messages('agent', 'native', cursor, 2)
        assert [row['id'] for row in first] == ['oldest']
        assert cursor is None
    finally:
        store.close()


def test_user_echoes_with_the_same_command_are_reconciled(tmp_path):
    store = Store(Settings(database_url=f'sqlite:///{tmp_path}/echoes.db', auto_connect_local_hermes=False))
    try:
        base = dict(session_id='native', agent_id='agent', role='user', kind='message', text='hello', attachments=[], command_id='command', tool=None)
        store.upsert_message({**base, 'id': 'native-live', 'created_at': '2026-09-08T00:00:02Z'})
        canonical = store.upsert_message({**base, 'id': 'history-alias', 'created_at': '2026-09-08T00:00:01Z'})
        rows, _ = store.list_messages('agent', 'native', None, 10)
        assert canonical['id'] == 'native-live'
        assert [row['id'] for row in rows] == ['native-live']
    finally:
        store.close()


def test_codex_history_aliases_do_not_duplicate_or_reorder_live_messages(tmp_path):
    store = Store(Settings(database_url=f'sqlite:///{tmp_path}/aliases.db', auto_connect_local_hermes=False))
    try:
        base = dict(session_id='native', agent_id='agent', role='assistant', kind='message', text='done', attachments=[], tool=None)
        store.upsert_message({**base, 'id': 'msg-live', 'created_at': '2026-09-08T00:00:02Z', 'command_id': 'command'})
        canonical = store.upsert_message({**base, 'id': 'item-2', 'created_at': '2026-09-08T00:00:01Z', 'command_id': None})
        repeated = store.upsert_message({**base, 'id': 'msg-live', 'created_at': '2026-09-08T00:00:00Z', 'command_id': None})
        repaired = store.upsert_message({**base, 'id': 'msg-live', 'created_at': '2026-09-08T00:00:03Z', 'command_id': None, 'authoritative_created_at': True})
        rows, _ = store.list_messages('agent', 'native', None, 10)
        assert canonical['id'] == 'msg-live'
        assert [row['id'] for row in rows] == ['msg-live']
        assert repeated['created_at'] == '2026-09-08T00:00:02.000Z'
        assert repaired['created_at'] == '2026-09-08T00:00:03.000Z'
        assert repeated['command_id'] == 'command'
    finally:
        store.close()


def test_placeholder_title_does_not_replace_meaningful_title(tmp_path):
    store = Store(Settings(database_url=f'sqlite:///{tmp_path}/titles.db', auto_connect_local_hermes=False))
    try:
        base = dict(id='session', agent_id='agent', workspace=None, status='idle', updated_at='2026-09-08T00:00:00Z')
        store.upsert_session({**base, 'title': 'meaningful title'})
        row = store.upsert_session({**base, 'title': '\u65b0\u4f1a\u8bdd', 'updated_at': '2026-09-08T00:00:01Z'})
        assert row['title'] == 'meaningful title'
    finally:
        store.close()
