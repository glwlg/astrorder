import asyncio
from unittest.mock import AsyncMock

from fastapi.testclient import TestClient

from astrorder.config import Settings
from astrorder.core.events import EventHub
from astrorder.main import create_app
from astrorder.service import ControlService
from astrorder.store import Store


def settings(tmp_path):
    return Settings(database_url=f'sqlite:///{tmp_path}/test.db', browser_secret='test-only',
                    connector_secret='connector-test-only', auto_connect_local_hermes=False)


def seed(store):
    agent = dict(id='agent', name='Agent', kind='hermes', status='ready', capabilities=['chat'], limitation=None)
    store.upsert_agent(agent)
    store.upsert_session(dict(id='native', source_session_id='native', agent_id='agent', title='会话',
                             status='idle', updated_at='2026-09-08T00:00:00Z', history_state='loaded'))
    return agent


def test_event_socket_replacement_does_not_remove_controller_read_handler(tmp_path):
    cfg = settings(tmp_path)
    store = Store(cfg)
    agent = seed(store)
    service = ControlService(store, EventHub(), cfg)
    reader = AsyncMock(return_value=[])
    async def scenario():
        await service.register_connector(AsyncMock(), agent)
        service.register_native_history_handler('agent', reader)
        await service.register_connector(AsyncMock(), agent)
        await service.load_native_history('agent', 'native')
    asyncio.run(scenario())
    reader.assert_awaited_once_with('native')
    service.clear_native_history_handler('agent')
    assert 'agent' not in service._native_history_handlers
    store.close()


def test_cached_messages_remain_readable_during_native_reconnect(tmp_path):
    app = create_app(settings(tmp_path))
    with TestClient(app) as client:
        seed(app.state.store)
        app.state.store.upsert_message(dict(id='message', session_id='native', agent_id='agent', role='assistant',
            kind='message', text='已有消息', attachments=[], created_at='2026-09-08T00:00:00Z'))
        response = client.get('/api/v1/sessions/native/messages?agent_id=agent', headers={'Authorization':'Bearer test-only'})
        assert response.status_code == 200
        assert response.json()['items'][0]['text'] == '已有消息'
