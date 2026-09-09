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
