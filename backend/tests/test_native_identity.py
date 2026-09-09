from astrorder.config import Settings
from astrorder.store import Store


def session(identity, **extra):
    return dict(id=identity, agent_id='agent', title='友好问候 #3', status='idle',
                updated_at='2026-09-08T00:00:00Z', source_id='source',
                source_session_id='native-id', project_id='home', project_name='Home', **extra)


def test_native_status_event_keeps_catalog_title_and_project(tmp_path):
    store = Store(Settings(database_url=f'sqlite:///{tmp_path}/test.db'))
    store.upsert_session(session('native-id'))
    event = store.apply_connector_event(event_id='event', event_type='session.upsert',
        agent_id='agent', session_id='native-id', data=dict(id='native-id', agent_id='agent',
        title='native-id', status='running', updated_at='2026-09-08T01:00:00Z'))
    assert event['data']['id'] == 'native-id'
    assert event['data']['title'] == '友好问候 #3'
    assert event['data']['project_id'] == 'home'
    assert len(store.list_sessions()) == 1


def test_migration_preserves_messages_and_merges_duplicate_native_row(tmp_path):
    settings = Settings(database_url=f'sqlite:///{tmp_path}/test.db')
    store = Store(settings)
    store.upsert_session(session('history-old'))
    store.upsert_session({**session('native-id'), 'title': 'native-id'})
    store.upsert_message(dict(id='message', agent_id='agent', session_id='history-old',
        role='user', kind='message', text='keep me', attachments=[],
        created_at='2026-09-08T00:00:00Z'))
    store.engine.dispose()
    reopened = Store(settings)
    rows = reopened.list_sessions()
    assert [row['id'] for row in rows] == ['native-id']
    assert rows[0]['title'] == '友好问候 #3'
    assert reopened.get_message('agent', 'native-id', 'message')['text'] == 'keep me'
    reopened.engine.dispose()
    assert len(Store(settings).list_sessions()) == 1
