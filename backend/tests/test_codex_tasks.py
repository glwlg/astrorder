import sys
from concurrent.futures import ThreadPoolExecutor

import pytest
from test_codex_connection import SID, FakeClient

from astrorder.config import Settings
from astrorder.connections import ConnectionError
from astrorder.events import EventHub
from astrorder.native_codex import CodexConnection
from astrorder.service import ControlService
from astrorder.store import Store


def test_native_runtime_events_update_durable_tasks(tmp_path):
    settings = Settings(database_url=f'sqlite:///{tmp_path}/tasks.db', auto_connect_local_hermes=False)
    store = Store(settings)
    connection = CodexConnection(settings, store, ControlService(store, EventHub(), settings))
    connection._agent('ready')
    connection._record_thread({'id': 'parent', 'preview': 'test'})

    def notify(method, **params):
        connection._notification({'method': method, 'params': {'threadId': 'parent', **params}})

    def tasks():
        return {task['id']: task for task in store.list_tasks(connection.agent_id, 'parent')}

    try:
        # Ordinary terminal/tool messages must not become background tasks.
        command = {'id': 'cmd', 'type': 'commandExecution', 'command': 'pytest', 'status': 'inProgress', 'processId': None}
        notify('item/started', item=command)
        assert tasks() == {}
        command['processId'] = '123'
        notify('item/started', item=command)
        notify('item/commandExecution/outputDelta', itemId='cmd', delta='passed')
        notify('item/completed', item={**command, 'status': 'completed', 'exitCode': 1, 'aggregatedOutput': 'failed'})
        assert tasks() == {}

        notify('turn/plan/updated', turnId='turn', plan=[{'step': 'read', 'status': 'completed'}, {'step': 'fix', 'status': 'inProgress'}])
        assert tasks()['codex:todo:0']['status'] == 'completed'
        assert tasks()['codex:todo:1']['status'] == 'running'
        notify('turn/plan/updated', turnId='turn', plan=[{'step': 'verify', 'status': 'pending'}])
        assert tasks()['codex:todo:0']['title'] == 'verify'
        assert tasks()['codex:todo:1']['status'] == 'cancelled'
        notify('turn/plan/updated', turnId='turn', plan=[])
        assert all(task['status'] == 'cancelled' for task in tasks().values() if task['kind'] == 'todo')

        item = {'id': 'spawn', 'type': 'collabAgentToolCall', 'tool': 'spawnAgent', 'status': 'completed', 'agentsStates': {'child': {'status': 'running', 'message': None}}}
        notify('item/completed', item=item)
        assert tasks()['codex:subagent:child']['status'] == 'running'
        notify('item/completed', item={'id': 'activity', 'type': 'subAgentActivity', 'agentThreadId': 'child', 'agentPath': '/review', 'kind': 'completed'})
        assert tasks()['codex:subagent:child']['status'] == 'completed'
        assert len([task for task in tasks().values() if task['kind'] == 'subagent']) == 1
        # Tasks survive a fresh DB read and are scoped to the parent session.
        assert store.list_tasks(connection.agent_id, 'other') == []
    finally:
        store.close()


def test_task_upsert_is_atomic_across_threads(tmp_path):
    settings = Settings(
        database_url=f'sqlite:///{tmp_path}/task-race.db', auto_connect_local_hermes=False
    )
    store = Store(settings)
    store.upsert_agent(
        {'id': 'agent', 'kind': 'codex', 'name': 'Codex', 'status': 'ready', 'capabilities': []}
    )
    store.upsert_session(
        {
            'id': 'session',
            'agent_id': 'agent',
            'title': 'race',
            'status': 'idle',
            'updated_at': '2026-01-01T00:00:00Z',
        }
    )
    task = {
        'id': 'task',
        'agent_id': 'agent',
        'session_id': 'session',
        'kind': 'background',
        'title': 'race',
        'status': 'running',
        'logs': [],
        'created_at': '2026-01-01T00:00:00Z',
        'updated_at': '2026-01-01T00:00:00Z',
    }
    try:
        with ThreadPoolExecutor(max_workers=8) as pool:
            list(pool.map(store.upsert_task, [task] * 32))
        assert [row['id'] for row in store.list_tasks('agent', 'session')] == ['task']
    finally:
        store.close()


