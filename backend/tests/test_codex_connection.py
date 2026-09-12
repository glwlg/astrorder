import sys
from types import SimpleNamespace

import pytest

from astrorder_codex_connector.app_server import CodexRpcRejected
from astrorder.config import Settings
from astrorder.connections import ConnectionError
from astrorder.events import EventHub
from astrorder.native_codex import CodexConnection
from astrorder.service import ControlService
from astrorder.store import Store

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
    def send(self, frame): self.calls.append((frame.get('method', 'response'), frame))
    def request(self, method, params, timeout=30):
        self.calls.append((method, params))
        if method == 'initialize': return {'userAgent': 'fixture'}
        if method == 'account/read': return {'requiresOpenaiAuth': False, 'account': None}
        if method == 'thread/list': return {'data': [THREAD], 'nextCursor': None}
        if method == 'thread/read':
            assert params['includeTurns'] is False
            return {'thread': THREAD}
        if method == 'thread/items/list':
            assert params['sortDirection'] == 'desc'
            assert isinstance(params['limit'], int) and 1 <= params['limit'] <= 200
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
async def test_codex_can_explicitly_delegate_commands_to_a_daemon_controller(tmp_path):
    settings = Settings(
        database_url=f'sqlite:///{tmp_path}/daemon-controller.db',
        auto_connect_local_hermes=False,
        codex_executable=sys.executable,
    )
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    connection = CodexConnection(settings, store, service, client_factory=FakeClient)

    class Controller:
        activated = False
        closed = False
        calls: list[dict[str, object]] = []
        model_calls: list[tuple[str, str, str]] = []
        effort_calls: list[tuple[str, str]] = []
        create_calls: list[tuple[str | None, str | None, bool, str | None]] = []

        def activate(self):
            self.activated = True

        def close(self):
            self.closed = True

        async def submit(self, command):
            self.calls.append(dict(command))
            return 'accepted', None

        def set_model(self, session_id, provider, model):
            self.model_calls.append((session_id, provider, model))
            return {'model': model, 'provider': provider}

        def set_effort(self, session_id, effort):
            self.effort_calls.append((session_id, effort))
            return {'effort': effort}

        def create(self, workspace, title, *, ephemeral=False, parent_session_id=None):
            self.create_calls.append((workspace, title, ephemeral, parent_session_id))
            return {
                'id': 'daemon-created-thread',
                'agent_id': 'local-codex',
                'workspace': workspace,
                'title': title,
                'status': 'idle',
            }

    controller = Controller()
    connection.set_daemon_controller_factory(lambda current: controller if current is connection else None)
    try:
        assert connection.connect()['daemon_mode'] is True
        assert controller.activated is True
        command = await service.submit_browser_command(
            {
                'id': 'daemon-command',
                'agent_id': connection.agent_id,
                'session_id': SID,
                'action': 'send',
                'text': 'daemon-owned command',
                'attachment_ids': [],
                'target_id': None,
            }
        )
        assert command['state'] == 'accepted'
        assert controller.calls[0]['id'] == 'daemon-command'
        assert connection.set_model(SID, 'fixture-provider', 'fixture-model') == {
            'model': 'fixture-model', 'provider': 'fixture-provider'
        }
        assert connection.set_effort(SID, 'high') == {'effort': 'high'}
        assert controller.model_calls == [(SID, 'fixture-provider', 'fixture-model')]
        assert controller.effort_calls == [(SID, 'high')]
        assert connection.create('C:/daemon-workspace', 'Daemon-created', ephemeral=True) == {
            'id': 'daemon-created-thread',
            'agent_id': 'local-codex',
            'workspace': 'C:/daemon-workspace',
            'title': 'Daemon-created',
            'status': 'idle',
        }
        assert controller.create_calls == [('C:/daemon-workspace', 'Daemon-created', True, None)]
        connection.disconnect()
        assert controller.closed is True
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


@pytest.mark.asyncio
async def test_codex_active_turn_steers_when_new_command_submitted(tmp_path):
    steer_calls = []

    class SteerClient(FakeClient):
        def request(self, method, params, timeout=30):
            if method == 'turn/start':
                self.notify({'method': 'turn/started', 'params': {'threadId': SID, 'turn': {'id': 'turn-active', 'status': 'inProgress'}}})
                return {'turn': {'id': 'turn-active', 'status': 'inProgress'}}
            if method == 'turn/steer':
                steer_calls.append(dict(params))
                return {}
            return super().request(method, params, timeout)

    settings = Settings(database_url=f'sqlite:///{tmp_path}/steer.db', auto_connect_local_hermes=False, codex_executable=sys.executable)
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    connection = CodexConnection(settings, store, service, client_factory=SteerClient)
    try:
        connection.connect()
        # 发送第一个任务，进入 running 态
        cmd1 = await service.submit_browser_command({
            'id': 'cmd-1',
            'agent_id': connection.agent_id,
            'session_id': SID,
            'action': 'send',
            'text': '初始长任务',
            'attachment_ids': [],
            'target_id': None,
        })
        assert cmd1['state'] == 'running'
        assert SID in connection._active
        assert connection._active[SID] == 'turn-active'

        # 此时任务正在执行，用户在手机端追加补充/纠偏消息
        cmd2 = await service.submit_browser_command({
            'id': 'cmd-2',
            'agent_id': connection.agent_id,
            'session_id': SID,
            'action': 'send',
            'text': '补充要求：不需要生成图表',
            'attachment_ids': [],
            'target_id': None,
        })
        # 验证 turn/steer 被成功调用，命令被即刻 accepted（而非 queued 死等）
        assert cmd2['state'] == 'accepted'
        assert len(steer_calls) == 1
        assert steer_calls[0]['threadId'] == SID
        assert steer_calls[0]['expectedTurnId'] == 'turn-active'
        assert steer_calls[0]['input'] == [{'type': 'text', 'text': '补充要求：不需要生成图表'}]
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


