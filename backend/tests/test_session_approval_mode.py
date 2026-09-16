from types import SimpleNamespace

import pytest
from fastapi.testclient import TestClient

from astrorder.config import Settings
from astrorder.connections import ConnectionError
from astrorder.events import EventHub
from astrorder.main import create_app
from astrorder.native_codex import CodexConnection
from astrorder.native_controls import current_session_approval_mode, set_session_approval_mode
from astrorder.service import ControlService
from astrorder.store import Store

SID = '01992890-4444-7777-8888-000000000001'
THREAD = {'id': SID, 'name': 'Native Codex', 'cwd': None, 'createdAt': 1, 'updatedAt': 2, 'status': {'type': 'notLoaded'}, 'modelProvider': 'native-provider', 'gitInfo': {'branch': 'native-branch'}}


class RecordingClient:
    def __init__(self, config, on_notification, **kwargs):
        self.calls = []
        self.sent = []
        self.process = SimpleNamespace(poll=lambda: None)
        self.notify = on_notification

    def start(self):
        pass

    def stop(self):
        self.process = None

    def send(self, frame):
        self.sent.append(frame)

    def request(self, method, params, timeout=30):
        self.calls.append((method, params))
        if method == 'initialize':
            return {'userAgent': 'fixture'}
        if method == 'account/read':
            return {'requiresOpenaiAuth': False, 'account': None}
        if method == 'thread/list':
            return {'data': [THREAD], 'nextCursor': None}
        if method == 'thread/read':
            return {'thread': THREAD}
        if method == 'thread/resume':
            return {'thread': THREAD, 'model': 'model', 'modelProvider': 'provider'}
        if method == 'turn/start':
            return {'turn': {'id': 'turn-1', 'status': 'completed'}}
        raise AssertionError(method)


@pytest.mark.asyncio
async def test_codex_approval_mode_defaults_to_auto_and_injects_policies(tmp_path):
    settings = Settings(database_url=f'sqlite:///{tmp_path}/cache.db', auto_connect_local_hermes=False)
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    connection = CodexConnection(settings, store, service, client_factory=RecordingClient)
    try:
        connection.connect()
        # 默认模式是 auto
        assert connection.get_approval_mode(SID) == 'auto'

        # “帮我批准” must be the documented Auto preset: routine work within
        # the workspace runs in a workspace-write sandbox; only boundary
        # crossings are reviewed, first by Codex's auto reviewer.
        command = {'id': 'cmd-1', 'agent_id': connection.agent_id, 'session_id': SID, 'action': 'send', 'text': 'hello', 'attachment_ids': [], 'target_id': None}
        status, _error = await connection.submit(command)
        assert status == 'accepted'
        turn_start_params = next(params for method, params in connection.client.calls if method == 'turn/start')
        assert turn_start_params.get('approvalPolicy') == 'on-request'
        assert turn_start_params.get('sandboxPolicy') == {'type': 'workspaceWrite'}
        assert turn_start_params.get('approvalsReviewer') == 'auto_review'

        # 切换到 manual
        connection.set_approval_mode(SID, 'manual')
        assert connection.get_approval_mode(SID) == 'manual'
        await connection.submit({'id': 'cmd-2', 'agent_id': connection.agent_id, 'session_id': SID, 'action': 'send', 'text': 'test', 'attachment_ids': [], 'target_id': None})
        last_turn_params = connection.client.calls[-1][1]
        assert last_turn_params.get('approvalPolicy') == 'untrusted'

        # 切换到 full_access
        connection.set_approval_mode(SID, 'full_access')
        assert connection.get_approval_mode(SID) == 'full_access'
        await connection.submit({'id': 'cmd-3', 'agent_id': connection.agent_id, 'session_id': SID, 'action': 'send', 'text': 'run', 'attachment_ids': [], 'target_id': None})
        full_turn_params = connection.client.calls[-1][1]
        assert full_turn_params.get('approvalPolicy') == 'never'
        assert full_turn_params.get('sandboxPolicy') == {'type': 'dangerFullAccess'}

        # 在 full_access 下，偶发的 requestApproval 通知直接自动接受
        connection._notification({
            'id': 99,
            'method': 'item/commandExecution/requestApproval',
            'params': {'threadId': SID, 'command': 'ls'},
        })
        assert any(frame.get('id') == 99 and frame.get('result', {}).get('decision') == 'accept' for frame in connection.client.sent)

        # 拒绝非法模式
        with pytest.raises(ConnectionError):
            connection.set_approval_mode(SID, 'invalid_mode')
    finally:
        connection.disconnect()
        store.close()


def test_hermes_session_approval_mode_mapping():
    calls = []

    def rpc(method, params):
        calls.append((method, params))
        if method == 'session.resume':
            return {'result': {'session_id': 'h-1'}}
        if method == 'config.get':
            return {'result': {'value': 'smart'}}
        if method == 'config.set':
            return {'result': {'ok': True}}
        raise AssertionError(method)

    assert current_session_approval_mode(rpc, 's-1') == 'auto'
    assert set_session_approval_mode(rpc, 's-1', 'full_access') == {'mode': 'full_access'}
    assert calls[-1] == ('config.set', {'session_id': 'h-1', 'key': 'approvals.mode', 'value': 'off'})
    assert set_session_approval_mode(rpc, 's-1', 'manual') == {'mode': 'manual'}
    assert calls[-1] == ('config.set', {'session_id': 'h-1', 'key': 'approvals.mode', 'value': 'manual'})


def test_session_approval_mode_api_endpoints(tmp_path):
    settings = Settings(database_url=f'sqlite:///{tmp_path}/http.db', auto_connect_local_hermes=False, browser_secret='test-token')
    app = create_app(settings)
    with TestClient(app) as client:
        app.state.codex.client_factory = RecordingClient
        headers = {'Authorization': 'Bearer test-token'}
        client.post('/api/v1/connections/codex/connect', headers=headers)

        # 默认模式为 auto
        res = client.get(f'/api/v1/sessions/{SID}/approval-mode?agent_id=local-codex', headers=headers)
        assert res.status_code == 200
        assert res.json() == {'mode': 'auto'}

        # 切换到 full_access
        res = client.post(f'/api/v1/sessions/{SID}/approval-mode', json={'agent_id': 'local-codex', 'mode': 'full_access'}, headers=headers)
        assert res.status_code == 200
        assert res.json() == {'mode': 'full_access'}

        # 读回确认
        res = client.get(f'/api/v1/sessions/{SID}/approval-mode?agent_id=local-codex', headers=headers)
        assert res.status_code == 200
        assert res.json() == {'mode': 'full_access'}


def test_session_approval_mode_persists_across_reconnect(tmp_path):
    db_path = f'sqlite:///{tmp_path}/persist.db'
    settings = Settings(database_url=db_path, auto_connect_local_hermes=False, browser_secret='test-token')
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    conn1 = CodexConnection(settings, store, service, client_factory=RecordingClient)
    conn1.connect()
    conn1.set_approval_mode(SID, 'full_access')
    conn1.disconnect()

    # 新建实例连接，验证从 DB 恢复持久化配置
    conn2 = CodexConnection(settings, store, service, client_factory=RecordingClient)
    conn2.connect()
    assert conn2.get_approval_mode(SID) == 'full_access'
    conn2.disconnect()
    store.close()