def test_disconnect_and_reconnect_ignore_command_execution_tasks(tmp_path):
    command = {'id': 'cmd', 'type': 'commandExecution', 'command': 'pytest', 'status': 'inProgress', 'processId': '123'}

    class TaskClient(FakeClient):
        def request(self, method, params, timeout=30):
            if method == 'thread/items/list':
                return {'data': [{'item': command}], 'nextCursor': None}
            return super().request(method, params, timeout)

    settings = Settings(database_url=f'sqlite:///{tmp_path}/tasks.db', auto_connect_local_hermes=False, codex_executable=sys.executable)
    store = Store(settings)
    connection = CodexConnection(settings, store, ControlService(store, EventHub(), settings), client_factory=TaskClient)
    try:
        connection.connect()
        connection._notification({'method': 'item/started', 'params': {'threadId': SID, 'item': command}})
        assert store.list_tasks(connection.agent_id, SID) == []
        connection._closed()
        connection.connect()
        assert store.list_tasks(connection.agent_id, SID) == []
        connection._closed()
        command['status'] = 'completed'
        command['aggregatedOutput'] = 'done offline'
        connection.connect()
        assert store.list_tasks(connection.agent_id, SID) == []
        connection.disconnect()
        assert store.list_tasks(connection.agent_id, SID) == []
    finally:
        store.close()


@pytest.mark.parametrize('legacy', [False, True])
def test_history_backfills_tasks_without_overwriting_live_updates(tmp_path, monkeypatch, legacy):
    settings = Settings(database_url=f'sqlite:///{tmp_path}/tasks.db', auto_connect_local_hermes=False)
    store = Store(settings)
    connection = CodexConnection(settings, store, ControlService(store, EventHub(), settings))
    connection._agent('ready')
    connection._record_thread({'id': SID, 'preview': 'test'})
    plan = {'id': 'plan', 'type': 'dynamicToolCall', 'tool': 'update_plan', 'success': True, 'arguments': {'plan': [{'step': 'read', 'status': 'completed'}, {'step': 'fix', 'status': 'in_progress'}]}}
    child = {'id': 'spawn', 'type': 'collabAgentToolCall', 'tool': 'spawnAgent', 'agentsStates': {'child': {'status': 'running', 'message': None}}}
    newest = [plan, {**child, 'id': 'end', 'agentsStates': {'child': {'status': 'completed', 'message': 'done'}}}, child]

    def request(method, params):
        if method == 'thread/items/list':
            if legacy:
                raise ConnectionError('not supported', 422)
            return {'data': [{'item': item} for item in newest], 'nextCursor': None}
        assert method == 'thread/read'
        return {'thread': {'turns': [{'id': 'turn', 'items': list(reversed(newest))}]}}

    monkeypatch.setattr(connection, '_request', request)
    list_tasks = store.list_tasks
    task_reads = 0

    def counted_list_tasks(agent_id, session_id):
        nonlocal task_reads
        task_reads += 1
        return list_tasks(agent_id, session_id)

    monkeypatch.setattr(store, 'list_tasks', counted_list_tasks)
    try:
        connection.messages(SID)
        assert task_reads == 1
        tasks = {task['id']: task for task in store.list_tasks(connection.agent_id, SID)}
        assert tasks['codex:todo:0']['title'] == 'read'
        assert tasks['codex:todo:1']['title'] == 'fix'
        assert tasks['codex:todo:1']['status'] == 'unknown'
        assert tasks['codex:subagent:child']['status'] == 'completed'
        connection._notification({'method': 'turn/plan/updated', 'params': {'threadId': SID, 'plan': [{'step': 'new plan', 'status': 'inProgress'}]}})
        connection.messages(SID)
        tasks = {task['id']: task for task in store.list_tasks(connection.agent_id, SID)}
        assert tasks['codex:todo:0']['title'] == 'new plan'
        assert tasks['codex:todo:0']['status'] == 'running'
        assert tasks['codex:todo:1']['status'] == 'cancelled'
        assert tasks['codex:subagent:child']['status'] == 'completed'
    finally:
        store.close()
