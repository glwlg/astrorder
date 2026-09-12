"""Owned Codex app-server connection; native threads/items remain authoritative."""
from __future__ import annotations

import asyncio
import base64
import json
import logging
import os
import queue
import re
import shutil
import sqlite3
import tempfile
import threading
from datetime import UTC, datetime
from pathlib import Path
from uuid import uuid4

from astrorder_codex_connector.app_server import CodexAppServer, CodexRpcRejected
from astrorder_codex_connector.config import CodexConnectorConfig

from .codex_policy import codex_turn_policy
from .codex_tasks import project_task_history, project_tasks
from .connections import ConnectionError
from .system_environment import load_system_environment

logger = logging.getLogger(__name__)


def timestamp(value=None):
    return datetime.fromtimestamp(value, UTC).isoformat() if isinstance(value, (float, int)) else datetime.now(UTC).isoformat()


_FAILURE_DETAIL_KEYS = ('message', 'detail', 'userMessage', 'errorMessage', 'reason', 'description', 'error', 'failure', 'lastError')


def _redact_failure_detail(value):
    text = str(value or '').replace('\x00', ' ').replace('\r', ' ').replace('\n', ' ').strip()
    text = re.sub(
        r'(?i)(password|passwd|token|secret|authorization|api[_ -]?key)(\s*[:=]\s*)[^\s,;]+',
        r'\1\2[REDACTED]',
        text,
    )
    return text[:1000]


def codex_turn_failure_reason(turn, params):
    """Project the native failure message without exposing credential values."""
    def extract(value, depth=0):
        if depth > 3:
            return None
        if isinstance(value, str):
            return _redact_failure_detail(value) or None
        if isinstance(value, dict):
            for key in _FAILURE_DETAIL_KEYS:
                detail = extract(value.get(key), depth + 1)
                if detail:
                    return detail
        if isinstance(value, (list, tuple)):
            for entry in value:
                detail = extract(entry, depth + 1)
                if detail:
                    return detail
        return None

    for value in (
        turn.get('error') if isinstance(turn, dict) else None,
        turn.get('failure') if isinstance(turn, dict) else None,
        turn.get('lastError') if isinstance(turn, dict) else None,
        turn.get('errorMessage') if isinstance(turn, dict) else None,
        turn.get('message') if isinstance(turn, dict) else None,
        params.get('error') if isinstance(params, dict) else None,
        params.get('failure') if isinstance(params, dict) else None,
        params.get('errorMessage') if isinstance(params, dict) else None,
        params.get('message') if isinstance(params, dict) else None,
    ):
        detail = extract(value)
        if detail:
            return detail
    return 'Codex 原生轮次失败，原生端未返回详细原因。'


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