@pytest.mark.asyncio
async def test_failed_turn_preserves_native_failure_reason_for_the_browser(tmp_path):
    class FailingClient(FakeClient):
        def request(self, method, params, timeout=30):
            if method != 'turn/start':
                return super().request(method, params, timeout)
            turn_id = 'failed-turn'
            self.notify({'method': 'turn/started', 'params': {'threadId': SID, 'turn': {'id': turn_id, 'status': 'inProgress'}}})
            self.notify({
                'method': 'turn/completed',
                'params': {
                    'threadId': SID,
                    'turn': {
                        'id': turn_id,
                        'status': 'failed',
                        'error': {'message': 'Missing environment variable: OPENCODEX_API_AUTH_TOKEN.'},
                    },
                },
            })
            return {'turn': {'id': turn_id, 'status': 'inProgress'}}

    settings = Settings(database_url=f'sqlite:///{tmp_path}/failed-turn.db', auto_connect_local_hermes=False, codex_executable=sys.executable)
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    connection = CodexConnection(settings, store, service, client_factory=FailingClient)
    command = {'id': 'failed-command', 'agent_id': connection.agent_id, 'session_id': SID, 'action': 'send', 'text': 'hello', 'attachment_ids': [], 'target_id': None}
    try:
        connection.connect()
        result = await service.submit_browser_command(command)
        assert result['state'] == 'failed'
        assert result['error'] == 'Missing environment variable: OPENCODEX_API_AUTH_TOKEN.'
        assert store.get_command(connection.agent_id, SID, command['id'])['error'] == 'Missing environment variable: OPENCODEX_API_AUTH_TOKEN.'
    finally:
        connection.disconnect()
        store.close()


def test_codex_launches_child_with_all_system_environment(tmp_path, monkeypatch):
    from astrorder import native_codex

    class EnvironmentRecordingClient(FakeClient):
        environment = None

        def __init__(self, config, on_notification, **kwargs):
            super().__init__(config, on_notification, **kwargs)
            type(self).environment = kwargs.get('environment')

    monkeypatch.setattr(
        native_codex,
        'load_system_environment',
        lambda: {'MACHINE_ONLY': 'machine', 'USER_ONLY': 'user', 'PROCESS_ONLY': 'process'},
    )
    settings = Settings(database_url=f'sqlite:///{tmp_path}/environment.db', auto_connect_local_hermes=False, codex_executable=sys.executable)
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    connection = CodexConnection(settings, store, service, client_factory=EnvironmentRecordingClient)
    try:
        connection.connect()
        assert EnvironmentRecordingClient.environment == {
            'MACHINE_ONLY': 'machine',
            'USER_ONLY': 'user',
            'PROCESS_ONLY': 'process',
        }
    finally:
        connection.disconnect()
        store.close()


def test_request_surfaces_codex_transport_stderr(tmp_path):
    class ClosedClient:
        def request(self, method, params, timeout=30):
            raise RuntimeError('Codex transport closed before response: remote bootstrap failed')

    settings = Settings(database_url=f'sqlite:///{tmp_path}/transport-error.db', auto_connect_local_hermes=False)
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    connection = CodexConnection(settings, store, service)
    connection.client = ClosedClient()
    try:
        with pytest.raises(ConnectionError) as raised:
            connection._request('initialize', {})
        assert 'remote bootstrap failed' in raised.value.detail
    finally:
        store.close()


