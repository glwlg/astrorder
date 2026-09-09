"""Owned Codex app-server connection; native threads/items remain authoritative."""
from __future__ import annotations

import asyncio
import base64
import json
import queue
import re
import shutil
import sqlite3
import threading
from datetime import UTC, datetime
from pathlib import Path
from uuid import uuid4

from astrorder_codex_connector.app_server import CodexAppServer, CodexRpcRejected
from astrorder_codex_connector.config import CodexConnectorConfig

from .connections import ConnectionError


def timestamp(value=None):
    return datetime.fromtimestamp(value, UTC).isoformat() if isinstance(value, (float, int)) else datetime.now(UTC).isoformat()


def stored_model(home, sid):
    """Read only native model metadata, never auth/config files or the transcript."""
    files = [(int(match.group(1)), file) for file in home.glob('state_*.sqlite') if (match := re.fullmatch(r'state_(\d+)\.sqlite', file.name))]
    if not files:
        return None
    database = max(files, key=lambda pair: pair[0])[1]
    db = sqlite3.connect(database.resolve().as_uri() + '?mode=ro', uri=True, timeout=3)
    try:
        columns = {row[1] for row in db.execute('PRAGMA table_info(threads)')}
        if not {'id', 'model', 'model_provider', 'git_branch'}.issubset(columns):
            return None
        row = db.execute('SELECT model, model_provider, git_branch FROM threads WHERE id = ?', (sid,)).fetchone()
        if row and isinstance(row[0], str) and row[0]:
            binding = {'model': row[0], 'provider': row[1]}
            if isinstance(row[2], str):
                binding['branch'] = row[2]
            return binding
        return None
    finally:
        db.close()


def item_message(item, session_id, agent_id, created_at='1970-01-01T00:00:00Z'):
    if not isinstance(item, dict) or not isinstance(item.get('id'), str):
        raise ConnectionError('Codex 未返回原生消息 ID。', 502)
    kind = item.get('type')
    role, message_kind, tool = 'assistant', 'message', None
    if kind == 'userMessage':
        role = 'user'
        text = '\n'.join(part.get('text', '') for part in item.get('content', []) if isinstance(part, dict) and part.get('type') == 'text')
    elif kind == 'reasoning':
        message_kind = 'thinking'
        text = '\n'.join(item.get('summary') or item.get('content') or [])
    elif kind in {'agentMessage', 'plan'}:
        text = item.get('text') or ''
    else:
        role, message_kind = 'tool', 'tool'
        text = str(item.get('aggregatedOutput') or item.get('result') or item.get('status') or '')
        tool = {'name': item.get('tool') or kind or 'Codex', 'arguments': item.get('arguments') or ({'command': item['command']} if item.get('command') else {}), 'status': item.get('status')}
    return {'id': item['id'], 'session_id': session_id, 'agent_id': agent_id, 'role': role, 'kind': message_kind, 'text': text, 'attachments': [], 'created_at': created_at, 'command_id': None, 'tool': tool}