def stored_effort(home, sid):
    files = [(int(match.group(1)), file) for file in home.glob('state_*.sqlite') if (match := re.fullmatch(r'state_(\d+)\.sqlite', file.name))]
    if not files:
        return None
    database = max(files, key=lambda pair: pair[0])[1]
    db = sqlite3.connect(database.resolve().as_uri() + '?mode=ro', uri=True, timeout=3)
    try:
        columns = {row[1] for row in db.execute('PRAGMA table_info(threads)')}
        if 'reasoning_effort' not in columns:
            return None
        row = db.execute('SELECT reasoning_effort FROM threads WHERE id = ?', (sid,)).fetchone()
        return row[0] if row and isinstance(row[0], str) else None
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
        summary = item.get('summary') or []
        content = item.get('content') or []
        text = '\n'.join(summary if summary and not content else ([*summary, '', *content] if summary else content))
    elif kind in {'agentMessage', 'plan'}:
        text = item.get('text') or ''
    else:
        role, message_kind = 'tool', 'tool'
        text = str(item.get('aggregatedOutput') or item.get('result') or item.get('status') or '')
        args = item.get('arguments') or {}
        if not isinstance(args, dict):
            args = {}
        if item.get('command') and 'command' not in args:
            args['command'] = item['command']
        if item.get('changes') and 'changes' not in args:
            args['changes'] = item['changes']
        if item.get('path') and 'path' not in args:
            args['path'] = item['path']
        if item.get('cwd') and 'cwd' not in args:
            args['cwd'] = item['cwd']
        tool_name = item.get('tool') or kind or 'Codex'
        if kind == 'imageView' and item.get('path'):
            args['path'] = item['path']
        elif kind == 'mcpToolCall':
            tool_name = f"mcp:{item.get('server')}.{item.get('tool')}"
        tool = {'name': tool_name, 'arguments': args, 'status': item.get('status')}
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
        self._queued = {}
        self._commands = {}
        self._approvals = {}
        self._approval_commands = {}
        self._approval_modes = {}
        self._efforts = {}
        self._stop_commands = {}
        self._streams = {}
        self._lock = threading.RLock()
        self._binding_changed = threading.Condition(self._lock)
        self._lifecycle = threading.Lock()
        self._session_locks = {}
        self._daemon_controller_factory = None
        self._daemon_controller = None

    def _executable(self):
        value = self.settings.codex_executable or shutil.which('codex')
        return str(Path(value)) if value and Path(value).is_file() else None

    def snapshot(self):
        return {'kind': 'codex', 'state': self.state, 'available': bool(self._executable()), 'agent_id': self.agent_id, 'session_count': len(self._threads), 'auth_required': self.auth_required, 'detail': self.detail, 'daemon_mode': self._daemon_controller_factory is not None}

    def set_daemon_controller_factory(self, factory):
        if factory is not None and not callable(factory):
            raise TypeError('daemon controller factory must be callable')
        self._close_daemon_controller()
        self._daemon_controller_factory = factory

    def _activate_daemon_controller(self):
        factory = self._daemon_controller_factory
        if factory is None:
            return
        controller = factory(self)
        if controller is None or not callable(getattr(controller, 'activate', None)) or not callable(getattr(controller, 'submit', None)):
            raise ConnectionError('daemon Codex controller is invalid.', 503)
        controller.activate()
        self._daemon_controller = controller
        self.service.register_native_command_handler(self.agent_id, controller.submit)

    def _close_daemon_controller(self):
        controller, self._daemon_controller = self._daemon_controller, None
        close = getattr(controller, 'close', None)
        if callable(close):
            try:
                close()
            except Exception:
                logger.warning('daemon Codex controller cleanup failed')

    def _agent(self, status):
        data = {'id': self.agent_id, 'kind': 'codex', 'name': '本机 Codex', 'source_id': self.agent_id, 'status': status, 'capabilities': ['chat', 'stop', 'events', 'history', 'approvals', 'queue'], 'limitation': '原生 app-server 连接；当前未提供附件映射或远程 Codex 连接。'}
        if getattr(self, 'connection_id', None):
            data['connection_id'] = self.connection_id
            data['name'] = self.display_name + ' · Codex'
        data['capabilities'] += ['attachments', 'delete']
        data['limitation'] = '原生 app-server 提供控制；附件支持星序上传的图片与音频。文档尚未映射。其他客户端本地图片仅在 Codex 数据目录内映射为预览。永久删除需原生接口确认。观察钩子不赋予其他客户端活动轮次的控制权。'
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
        except RuntimeError as exc:
            detail = _redact_failure_detail(exc)
            raise ConnectionError(
                f'Codex 原生请求 {method} 未确认：{detail}' if detail else f'Codex 原生请求 {method} 未确认。',
                502,
            ) from None
        except (OSError, queue.Empty, TimeoutError):
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
            self.client = self.client_factory(
                config,
                self._notification,
                on_close=self._closed,
                environment=load_system_environment(),
            )
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
                self._purge_archived_sessions()
                if self.auth_required:
                    self.state, self.detail = 'authentication_required', 'Codex 需要登录；请在本机 Codex 完成登录后重连。'
                else:
                    self.service.register_native_command_handler(self.agent_id, self.submit)
                    self._activate_daemon_controller()
                    self.state, self.detail = 'connected', '原生握手、会话目录与命令通道已就绪。'
                    self._reconcile_tasks()
                return self.snapshot()
            except Exception as exc:  # noqa: BLE001 - every failed connection must close its owned transport
                self._close_daemon_controller()
                self.client.stop()
                self.client = None
                self.state, self.detail = 'error', exc.detail if isinstance(exc, ConnectionError) else 'Codex 连接未完成；未标记为已连接。'
                self.service.clear_native_command_handler(self.agent_id)
                if self.store.get_agent(self.agent_id):
                    self._agent('error')
                raise ConnectionError(self.detail, 502) from None

    def _archived_ids_from_db(self):
        home = self._home or (Path.home() / '.codex')
        dbs = sorted(home.glob('state_*.sqlite'))
        if not dbs and (home / 'state.db').is_file():
            dbs = [home / 'state.db']
        res = set()
        for db in dbs:
            if db.is_file():
                try:
                    with sqlite3.connect(db) as conn:
                        for r in conn.cursor().execute("SELECT id FROM threads WHERE archived=1"):
                            res.add(r[0])
                except (OSError, sqlite3.Error):
                    pass
        return res

    def _purge_archived_sessions(self):
        try:
            archived_ids = self._archived_ids_from_db()
            for aid in archived_ids:
                if self.store.get_session(self.agent_id, aid) is not None:
                    self.service.delete_session(self.agent_id, aid)
        except ConnectionError:
            pass

    def _record_thread(self, thread):
        sid = thread.get('id') if isinstance(thread, dict) else None
        if not isinstance(sid, str) or not sid:
            raise ConnectionError('Codex 未返回原生 thread ID。', 502)
        is_archived = bool(
            thread.get('archived')
            or (isinstance(thread.get('status'), dict) and thread.get('status', {}).get('type') == 'archived')
            or thread.get('status') == 'archived'
        )
        if is_archived:
            self._threads.pop(sid, None)
            if self.store.get_session(self.agent_id, sid) is not None:
                self.service.delete_session(self.agent_id, sid)
            return None
        self._threads[sid] = thread
        status = (thread.get('status') or {}).get('type')
        cwd = thread.get('cwd')
        data = {'id': sid, 'agent_id': self.agent_id, 'source_id': self.agent_id, 'source_session_id': sid, 'title': thread.get('name') or str(thread.get('preview') or '')[:160] or sid, 'workspace': cwd, 'status': 'running' if status == 'active' else 'error' if status == 'systemError' else 'idle', 'updated_at': timestamp(thread.get('updatedAt')), 'history_state': 'available', 'control_state': 'owned', 'project_id': thread.get('projectId'), 'project_name': Path(cwd).name if cwd else None}
        source = thread.get('source')
        if getattr(self, 'connection_id', None):
            data['connection_id'] = self.connection_id
        data['native_kind'] = 'subagent' if isinstance(source, dict) and 'subAgent' in source else source if isinstance(source, str) else None
        data['ephemeral'] = thread.get('ephemeral') is True
        self.service.record_native_sessions([data])
        return data

    def _scope(self, sid):
        if self.store.get_session(self.agent_id, sid) is None:
            raise ConnectionError('Codex 会话不属于当前连接。', 404)

    def _roots(self, sid=None):
        roots = []
        if self._home:
            roots.append(self._home)
        roots.append(Path.home() / '.codex')
        cwd = (self._threads.get(sid) or {}).get('cwd')
        if isinstance(cwd, str) and cwd:
            roots.append(Path(cwd))
        roots.append(Path(tempfile.gettempdir()))
        for env_k in ('TEMP', 'TMP'):
            val = os.environ.get(env_k)
            if val:
                roots.append(Path(val))
        roots.append(Path.home() / 'AppData' / 'Local' / 'Temp')
        return roots

    def _remote_bytes(self, path):
        reader = getattr(self, 'remote_json', None)
        if not callable(reader) or not self._home:
            return None
        source = (
            'import json,base64\nfrom pathlib import Path\n'
            'p=Path(' + repr(path) + ').expanduser()\n'
            'root=Path(' + repr(self._home.as_posix()) + ').resolve()\n'
            'try:\n'
            ' r=p.resolve(); r.relative_to(root)\n'
            ' data=r.read_bytes()\n'
            'except Exception:\n'
            ' print(json.dumps(None)); raise SystemExit\n'
            'if len(data)>10485760:\n'
            ' print(json.dumps(None)); raise SystemExit\n'
            'print(json.dumps({"data":base64.b64encode(data).decode("ascii"),"name":r.name}))\n'
        )
        try:
            payload = reader(source)
        except ConnectionError:
            return None
        if not isinstance(payload, dict) or not isinstance(payload.get('data'), str):
            return None
        try:
            return base64.b64decode(payload['data'], validate=True)
        except (ValueError, TypeError):
            return None

    def _message(self, item, sid, turn_id=None, created_at='1970-01-01T00:00:00Z'):
        message=item_message(item,sid,self.agent_id,created_at)
        if message['role']=='user':
            cached=self.store.get_message(self.agent_id,sid,message['id'])
            command=self._commands.get((sid,turn_id)) or (self._pending.get(sid) if turn_id else None)
            if command:
                message['command_id']=command['id']
                message['attachments']=list(command.get('attachments',[]))
            elif isinstance(cached,dict) and cached.get('attachments'):
                message['attachments']=cached.get('attachments',[])
                message['command_id']=cached.get('command_id')
            else:
                content=item.get('content') if isinstance(item, dict) else None
                has_media=isinstance(content,list) and any(isinstance(part,dict) and part.get('type') in {'image','localImage','audio','localAudio','local_image','local_audio'} for part in content)
                if has_media:
                    from .attachments import AttachmentManager
                    from .native_attachments import bind_codex_item
                    remote_root = self._home.as_posix() if self._home and hasattr(self, 'remote_json') else None
                    message['attachments']=bind_codex_item(
                        item,
                        AttachmentManager(self.settings, self.store),
                        self._roots(sid),
                        remote_read=self._remote_bytes if remote_root else None,
                        remote_root=remote_root,
                    )
                if not message.get('attachments'):
                    from .attachments import AttachmentManager
                    from .native_attachments import import_local_file
                    img_paths = []
                    raw_text = message.get('text') or ''
                    for m in re.finditer(r'##\s*[\w\.-]+\.(?:png|jpe?g|gif|webp|svg):\s*(.+)', raw_text):
                        img_paths.append(m.group(1).strip())
                    for m in re.finditer(r'<image\s+[^>]*path=["\']([^"\']+)["\']', raw_text):
                        img_paths.append(m.group(1).strip())
                    if img_paths:
                        manager = AttachmentManager(self.settings, self.store)
                        roots = self._roots(sid)
                        attachments = []
                        for ipath in img_paths:
                            mapped = import_local_file(manager, ipath, roots)
                            if mapped:
                                attachments.append(mapped)
                        if attachments:
                            message['attachments'] = attachments
                if isinstance(cached, dict):
                    message['command_id']=cached.get('command_id')
            raw_text = message.get('text') or ''
            if '# Files mentioned by the user:' in raw_text and '## My request:' in raw_text:
                parts = raw_text.split('## My request:', 1)
                if len(parts) > 1 and parts[1].strip():
                    message['text'] = parts[1].strip()
        elif message['role'] == 'assistant':
            # Check for markdown image patterns like ![alt](path/to/image.png)
            text = message.get('text', '')
            img_matches = list(re.finditer(r'!\[([^\]]*)\]\(([^)]+)\)', text))
            if img_matches:
                from .attachments import AttachmentManager
                from .native_attachments import import_local_file
                manager = AttachmentManager(self.settings, self.store)
                roots = self._roots(sid)
                attachments = list(message.get('attachments') or [])
                for match in img_matches:
                    img_path = match.group(2)
                    if img_path.startswith(('http://', 'https://', 'data:', '/api/')):
                        continue
                    mapped = import_local_file(manager, img_path, roots)
                    if mapped:
                        attachments.append(mapped)
                        # Replace the image markdown link with mapped attachment url
                        text = text.replace(match.group(0), f'![{match.group(1)}]({mapped["url"]})')
                message['attachments'] = attachments
                message['text'] = text
        elif message['kind'] == 'tool' and message.get('tool', {}).get('name') == 'imageView':
            path = (message.get('tool', {}).get('arguments') or {}).get('path')
            if isinstance(path, str) and path:
                from .attachments import AttachmentManager
                from .native_attachments import import_local_file
                manager = AttachmentManager(self.settings, self.store)
                roots = self._roots(sid)
                mapped = import_local_file(manager, path, roots)
                if mapped:
                    message['attachments'] = [mapped]
        return message

    def messages(self, sid, before=None, limit=2):
        self._scope(sid)
        history_started = timestamp()
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
                stored_items, next_cursor = self.store.list_messages(self.agent_id, sid, before, limit)
                if stored_items:
                    return {'items': stored_items, 'next_cursor': next_cursor}
                raise ConnectionError('Codex 分页游标不属于当前会话。', 400) from None
        use_items_list = True
        try:
            result = self._request('thread/items/list', {'threadId': sid, 'cursor': cursor, 'limit': limit, 'sortDirection': 'desc'})
        except ConnectionError as exc:
            exc_str = str(exc).lower()
            if '-32601' in exc_str or 'not supported' in exc_str:
                use_items_list = False
            elif 'rollout' in exc_str or '-32600' in exc_str or 'not found' in exc_str:
                stored, next_c = self.store.list_messages(self.agent_id, sid, before, limit)
                if stored:
                    return {'items': stored, 'next_cursor': next_c}
                return {'items': [], 'next_cursor': None}
            else:
                raise

        if use_items_list:
            if not isinstance(result.get('data'), list):
                raise ConnectionError('Codex 消息分页响应无效。', 502)
            project_task_history(self, sid, result['data'], history_started, refresh_unknown=not before)
            items = [self._message(entry['item'], sid) for entry in reversed(result['data'])]
            for m in items:
                self.store.upsert_message(m)
            native_cursor = result.get('nextCursor')
            next_cursor = None
            if native_cursor:
                payload = {'thread': sid, 'agent': self.agent_id, 'cursor': native_cursor}
                next_cursor = 'codex:' + base64.urlsafe_b64encode(json.dumps(payload, separators=(',', ':')).encode()).decode().rstrip('=')
            return {'items': items, 'next_cursor': next_cursor}

        # Fallback: 当前 Codex 版本不支持 thread/items/list，从 thread/read(includeTurns=True) 同步
        try:
            thread_data = self._request('thread/read', {'threadId': sid, 'includeTurns': True})
            turns = (thread_data.get('thread') or {}).get('turns') or []
            entries = ({'item': item} for turn in reversed(turns) for item in reversed(turn.get('items') or []))
            project_task_history(self, sid, entries, history_started, refresh_unknown=not before)
            first_user_text = None
            for turn in turns:
                turn_id = turn.get('id')
                for item in turn.get('items') or []:
                    msg = self._message(item, sid, turn_id=turn_id)
                    if not first_user_text and msg.get('role') == 'user' and msg.get('text'):
                        first_user_text = msg['text']
                    self.store.upsert_message(msg)
            if first_user_text:
                session_row = self.store.get_session(self.agent_id, sid)
                if session_row and (session_row.get('title') == sid or not session_row.get('title')):
                    cleaned_title = first_user_text.strip().splitlines()[0][:60]
                    if cleaned_title:
                        self.store.upsert_session({**session_row, 'title': cleaned_title})
        except Exception:
            pass

        stored_items, next_cursor = self.store.list_messages(self.agent_id, sid, before, limit)
        return {'items': stored_items, 'next_cursor': next_cursor}

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

    def _bind_resumed_thread(self, sid, response):
        """Validate and cache a real native thread/resume response.

        Callers must never fabricate this state from a failed resume: downstream
        controls such as thread/settings/update require an actually loaded thread.
        """
        thread = response.get('thread') or {}
        if thread.get('id') != sid or not response.get('model'):
            raise ConnectionError('Codex 会话模型尚未读回确认。', 502)
        self._threads[sid] = thread
        binding = {'model': response['model'], 'provider': response.get('modelProvider') or thread.get('modelProvider')}
        branch = (thread.get('gitInfo') or {}).get('branch')
        if isinstance(branch, str):
            binding['branch'] = branch
        self._bindings[sid] = binding
        from .native_controls import REASONING_EFFORTS
        effort = response.get('reasoningEffort')
        if effort in REASONING_EFFORTS:
            self._efforts[sid] = effort
        self._owned_threads.add(sid)
        return binding

    def _resume(self, sid):
        if sid in self._owned_threads and sid in self._bindings:
            return dict(self._bindings[sid])
        response = self._request('thread/resume', {'threadId': sid, 'excludeTurns': True})
        return self._bind_resumed_thread(sid, response)

    def models(self, sid):
        provider = self.model(sid)['provider']
        return [{'provider': provider, 'model': row.get('model') or row['id'], 'label': row.get('displayName') or row.get('model') or row['id']} for row in self._pages('model/list', {'limit': 100, 'includeHidden': False}) if not row.get('hidden')]

    def set_model(self, sid, provider, model):
        if self._daemon_controller is not None:
            return self._daemon_controller.set_model(sid, provider, model)
        if not any(row['provider'] == provider and row['model'] == model for row in self.models(sid)):
            raise ConnectionError('所选模型不在当前 Codex 原生目录中。', 422)
        self._resume(sid)
        self._request('thread/settings/update', {'threadId': sid, 'model': model})
        with self._binding_changed:
            confirmed = self._binding_changed.wait_for(lambda: self._bindings.get(sid, {}).get('model') == model and self._bindings.get(sid, {}).get('provider') == provider, timeout=3)
            if not confirmed:
                raise ConnectionError('Codex 模型切换尚未通过原生状态确认。', 502)
            return dict(self._bindings[sid])

    def current_effort(self, sid):
        self._scope(sid)
        from .native_controls import REASONING_EFFORTS
        if sid in self._efforts:
            return self._efforts[sid]
        if self._home:
            val = stored_effort(self._home, sid)
            if val in REASONING_EFFORTS:
                return val
        return None

    def set_effort(self, sid, effort):
        if self._daemon_controller is not None:
            return self._daemon_controller.set_effort(sid, effort)
        from .native_controls import REASONING_EFFORTS
        if effort not in REASONING_EFFORTS:
            raise ConnectionError('思考强度不在原生支持范围内。', 422)
        self._resume(sid)
        self._request('thread/settings/update', {'threadId': sid, 'effort': effort})
        with self._binding_changed:
            confirmed = self._binding_changed.wait_for(lambda: self._efforts.get(sid) == effort, timeout=3)
            if not confirmed:
                raise ConnectionError('Codex 思考强度尚未读回确认。', 502)
            return {'effort': effort}

    def get_approval_mode(self, sid):
        self._scope(sid)
        return self._approval_modes.get(sid, 'auto')

    def set_approval_mode(self, sid, mode):
        self._scope(sid)
        if mode not in {'manual', 'auto', 'full_access'}:
            raise ConnectionError('不支持的审批模式；可选 manual、auto、full_access。', 422)
        self._approval_modes[sid] = mode
        return {'mode': mode}

    def open_ids(self):
        return list(self._pages('thread/loaded/list', {'limit': 100}))

    def create(self, workspace, title, ephemeral=False, parent_session_id=None):
        if self._daemon_controller is not None:
            return self._daemon_controller.create(
                workspace,
                title,
                ephemeral=ephemeral,
                parent_session_id=parent_session_id,
            )
        path = self.validate_workspace(workspace)
        if parent_session_id:
            self._scope(parent_session_id)
            response = self._request('thread/fork', {
                'threadId': parent_session_id,
                'cwd': path,
                'ephemeral': bool(ephemeral),
                'excludeTurns': True,
                'deferGoalContinuation': True,
            })
        else:
            response = self._request('thread/start', {
                'cwd': path,
                'ephemeral': bool(ephemeral),
                'persistExtendedHistory': not ephemeral,
            })
        data = self._record_thread(response.get('thread'))
        if ephemeral:
            data['ephemeral'] = True
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
            if sid in self._active:
                raise ConnectionError('会话正在运行，未执行永久删除。', 409)
            try:
                self._request('thread/delete', {'threadId': sid})
            except ConnectionError as exc:
                # If thread was not initialized or corrupted in rollout (-32603; rollout, thread), fallback to deleting directly
                if 'rollout' in str(exc).lower() or '-32603' in str(exc):
                    if getattr(self, 'remote_json', None) and getattr(self, '_home', None):
                        source = 'import sqlite3,json\\nfrom pathlib import Path\\nfor p in Path(' + repr(self._home.as_posix()) + ').glob(\"state_*.sqlite\"):\\n    with sqlite3.connect(p) as db:\\n        db.execute(\"DELETE FROM threads WHERE id=?\", (' + repr(sid) + ',))\\n        db.commit()\\nprint(json.dumps({\"deleted\":True}))'
                        self.remote_json(source)
                else:
                    raise
            for archived in (False, True):
                rows=self._pages('thread/list', {'limit':100,'archived':archived,'modelProviders':[],'sourceKinds':['cli','vscode','exec','appServer','subAgent','subAgentReview','subAgentCompact','subAgentThreadSpawn','subAgentOther','unknown'],'useStateDbOnly':True})
                if any(row.get('id')==sid for row in rows):
                    raise ConnectionError('原生目录仍包含此会话，删除尚未确认。',502)
            self._threads.pop(sid,None)
            return
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
        if action not in {'send', 'enqueue'}:
            return 'failed', '当前 Codex 连接未提供此命令类型。'
        from .codex_inputs import command_input
        try:
            inputs=command_input(self.settings,self.store,command)
        except ConnectionError as exc:
            return 'failed', exc.detail
        with self._lock:
            lock = self._session_locks.setdefault(sid, threading.Lock())
        with lock:
            if sid in self._active:
                # Codex 当前正在执行轮次：优先尝试 turn/steer 无感注入当前轮次进行动态引导（不打断会话），失败时再降级进入排队
                active_turn_id = self._active[sid]
                try:
                    steer_params = {
                        'threadId': sid,
                        'expectedTurnId': active_turn_id,
                        'input': inputs,
                    }
                    self._request('turn/steer', steer_params)
                    # turn/steer 成功注入当前活动轮次，将 command 关联至当前轮次并标记为 accepted
                    self._commands[(sid, active_turn_id)] = dict(command)
                    self.store.set_command_state(self.agent_id, sid, command['id'], 'accepted', None)
                    self._event('command.upsert', sid, {**command, 'state': 'accepted', 'error': None})
                    return 'accepted', None
                except Exception as steer_exc:
                    logger.info("Codex turn/steer not accepted or supported (%s); enqueuing command instead.", steer_exc)
                    # Enqueue the command to dispatch automatically when current turn finishes.
                    with self._lock:
                        self._queued.setdefault(sid, []).append(dict(command))
                    self.store.set_command_state(self.agent_id, sid, command['id'], 'queued', None)
                    self._event('command.upsert', sid, {**command, 'state': 'queued', 'error': None})
                    return 'accepted', None
            try:
                self._resume(sid)  # Resume for explicit commands only, not metadata reads.
            except ConnectionError as exc:
                return 'failed', exc.detail  # No turn/start was submitted.
            with self._lock:
                self._pending[sid] = dict(command)
            try:
                mode = self.get_approval_mode(sid)
                turn_params = {'threadId': sid, 'input': inputs}
                turn_params.update(codex_turn_policy(mode))
                response = self._request('turn/start', turn_params)
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
            updated = {**session, 'status': status, 'updated_at': timestamp()}
            self.store.upsert_session(updated)
            self._event('session.upsert', sid, updated)

    def _notification(self, frame):
        method, params = frame.get('method'), frame.get('params') or {}
        sid = params.get('threadId')
        if 'id' in frame:
            if method in {'item/commandExecution/requestApproval', 'item/fileChange/requestApproval'} and self.store.get_session(self.agent_id, sid):
                mode = self.get_approval_mode(sid) if sid else 'auto'
                if mode == 'full_access':
                    self.client.send({'id': frame['id'], 'result': {'decision': 'accept'}})
                    return
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
        if method in {'thread/archived', 'thread/archive', 'thread/deleted'} or (
            method == 'thread/updated' and (params.get('thread') or {}).get('archived')
        ):
            target_sid = sid or params.get('id') or (params.get('thread') or {}).get('id')
            if target_sid:
                self._threads.pop(target_sid, None)
                self.service.delete_session(self.agent_id, target_sid)
            return
        if method == 'thread/name/updated':
            name = params.get('name')
            if sid and isinstance(name, str) and name:
                session = self.store.get_session(self.agent_id, sid)
                if session:
                    updated = {**session, 'title': name, 'updated_at': timestamp()}
                    self.store.upsert_session(updated)
                    self._event('session.upsert', sid, updated)
            return
        if not isinstance(sid, str) or self.store.get_session(self.agent_id, sid) is None:
            return
        if method in {'turn/plan/updated', 'item/started', 'item/completed', 'item/commandExecution/outputDelta'}:
            project_tasks(self, method, params, timestamp())
        if method == 'thread/settings/updated':
            settings = params.get('threadSettings') or {}
            from .native_controls import REASONING_EFFORTS
            with self._binding_changed:
                if isinstance(settings.get('model'), str) and isinstance(settings.get('modelProvider'), str):
                    self._bindings[sid] = {**self._bindings.get(sid, {}), 'model': settings['model'], 'provider': settings['modelProvider']}
                effort = settings.get('effort')
                if effort in REASONING_EFFORTS:
                    self._efforts[sid] = effort
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
            failure_reason = codex_turn_failure_reason(turn, params) if method == 'turn/completed' and turn.get('status') == 'failed' else None
            if command:
                state = 'running' if method == 'turn/started' else 'failed' if turn.get('status') == 'failed' else 'cancelled' if turn.get('status') == 'interrupted' else 'completed'
                self.store.set_command_state(self.agent_id, sid, command['id'], state, failure_reason if state == 'failed' else None)
                self._event('command.upsert', sid, {**command, 'state': state, 'error': failure_reason if state == 'failed' else None})
            if method == 'turn/completed':
                for stop in self._stop_commands.pop((sid, turn_id), []):
                    self.store.set_command_state(self.agent_id, sid, stop['id'], 'completed', None)
                    self._event('command.upsert', sid, {**stop, 'state': 'completed', 'error': None})
                # Reconcile any older active commands for this session that finished
                for cmd in self.store.active_commands():
                    if cmd.get('agent_id') == self.agent_id and cmd.get('session_id') == sid and cmd.get('action') != 'enqueue':
                        self.store.set_command_state(self.agent_id, sid, cmd['id'], 'completed', None)
                        self._event('command.upsert', sid, {**cmd, 'state': 'completed', 'error': None})
                # Check if there is a queued command waiting for this session
                with self._lock:
                    queued_list = self._queued.get(sid) or []
                    next_command = queued_list.pop(0) if queued_list else None
                if next_command:
                    threading.Thread(target=self._submit, args=(next_command,), daemon=True).start()
        elif method in {'item/started', 'item/completed'}:
            item = params.get('item')
            if isinstance(item, dict) and isinstance(item.get('id'), str):
                message = self._message(item, sid, params.get('turnId'), timestamp())
                self._streams[(sid, item['id'])] = message
                self._event('message.upsert', sid, message)
        elif method and method.startswith('item/') and any(token in method for token in ('delta', 'Delta', 'output', 'Output')):
            item_id = params.get('itemId') or params.get('id')
            delta = params.get('delta') or params.get('textDelta') or params.get('output') or params.get('text')
            if not isinstance(item_id, str) or not isinstance(delta, str):
                return
            key = (sid, item_id)
            message = self._streams.get(key)
            if message is None:
                kind = 'reasoning' if 'reasoning' in method else 'commandExecution' if 'commandExecution' in method else 'plan' if 'plan' in method else 'agentMessage'
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
        for session in self.store.list_sessions(self.agent_id):
            sid = session['id']
            for task in self.store.list_tasks(self.agent_id, sid):
                if task['id'].startswith('codex:') and task['status'] in {'running', 'pending', 'waiting_approval'}:
                    self._event('task.upsert', sid, {**task, 'status': 'unknown', 'updated_at': timestamp()})
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

    def _reconcile_tasks(self):
        for sid in self._threads:
            if not any(task['status'] == 'unknown' and task['id'].startswith('codex:')
                       for task in self.store.list_tasks(self.agent_id, sid)):
                continue
            started = timestamp()
            entries = self._pages('thread/items/list', {'threadId': sid, 'limit': 200, 'sortDirection': 'desc'})
            try:
                project_task_history(self, sid, entries, started, refresh_unknown=True)
            except ConnectionError:
                logger.warning('Codex task reconciliation failed for %s; task state remains unknown', sid)

    def disconnect(self):
        with self._lifecycle:
            self._close_daemon_controller()
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
