import sys
import pytest
from types import SimpleNamespace

from astrorder.config import Settings
from astrorder.events import EventHub
from astrorder.service import ControlService
from astrorder.store import Store
from astrorder.native_codex import CodexConnection

SID = '01992890-4444-7777-8888-000000000001'
THREAD = {'id': SID, 'name': 'Native Codex', 'cwd': None, 'createdAt': 1, 'updatedAt': 2, 'status': {'type': 'notLoaded'}, 'modelProvider': 'native-provider', 'gitInfo': {'branch': 'native-branch'}}

class FakeClient:
    def __init__(self, config, on_notification, **kwargs):
        self.calls = []
        self.process = SimpleNamespace(poll=lambda: None)
        self.notify = on_notification
        self.model = 'native-model'
    def start(self): pass
    def stop(self): self.process = None
    def send(self, frame): self.calls.append((frame['method'] if 'method' in frame else 'response', frame))
    def request(self, method, params, timeout=30):
        self.calls.append((method, params))
        if method == 'initialize': return {'userAgent': 'fixture'}
        if method == 'account/read': return {'requiresOpenaiAuth': False, 'account': None}
        if method == 'thread/list': return {'data': [THREAD], 'nextCursor': None}
        if method == 'thread/read':
            assert params['includeTurns'] is False
            return {'thread': THREAD}
        if method == 'thread/items/list':
            assert params['limit'] == 2
            assert params['sortDirection'] == 'desc'
            return {'data': [{'turnId': 'turn', 'item': {'id': 'native-item-2', 'type': 'agentMessage', 'text': 'answer'}}, {'turnId': 'turn', 'item': {'id': 'native-item-1', 'type': 'userMessage', 'content': [{'type': 'text', 'text': 'question'}]}}], 'nextCursor': 'native-opaque-cursor'}
        if method == 'thread/resume':
            assert params['excludeTurns'] is True
            return {'thread': THREAD, 'model': self.model, 'modelProvider': 'native-provider'}
        if method == 'model/list': return {'data': [{'id': 'other', 'model': 'other', 'displayName': 'Other'}], 'nextCursor': None}
        if method == 'thread/settings/update':
            self.model = params['model']
            self.notify({'method': 'thread/settings/updated', 'params': {'threadId': params['threadId'], 'threadSettings': {'model': self.model, 'modelProvider': 'native-provider'}}})
            return {}
        raise AssertionError(method)


def test_connected_means_initialized_catalog_and_scoped_native_handler(tmp_path):
    settings = Settings(database_url=f'sqlite:///{tmp_path}/cache.db', auto_connect_local_hermes=False, codex_executable=sys.executable)
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    connection = CodexConnection(settings, store, service, client_factory=FakeClient)
    try:
        assert connection.snapshot()['state'] == 'disconnected'
        state = connection.connect()
        assert state['state'] == 'connected'
        assert state['session_count'] == 1
        assert [s['id'] for s in store.list_sessions(connection.agent_id)] == [SID]
        assert connection.agent_id in service._native_command_handlers
        assert not any(method in {'thread/start', 'thread/resume', 'turn/start'} for method, _ in connection.client.calls)
        page = connection.messages(SID, None, 2)
        assert [m['id'] for m in page['items']] == ['native-item-1', 'native-item-2']
        assert page['next_cursor']
        assert connection.model(SID)['model'] == 'native-model'
        assert connection.set_model(SID, 'native-provider', 'other')['model'] == 'other'
        assert store.get_session(connection.agent_id, SID)['id'] == SID
        connection.disconnect()
        assert connection.snapshot()['state'] == 'disconnected'
        assert connection.agent_id not in service._native_command_handlers
        assert store.get_agent(connection.agent_id)['status'] == 'disconnected'
    finally:
        connection.disconnect()
        store.close()