def test_messages_falls_back_to_thread_read_and_store_when_items_list_not_supported(tmp_path):
    class UnsupportedItemsClient(FakeClient):
        def request(self, method, params, timeout=30):
            if method == 'thread/items/list':
                raise ConnectionError('Codex rejected request (-32601; thread/items/list is not supported yet)', 422)
            if method == 'thread/read':
                return {
                    'thread': {
                        **THREAD,
                        'turns': [{
                            'id': 'turn-1',
                            'items': [
                                {'id': 'item-user-1', 'type': 'userMessage', 'content': [{'type': 'text', 'text': '画个图'}]},
                                {'id': 'item-agent-1', 'type': 'agentMessage', 'text': '正在处理'},
                            ],
                        }],
                    },
                }
            return super().request(method, params, timeout)

    settings = Settings(database_url=f'sqlite:///{tmp_path}/fallback.db', auto_connect_local_hermes=False, codex_executable=sys.executable)
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    connection = CodexConnection(settings, store, service, client_factory=UnsupportedItemsClient)
    try:
        connection.connect()
        page = connection.messages(SID, None, 2)
        assert len(page['items']) == 2
        assert [m['id'] for m in page['items']] == ['item-user-1', 'item-agent-1']
        assert page['items'][0]['text'] == '画个图'
        assert page['items'][1]['text'] == '正在处理'
    finally:
        connection.disconnect()
        store.close()


def _codex_connection(tmp_path, client_factory, name='effort.db'):
    settings = Settings(database_url=f'sqlite:///{tmp_path}/{name}', auto_connect_local_hermes=False, codex_executable=sys.executable)
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    connection = CodexConnection(settings, store, service, client_factory=client_factory)
    return connection, store


def test_codex_effort_change_is_confirmed_by_settings_notification_not_thread_read(tmp_path):
    class EffortClient(FakeClient):
        def request(self, method, params, timeout=30):
            if method == 'thread/settings/update':
                assert params == {'threadId': SID, 'effort': 'high'}
                self.notify({
                    'method': 'thread/settings/updated',
                    'params': {
                        'threadId': params['threadId'],
                        'threadSettings': {
                            'model': self.model,
                            'modelProvider': 'native-provider',
                            'effort': params['effort'],
                        },
                    },
                })
                return {}
            if method == 'thread/read':
                raise AssertionError('thread/read does not carry Codex effort')
            return super().request(method, params, timeout)

    connection, store = _codex_connection(tmp_path, EffortClient)
    try:
        connection.connect()
        assert connection.set_effort(SID, 'high') == {'effort': 'high'}
        assert connection.current_effort(SID) == 'high'
    finally:
        connection.disconnect()
        store.close()


def test_codex_effort_never_updates_after_resume_reports_thread_not_found(tmp_path):
    class MissingThreadClient(FakeClient):
        settings_update_calls = 0

        def request(self, method, params, timeout=30):
            if method == 'thread/resume':
                raise CodexRpcRejected({'code': -32600, 'message': f'thread not found: {params["threadId"]}'})
            if method == 'thread/settings/update':
                type(self).settings_update_calls += 1
                raise CodexRpcRejected({'code': -32600, 'message': f'thread not found: {params["threadId"]}'})
            return super().request(method, params, timeout)

    connection, store = _codex_connection(tmp_path, MissingThreadClient, name='missing-thread-effort.db')
    try:
        connection.connect()
        with pytest.raises(ConnectionError, match='thread not found'):
            connection.set_effort(SID, 'high')
        # A failed resume must stop the control flow. Sending settings/update to
        # that same absent thread is the regression shown in the UI toast.
        assert MissingThreadClient.settings_update_calls == 0
    finally:
        connection.disconnect()
        store.close()


def test_codex_effort_change_rejects_when_native_notification_omits_effort(tmp_path):
    class SilentEffortClient(FakeClient):
        def request(self, method, params, timeout=30):
            if method == 'thread/settings/update':
                self.notify({
                    'method': 'thread/settings/updated',
                    'params': {
                        'threadId': params['threadId'],
                        'threadSettings': {'model': self.model, 'modelProvider': 'native-provider'},
                    },
                })
                return {}
            return super().request(method, params, timeout)

    connection, store = _codex_connection(tmp_path, SilentEffortClient, name='silent-effort.db')
    try:
        connection.connect()
        with pytest.raises(ConnectionError, match='思考强度尚未读回确认'):
            connection.set_effort(SID, 'high')
    finally:
        connection.disconnect()
        store.close()


def test_current_effort_reads_native_sqlite_without_resume_or_thread_read(tmp_path):
    import sqlite3
    home = tmp_path / 'codex-home'
    home.mkdir()
    database = home / 'state_5.sqlite'
    with sqlite3.connect(database) as db:
        db.execute('CREATE TABLE threads (id TEXT, reasoning_effort TEXT)')
        db.execute('INSERT INTO threads (id, reasoning_effort) VALUES (?, ?)', (SID, 'medium'))
        db.commit()

    class DisplayClient(FakeClient):
        def request(self, method, params, timeout=30):
            if method in {'thread/read', 'thread/resume'}:
                raise AssertionError(f'{method} must not be used to display Codex effort')
            return super().request(method, params, timeout)

    connection, store = _codex_connection(tmp_path, DisplayClient, name='display-effort.db')
    try:
        connection.connect()
        connection._home = home
        assert connection.current_effort(SID) == 'medium'
    finally:
        connection.disconnect()
        store.close()