class CodexConnection:
    agent_id = 'local-codex'

    def __init__(self, settings, store, service, client_factory=CodexAppServer):
        self.settings, self.store, self.service = settings, store, service
        self.client_factory = client_factory
        self.client = None
        self.state = 'disconnected'
        self.detail = '尚未连接本机 Codex。'
        self.auth_required = False
        self._threads = {}
        self._bindings = {}
        self._home = None
        self._owned_threads = set()
        self._active = {}
        self._pending = {}
        self._finished_pending = set()
        self._generation = uuid4().hex
        self._commands = {}
        self._approvals = {}
        self._approval_commands = {}
        self._stop_commands = {}
        self._streams = {}
        self._lock = threading.RLock()
        self._binding_changed = threading.Condition(self._lock)
        self._lifecycle = threading.Lock()
        self._session_locks = {}

    def _executable(self):
        value = self.settings.codex_executable or shutil.which('codex')
        return str(Path(value)) if value and Path(value).is_file() else None

    def snapshot(self):
        return {'kind': 'codex', 'state': self.state, 'available': bool(self._executable()), 'agent_id': self.agent_id, 'session_count': len(self._threads), 'auth_required': self.auth_required, 'detail': self.detail}

    def _agent(self, status):
        data = {'id': self.agent_id, 'kind': 'codex', 'name': '本机 Codex', 'source_id': self.agent_id, 'status': status, 'capabilities': ['chat', 'stop', 'events', 'history', 'approvals'], 'limitation': '原生 app-server 连接；当前未提供附件映射或远程 Codex 连接。'}
        if getattr(self, 'connection_id', None):
            data['connection_id'] = self.connection_id
            data['name'] = self.display_name + ' · Codex'
        data['limitation'] = '原生会话接入；暂未提供附件映射和永久删除。'
        self.store.upsert_agent(data)
        self.service.apply_connector_hello_event(data)

    def _request(self, method, params):
        client = self.client
        if client is None:
            raise ConnectionError('Codex 尚未连接。', 503)
        try:
            return client.request(method, params, timeout=30)
        except CodexRpcRejected as exc:
            raise ConnectionError(f'Codex 原生接口 {method} 拒绝请求：{exc}', 422) from None
        except (RuntimeError, OSError, queue.Empty, TimeoutError):
            raise ConnectionError(f'Codex 原生请求 {method} 未确认。', 502) from None

    def _pages(self, method, params):
        cursor, seen = None, set()
        while True:
            result = self._request(method, {**params, 'cursor': cursor})
            if not isinstance(result.get('data'), list):
                raise ConnectionError('Codex 原生列表响应无效。', 502)
            yield from result['data']
            cursor = result.get('nextCursor')
            if cursor is None:
                return
            if not isinstance(cursor, str) or cursor in seen:
                raise ConnectionError('Codex 原生分页未完成。', 502)
            seen.add(cursor)

    def connect(self):
        with self._lifecycle:
            if self.state == 'connected':
                return self.snapshot()
            executable = self._executable()
            if not executable:
                raise ConnectionError('未发现本机 Codex CLI，请先安装 Codex。', 503)
            if self.client:
                self.client.stop()
            self.state, self.detail = 'connecting', '正在与 Codex 原生接口握手…'
            config = CodexConnectorConfig(endpoint='', secret='', agent_id=self.agent_id, agent_name='本机 Codex', executable=executable, workspace=Path.cwd(), allowed_workspaces=self.settings.allowed_workspaces)
            self.client = self.client_factory(config, self._notification, on_close=self._closed)
            try:
                self.client.start()
                initialized = self._request('initialize', {'clientInfo': {'name': 'astrorder', 'title': 'Astrorder', 'version': '0.1.0'}, 'capabilities': {'experimentalApi': True}})
                self._home = Path(initialized['codexHome']) if initialized.get('codexHome') else None
                self.client.send({'method': 'initialized', 'params': {}})
                account = self._request('account/read', {'refreshToken': False})
                self.auth_required = bool(account.get('requiresOpenaiAuth') and not account.get('account'))
                rows = list(self._pages('thread/list', {'limit': 100, 'sortKey': 'updated_at', 'sortDirection': 'desc', 'archived': False, 'modelProviders': [], 'sourceKinds': ['cli', 'vscode', 'exec', 'appServer', 'subAgent', 'subAgentReview', 'subAgentCompact', 'subAgentThreadSpawn', 'subAgentOther', 'unknown'], 'useStateDbOnly': True}))
                self._agent('error' if self.auth_required else 'ready')
                self._threads = {}
                for row in rows:
                    self._record_thread(row)
                if self.auth_required:
                    self.state, self.detail = 'authentication_required', 'Codex 需要登录；请在本机 Codex 完成登录后重连。'
                else:
                    self.service.register_native_command_handler(self.agent_id, self.submit)
                    self.state, self.detail = 'connected', '原生握手、会话目录与命令通道已就绪。'
                return self.snapshot()
            except Exception as exc:  # noqa: BLE001 - every failed connection must close its owned transport
                self.client.stop()
                self.client = None
                self.state, self.detail = 'error', exc.detail if isinstance(exc, ConnectionError) else 'Codex 连接未完成；未标记为已连接。'
                self.service.clear_native_command_handler(self.agent_id)
                if self.store.get_agent(self.agent_id):
                    self._agent('error')
                raise ConnectionError(self.detail, 502) from None

    def _record_thread(self, thread):
        sid = thread.get('id') if isinstance(thread, dict) else None
        if not isinstance(sid, str) or not sid:
            raise ConnectionError('Codex 未返回原生 thread ID。', 502)
        self._threads[sid] = thread
        status = (thread.get('status') or {}).get('type')
        cwd = thread.get('cwd')
        data = {'id': sid, 'agent_id': self.agent_id, 'source_id': self.agent_id, 'source_session_id': sid, 'title': thread.get('name') or str(thread.get('preview') or '')[:160] or sid, 'workspace': cwd, 'status': 'running' if status == 'active' else 'error' if status == 'systemError' else 'idle', 'updated_at': timestamp(thread.get('updatedAt')), 'history_state': 'available', 'control_state': 'owned', 'project_id': thread.get('projectId'), 'project_name': Path(cwd).name if cwd else None}
        source = thread.get('source')
        if getattr(self, 'connection_id', None):
            data['connection_id'] = self.connection_id
        data['native_kind'] = 'subagent' if isinstance(source, dict) and 'subAgent' in source else source if isinstance(source, str) else None
        self.service.record_native_sessions([data])
        return data

    def _scope(self, sid):
        if self.store.get_session(self.agent_id, sid) is None:
            raise ConnectionError('Codex 会话不属于当前连接。', 404)

    def messages(self, sid, before=None, limit=2):
        self._scope(sid)
        cursor = None
        if before:
            try:
                if not before.startswith('codex:'):
                    raise ValueError()
                token = before[6:]
                value = json.loads(base64.urlsafe_b64decode(token + '=' * (-len(token) % 4)))
                if value['thread'] != sid or value['agent'] != self.agent_id:
                    raise ValueError()
                cursor = value['cursor']
            except (ValueError, KeyError, TypeError):
                raise ConnectionError('Codex 分页游标不属于当前会话。', 400) from None
        result = self._request('thread/items/list', {'threadId': sid, 'cursor': cursor, 'limit': limit, 'sortDirection': 'desc'})
        if not isinstance(result.get('data'), list):
            raise ConnectionError('Codex 消息分页响应无效。', 502)
        items = [item_message(entry['item'], sid, self.agent_id) for entry in reversed(result['data'])]
        native_cursor = result.get('nextCursor')
        next_cursor = None
        if native_cursor:
            payload = {'thread': sid, 'agent': self.agent_id, 'cursor': native_cursor}
            next_cursor = 'codex:' + base64.urlsafe_b64encode(json.dumps(payload, separators=(',', ':')).encode()).decode().rstrip('=')
        return {'items': items, 'next_cursor': next_cursor}

    def model(self, sid):
        self._scope(sid)
        if sid in self._owned_threads and sid in self._bindings:
            return dict(self._bindings[sid])
        if self._home:
            binding = stored_model(self._home, sid)
            if binding:
                return binding
            raise ConnectionError('Codex 原生记录尚未提供模型绑定；未使用全局默认模型代替。', 502)
        return self._resume(sid)

    def _resume(self, sid):
        if sid in self._owned_threads and sid in self._bindings:
            return dict(self._bindings[sid])
        response = self._request('thread/resume', {'threadId': sid, 'excludeTurns': True})
        thread = response.get('thread') or {}
        if thread.get('id') != sid or not response.get('model'):
            raise ConnectionError('Codex 会话模型尚未读回确认。', 502)
        self._threads[sid] = thread
        binding = {'model': response['model'], 'provider': response.get('modelProvider') or thread.get('modelProvider')}
        branch = (thread.get('gitInfo') or {}).get('branch')
        if isinstance(branch, str):
            binding['branch'] = branch
        self._bindings[sid] = binding
        self._owned_threads.add(sid)
        return binding

    def models(self, sid):
        provider = self.model(sid)['provider']
        return [{'provider': provider, 'model': row.get('model') or row['id'], 'label': row.get('displayName') or row.get('model') or row['id']} for row in self._pages('model/list', {'limit': 100, 'includeHidden': False}) if not row.get('hidden')]

    def set_model(self, sid, provider, model):
        if not any(row['provider'] == provider and row['model'] == model for row in self.models(sid)):
            raise ConnectionError('所选模型不在当前 Codex 原生目录中。', 422)
        self._resume(sid)
        self._request('thread/settings/update', {'threadId': sid, 'model': model})
        with self._binding_changed:
            confirmed = self._binding_changed.wait_for(lambda: self._bindings.get(sid, {}).get('model') == model and self._bindings.get(sid, {}).get('provider') == provider, timeout=3)
            if not confirmed:
                raise ConnectionError('Codex 模型切换尚未通过原生状态确认。', 502)
            return dict(self._bindings[sid])

    def open_ids(self):
        return list(self._pages('thread/loaded/list', {'limit': 100}))

    def create(self, workspace, title):
        path = self.validate_workspace(workspace)
        response = self._request('thread/start', {'cwd': path, 'ephemeral': False, 'persistExtendedHistory': True})
        data = self._record_thread(response.get('thread'))
        if response.get('model'):
            self._owned_threads.add(data['id'])
            self._bindings[data['id']] = {'model': response['model'], 'provider': response.get('modelProvider')}
        if title:
            self.mutate(data['id'], {'title': title})
            data['title'] = title
        return data

    def validate_workspace(self, workspace):
        path = Path(workspace).expanduser().resolve() if workspace else Path.cwd()
        if not path.is_dir() or (self.settings.allowed_workspaces and not any(path.is_relative_to(root.resolve()) for root in self.settings.allowed_workspaces)):
            raise ConnectionError('Codex 工作区不存在或不在允许范围。', 422)
        return str(path)

    def mutate(self, sid, updates):
        self._scope(sid)
        if updates is None:
            raise ConnectionError('当前连接尚未接入 Codex 永久删除；未用归档冒充删除。', 501)
        if set(updates) - {'title'}:
            raise ConnectionError('当前仅支持原生 Codex 会话重命名。', 422)
        if 'title' in updates:
            self._request('thread/name/set', {'threadId': sid, 'name': updates['title']})
            thread = self._request('thread/read', {'threadId': sid, 'includeTurns': False}).get('thread') or {}
            if thread.get('id') != sid or thread.get('name') != updates['title']:
                raise ConnectionError('Codex 重命名尚未读回确认。', 502)

    async def submit(self, command):
        return await asyncio.to_thread(self._submit, command)

    def _submit(self, command):
        sid = command['session_id']
        self._scope(sid)
        if self.state != 'connected':
            return 'failed', 'Codex 尚未连接。'
        action = command['action']
        if action in {'approve', 'cancel'}:
            with self._lock:
                approval = self._approvals.get(command.get('target_id'))
                if not approval or approval['session_id'] != sid:
                    return 'failed', '审批不属于当前 Codex 会话。'
                key = (sid, approval['native_id'])
                if key in self._approval_commands:
                    return 'failed', '授权正在等待原生确认，请勿重复提交。'
                self._approval_commands[key] = (dict(command), approval)
            self.client.send({'id': approval['native_id'], 'result': {'decision': 'accept' if action == 'approve' else 'decline'}})
            return 'accepted', None
        if action == 'stop':
            turn_id = self._active.get(sid)
            if command.get('target_id') != sid or not turn_id:
                return 'failed', '当前 Codex 会话没有已确认的活动轮次。'
            self._stop_commands.setdefault((sid, turn_id), []).append(dict(command))
            self._request('turn/interrupt', {'threadId': sid, 'turnId': turn_id})
            return 'accepted', None
        if action != 'send' or command.get('attachments'):
            return 'failed', '当前 Codex 连接仅支持文本发送。'
        with self._lock:
            lock = self._session_locks.setdefault(sid, threading.Lock())
        with lock:
            if sid in self._active:
                return 'failed', 'Codex 正在执行；请等待当前轮次或停止后发送。'
            try:
                self._resume(sid)  # Resume for explicit commands only, not metadata reads.
            except ConnectionError as exc:
                return 'failed', exc.detail  # No turn/start was submitted.
            with self._lock:
                self._pending[sid] = dict(command)
            try:
                response = self._request('turn/start', {'threadId': sid, 'input': [{'type': 'text', 'text': command['text']}]})
                turn = response.get('turn') or {}
                turn_id = turn.get('id')
                if not isinstance(turn_id, str):
                    return 'unknown', 'Codex 未返回轮次 ID；不会自动重发。'
                with self._lock:
                    if (sid, turn_id) not in self._finished_pending:
                        self._commands[(sid, turn_id)] = dict(command)
                        if turn.get('status') not in {'completed', 'failed', 'interrupted'}:
                            self._active[sid] = turn_id
                return 'accepted', None
            except ConnectionError as exc:
                if exc.status_code == 422:
                    return 'failed', exc.detail
                return 'unknown', 'Codex 发送结果未确认；不会自动重发。'
            finally:
                with self._lock:
                    self._pending.pop(sid, None)
                    self._finished_pending = {key for key in self._finished_pending if key[0] != sid}

    def _event(self, kind, sid, data):
        self.service.accept_connector_event(self.agent_id, {'id': str(uuid4()), 'type': kind, 'agent_id': self.agent_id, 'session_id': sid, 'data': data})

    def _session_status(self, sid, status):
        session = self.store.get_session(self.agent_id, sid)
        if session:
            self._event('session.upsert', sid, {**session, 'status': status, 'updated_at': timestamp()})

    def _notification(self, frame):
        method, params = frame.get('method'), frame.get('params') or {}
        sid = params.get('threadId')
        if 'id' in frame:
            if method in {'item/commandExecution/requestApproval', 'item/fileChange/requestApproval'} and self.store.get_session(self.agent_id, sid):
                key = f"codex-approval-{self._generation}-{frame['id']}"
                data = {'id': key, 'agent_id': self.agent_id, 'session_id': sid, 'title': 'Codex 请求执行授权', 'detail': str(params.get('command') or params.get('reason') or method), 'state': 'pending', 'target_id': key, 'data': {}}
                self._approvals[key] = {'native_id': frame['id'], 'session_id': sid, 'data': data}
                self._event('approval.upsert', sid, data)
                self._session_status(sid, 'waiting_approval')
            elif self.client:
                self.client.send({'id': frame['id'], 'error': {'code': -32601, 'message': 'This client does not support this server request'}})
            return
        if method == 'thread/started' and self.store.get_agent(self.agent_id):
            self._record_thread(params['thread'])
            return
        if not isinstance(sid, str) or self.store.get_session(self.agent_id, sid) is None:
            return
        if method == 'thread/settings/updated':
            settings = params.get('threadSettings') or {}
            if isinstance(settings.get('model'), str) and isinstance(settings.get('modelProvider'), str):
                with self._binding_changed:
                    self._bindings[sid] = {**self._bindings.get(sid, {}), 'model': settings['model'], 'provider': settings['modelProvider']}
                    self._binding_changed.notify_all()
            return
        if method == 'serverRequest/resolved':
            with self._lock:
                resolved = self._approval_commands.pop((sid, params.get('requestId')), None)
                if resolved:
                    self._approvals.pop(resolved[0]['target_id'], None)
            if resolved:
                command, approval = resolved
                self._event('approval.upsert', sid, {**approval['data'], 'state': 'approved' if command['action'] == 'approve' else 'rejected'})
                self._event('command.upsert', sid, {**command, 'state': 'completed', 'error': None})
            return
        if method in {'turn/started', 'turn/completed'}:
            turn = params.get('turn') or {}
            turn_id = turn.get('id')
            with self._lock:
                if method == 'turn/started':
                    self._active[sid] = turn_id
                    command = self._pending.get(sid)
                    if command:
                        self._commands[(sid, turn_id)] = command
                else:
                    if sid in self._pending:
                        self._finished_pending.add((sid, turn_id))
                    self._active.pop(sid, None)
                    command = self._commands.pop((sid, turn_id), None) or self._pending.get(sid)
            self._session_status(sid, 'running' if method == 'turn/started' else 'error' if turn.get('status') == 'failed' else 'idle')
            if command:
                state = 'running' if method == 'turn/started' else 'failed' if turn.get('status') == 'failed' else 'cancelled' if turn.get('status') == 'interrupted' else 'completed'
                self._event('command.upsert', sid, {**command, 'state': state, 'error': 'Codex 原生轮次失败。' if state == 'failed' else None})
            if method == 'turn/completed':
                for stop in self._stop_commands.pop((sid, turn_id), []):
                    self._event('command.upsert', sid, {**stop, 'state': 'completed', 'error': None})
        elif method in {'item/started', 'item/completed'}:
            item = params.get('item')
            if isinstance(item, dict) and isinstance(item.get('id'), str):
                message = item_message(item, sid, self.agent_id, timestamp())
                self._streams[(sid, item['id'])] = message
                self._event('message.upsert', sid, message)
        elif method in {'item/agentMessage/delta', 'item/reasoning/summaryTextDelta', 'item/commandExecution/outputDelta'}:
            item_id, delta = params.get('itemId'), params.get('delta')
            if not isinstance(item_id, str) or not isinstance(delta, str):
                return
            key = (sid, item_id)
            message = self._streams.get(key)
            if message is None:
                kind = 'reasoning' if 'reasoning' in method else 'commandExecution' if 'commandExecution' in method else 'agentMessage'
                message = item_message({'id': item_id, 'type': kind}, sid, self.agent_id, timestamp())
            message = {**message, 'text': message['text'] + delta}
            self._streams[key] = message
            self._event('message.upsert', sid, message)

    def _closed(self):
        self.state, self.detail = 'error', 'Codex 原生连接已断开；请重连。'
        self.service.clear_native_command_handler(self.agent_id)
        self._retire_inflight()
        if self.store.get_agent(self.agent_id):
            self._agent('disconnected')

    def _retire_inflight(self):
        for command in self.store.active_commands():
            if command['agent_id'] == self.agent_id:
                self._event('command.upsert', command['session_id'], {**command, 'state': 'unknown', 'error': 'Codex 连接已断开，执行结果未确认；不会自动重发。'})
        for sid in list(self._active):
            self._session_status(sid, 'error')
        for approval in list(self._approvals.values()):
            self._event('approval.upsert', approval['session_id'], {**approval['data'], 'state': 'cancelled'})
        self._active.clear()
        self._pending.clear()
        self._commands.clear()
        self._approvals.clear()
        self._approval_commands.clear()
        self._stop_commands.clear()
        self._streams.clear()
        self._owned_threads.clear()
        self._bindings.clear()
        self._generation = uuid4().hex

    def disconnect(self):
        with self._lifecycle:
            self.service.clear_native_command_handler(self.agent_id)
            client, self.client = self.client, None
            if client:
                client.stop()
            self._retire_inflight()
            self.state, self.detail = 'disconnected', 'Codex 已断开；未停止其他 Codex 进程。'
            self._active.clear()
            self._pending.clear()
            self._commands.clear()
            self._streams.clear()
            if self.store.get_agent(self.agent_id):
                self._agent('disconnected')
            return self.snapshot()