@pytest.mark.asyncio
async def test_completion_before_start_reply_is_not_downgraded_and_same_text_stays_distinct(tmp_path):
    class FastClient(FakeClient):
        count = 0
        def request(self, method, params, timeout=30):
            if method != 'turn/start':
                return super().request(method, params, timeout)
            self.count += 1
            tid = f'turn-{self.count}'
            self.notify({'method': 'turn/started', 'params': {'threadId': SID, 'turn': {'id': tid, 'status': 'inProgress'}}})
            for item in [{'id': f'u-{tid}', 'type': 'userMessage', 'content': params['input']}, {'id': f'a-{tid}', 'type': 'agentMessage', 'text': 'reply'}]:
                self.notify({'method': 'item/completed', 'params': {'threadId': SID, 'turnId': tid, 'item': item}})
            self.notify({'method': 'turn/completed', 'params': {'threadId': SID, 'turn': {'id': tid, 'status': 'completed'}}})
            return {'turn': {'id': tid, 'status': 'inProgress'}}
    settings = Settings(database_url=f'sqlite:///{tmp_path}/race.db', auto_connect_local_hermes=False, codex_executable=sys.executable)
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    connection = CodexConnection(settings, store, service, client_factory=FastClient)
    try:
        connection.connect()
        for index in range(2):
            result = await service.submit_browser_command({'id': f'command-{index}', 'agent_id': connection.agent_id, 'session_id': SID, 'action': 'send', 'text': 'same text', 'attachment_ids': [], 'target_id': None})
            assert result['state'] == 'completed'
            assert SID not in connection._active
        assert len(store.list_messages(connection.agent_id, SID, None, 20)[0]) == 4
    finally:
        connection.disconnect()
        store.close()


@pytest.mark.asyncio
async def test_stop_waits_for_completion_and_disconnect_retains_unknown(tmp_path):
    class HoldingClient(FakeClient):
        def request(self, method, params, timeout=30):
            if method == 'turn/start':
                self.notify({'method': 'turn/started', 'params': {'threadId': SID, 'turn': {'id': 'held', 'status': 'inProgress'}}})
                return {'turn': {'id': 'held', 'status': 'inProgress'}}
            if method == 'turn/interrupt':
                assert params == {'threadId': SID, 'turnId': 'held'}
                self.notify({'method': 'turn/completed', 'params': {'threadId': SID, 'turn': {'id': 'held', 'status': 'interrupted'}}})
                return {}
            return super().request(method, params, timeout)
    settings = Settings(database_url=f'sqlite:///{tmp_path}/stop.db', auto_connect_local_hermes=False, codex_executable=sys.executable)
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    connection = CodexConnection(settings, store, service, client_factory=HoldingClient)
    payload = {'agent_id': connection.agent_id, 'session_id': SID, 'action': 'send', 'text': 'hold', 'attachment_ids': [], 'target_id': None}
    try:
        connection.connect()
        assert (await service.submit_browser_command({**payload, 'id': 'start'}))['state'] == 'running'
        stop = await service.submit_browser_command({**payload, 'id': 'stop', 'action': 'stop', 'text': '', 'target_id': SID})
        assert stop['state'] == 'completed'
        assert store.get_command(connection.agent_id, SID, 'start')['state'] == 'cancelled'
        await service.submit_browser_command({**payload, 'id': 'lost'})
        connection.disconnect()
        assert store.get_command(connection.agent_id, SID, 'lost')['state'] == 'unknown'
    finally:
        connection.disconnect()
        store.close()


def test_private_codex_http_connection_and_message_routes(tmp_path):
    from fastapi.testclient import TestClient
    from astrorder.main import create_app
    settings = Settings(database_url=f'sqlite:///{tmp_path}/http.db', auto_connect_local_hermes=False, codex_executable=sys.executable, browser_secret='test-only', attachments_dir=tmp_path / 'attachments')
    with TestClient(create_app(settings)) as client:
        client.app.state.codex.client_factory = FakeClient
        assert client.post('/api/v1/connections/codex/connect').status_code == 401
        headers = {'Authorization': 'Bearer test-only'}
        assert client.post('/api/v1/connections/codex/connect', headers=headers).status_code == 200
        assert client.get('/api/v1/connections/codex', headers=headers).json()['state'] == 'connected'
        response = client.get(f'/api/v1/sessions/{SID}/messages?agent_id=local-codex', headers=headers)
        assert response.status_code == 200
        assert [row['id'] for row in response.json()['items']] == ['native-item-1', 'native-item-2']
        assert client.post('/api/v1/connections/codex/disconnect', headers=headers).status_code == 200
        assert client.get('/api/v1/connections/codex', headers=headers).json()['state'] == 'disconnected'
