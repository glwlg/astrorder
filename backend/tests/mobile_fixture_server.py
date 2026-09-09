"""Isolated protocol fixture only: no Hermes/Codex, credentials or existing DB access."""
import asyncio
import os
from contextlib import asynccontextmanager
from datetime import UTC, datetime
from pathlib import Path
from uuid import uuid4
from types import SimpleNamespace

import uvicorn
from astrorder.config import Settings
from astrorder.main import create_app

root = Path(__file__).resolve().parents[2]
settings = Settings(
    port=30013, database_url=os.environ['ASTRORDER_E2E_DATABASE'],
    attachments_dir=Path(os.environ['ASTRORDER_E2E_ATTACHMENTS']),
    browser_secret=os.environ['ASTRORDER_E2E_TOKEN'], connector_secret=uuid4().hex,
    allowed_origins=('http://127.0.0.1:30013',), static_dir=root / 'frontend' / 'dist',
    auto_connect_local_hermes=False,
)
app = create_app(settings)
base_lifespan = app.router.lifespan_context
agent_id = 'mobile-protocol-fixture'


def now():
    return datetime.now(UTC).isoformat()


@asynccontextmanager
async def fixture_lifespan(app):
    async with base_lifespan(app):
        service = app.state.service
        store = app.state.store
        def create_fixture_session(*, agent_id, workspace, title):
            assert agent_id == 'mobile-protocol-fixture'
            row = {'id': 'fixture-created-' + uuid4().hex, 'agent_id': agent_id, 'title': title or '新会话', 'workspace': workspace, 'status': 'idle', 'updated_at': now()}
            service.record_native_sessions([row])
            return store.get_session(agent_id, row['id'])
        app.state.connections.create_session_for_agent = create_fixture_session
        # Inert native-shaped model/open-state fixture; no real runtime is contacted.
        model_bindings = {'fixture-main': 'model-main', 'fixture-other': 'model-other'}
        def open_rpc(method, params, timeout=3):
            if method == 'session.active_list':
                return {'result': {'sessions': [{'id': 'private-' + sid, 'session_key': sid, 'status': 'idle'} for sid in ('fixture-main', 'fixture-other')]}}
            sid = str(params.get('session_id', '')).removeprefix('private-')
            if method == 'session.resume':
                assert params['omit_messages'] is True and params['defer_history'] is True
                return {'result': {'session_id': 'private-' + sid, 'info': {'model': model_bindings.get(sid, 'model-main'), 'provider': 'fixture-provider', 'branch': 'fixture/native-branch'}}}
            if method == 'model.options':
                return {'result': {'providers': [{'slug': 'fixture-provider', 'name': 'Fixture', 'models': ['model-main', 'model-other', 'model-next']}]}}
            if method == 'config.set':
                assert params['key'] == 'model'
                model_bindings[sid] = params['value'].split(' --provider ', 1)[0]
                return {'result': {'value': model_bindings[sid], 'scope': 'session', 'confirm_required': False}}
            if method == 'session.status':
                return {'result': {'output': f'Model: {model_bindings[sid]} (fixture-provider)'}}
            return {'error': {'code': -32601}}
        app.state.connections.get_runtime_by_agent_id = lambda aid: SimpleNamespace(rpc=open_rpc) if aid == agent_id else None
        agent = {'id': agent_id, 'kind': 'hermes', 'name': '隔离协议测试（非原生 Agent）', 'status': 'ready', 'capabilities': ['chat', 'stop', 'attachments', 'events'], 'limitation': 'Inert fixture; no native model or agent execution'}
        running = {}
        jobs = set()

        def publish(kind, sid, data):
            service.accept_connector_event(agent_id, {'id': uuid4().hex, 'type': kind, 'agent_id': agent_id, 'session_id': sid, 'data': data})

        def state(sid, status):
            publish('session.upsert', sid, {**store.get_session(agent_id, sid), 'status': status, 'updated_at': now()})

        def receipt(command, status):
            publish('command.upsert', command['session_id'], {**command, 'state': status, 'error': 'Fixture rejection' if status == 'failed' else None})

        async def finish(command):
            sid = command['session_id']
            if command['action'] == 'stop':
                if sid in running:
                    running[sid].set()
                receipt(command, 'completed')
                return
            await asyncio.sleep(0.15)  # Intentional delayed acknowledgment test condition.
            if command['text'] == 'REJECT':
                receipt(command, 'failed')
                return
            if command['text'] == 'UNKNOWN':
                receipt(command, 'unknown')
                return
            if sid in running:
                receipt(command, 'failed')
                return
            gate = running[sid] = asyncio.Event()
            receipt(command, 'accepted')
            state(sid, 'running')
            publish('message.upsert', sid, {'id': f"user-{command['id']}", 'agent_id': agent_id, 'session_id': sid, 'role': 'user', 'kind': 'message', 'text': command['text'], 'attachments': command['attachments'], 'created_at': now(), 'command_id': command['id'], 'tool': None})
            if command['text'] == 'HOLD':
                await gate.wait()
            else:
                await asyncio.sleep(0.7)
            publish('message.upsert', sid, {'id': f"reply-{command['id']}", 'agent_id': agent_id, 'session_id': sid, 'role': 'assistant', 'kind': 'message', 'text': '协议测试回显：' + (command['text'] or '附件'), 'attachments': [], 'created_at': now(), 'command_id': command['id'], 'tool': None})
            receipt(command, 'completed')
            running.pop(sid, None)
            state(sid, 'idle')

        class InertConnector:
            async def send_json(self, frame):
                if frame.get('type') == 'command':
                    task = asyncio.create_task(finish(frame['command']))
                    jobs.add(task)
                    task.add_done_callback(jobs.discard)
            async def close(self, **kwargs):
                pass

        await service.register_connector(InertConnector(), agent)
        for sid in ('fixture-main', 'fixture-other', 'fixture-fail', 'fixture-unknown', 'fixture-pages'):
            publish('session.upsert', sid, {'id': sid, 'agent_id': agent_id, 'title': sid, 'workspace': None, 'status': 'idle', 'updated_at': now(), 'history_state': 'local', 'control_state': 'owned'})
        for i in range(62):
            kind = 'thinking' if i == 58 else 'tool' if i == 59 else 'message'
            publish('message.upsert', 'fixture-pages', {'id': f'page-{i:03d}', 'agent_id': agent_id, 'session_id': 'fixture-pages', 'role': 'tool' if kind == 'tool' else 'user' if i % 2 == 0 else 'assistant', 'kind': kind, 'text': f'分页记录 {i:03d}\n\n这是隔离协议测试中的消息，用于核对按需分页与阅读位置。', 'attachments': [], 'created_at': datetime.fromtimestamp(i + 1, UTC).isoformat(), 'command_id': None, 'tool': {'name': 'fixture-tool', 'arguments': {'test': True}} if kind == 'tool' else None})
        try:
            yield
        finally:
            for task in jobs:
                task.cancel()
            await asyncio.gather(*jobs, return_exceptions=True)


app.router.lifespan_context = fixture_lifespan
if __name__ == '__main__':
    uvicorn.run(app, host='127.0.0.1', port=30013, log_level='warning', access_log=False)
