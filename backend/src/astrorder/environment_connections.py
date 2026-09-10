"""Connection-first Agent discovery and owned remote Codex stdio transport."""
from __future__ import annotations

import inspect
import json
import subprocess
from pathlib import Path
from threading import RLock

from astrorder_codex_connector.app_server import CodexAppServer

from .connections import ConnectionError, validate_ssh_settings
from .models import AgentConnectionChoice
from .native_codex import CodexConnection, stored_model
from .ssh_transport import SshNativeRuntime, build_remote_python_command

DISCOVERY = r'''
import json, os, platform, shutil
from pathlib import Path
items = []
for kind in ('hermes', 'codex'):
    executable = shutil.which(kind)
    if not executable:
        candidates = [Path.home()/'.local/bin'/kind, Path.home()/'bin'/kind]
        if kind == 'codex':
            candidates += sorted((Path.home()/'.nvm/versions/node').glob('*/bin/codex'), reverse=True)
        executable = next((str(p) for p in candidates if p.is_file() and os.access(p, os.X_OK)), None)
    items.append({'kind': kind, 'available': bool(executable), 'executable': executable})
print(json.dumps({'os': platform.system(), 'items': items}))
'''

class RemoteCodex(CodexConnection):
    def __init__(self, settings, store, service, row, executable):
        self.connection_id = row['id']
        self.display_name = row.get('display_name') or self.connection_id
        self.agent_id = 'ssh-codex-' + self.connection_id
        self.remote_executable = executable
        self.ssh_settings = validate_ssh_settings(row['settings'])
        self.transport = SshNativeRuntime(self.ssh_settings, self.connection_id, 0, Path.cwd(), Path.cwd(), connector_secret=None)
        super().__init__(settings, store, service, client_factory=self._client)

    def ssh_argv(self):
        return ['-o' if part == '-o' else 'StrictHostKeyChecking=yes' if part == 'StrictHostKeyChecking=ask' else part for part in self.transport._base_ssh_argv()] + ['--', self.transport._target()]

    def remote_json(self, source):
        try:
            loader = 'import sys\nexec(compile(sys.stdin.read(), "<astrorder-remote>", "exec"))'
            result = subprocess.run(self.ssh_argv() + [build_remote_python_command(loader)], input=source, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, encoding='utf-8', timeout=30, check=False, creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0))
            if result.returncode:
                raise ConnectionError('SSH 探测失败；请检查连接、主机指纹及远端 Python。', 502)
            return json.loads(result.stdout)
        except (OSError, subprocess.TimeoutExpired, ValueError):
            raise ConnectionError('远程探测未确认，请检查 SSH 连接与远端 Python。', 502) from None

    def _executable(self):
        return self.remote_executable

    def _client(self, config, on_notification, **kwargs):
        source = 'import os\nfrom pathlib import Path\np=' + repr(self.remote_executable) + '\nos.environ["PATH"]=str(Path(p).parent)+os.pathsep+os.environ.get("PATH", "")\nos.execv(p,[p,"app-server","--listen","stdio://"])\n'
        return CodexAppServer(config, on_notification, **kwargs, launch_argv=self.ssh_argv() + [build_remote_python_command(source)])

    def open_ids(self):
        if not self._home:
            return []
        source = 'import sqlite3,json\\nfrom pathlib import Path\\n' + inspect.getsource(stored_model) + '\\np=Path(' + repr(self._home.as_posix()) + ')\\ndb_path=p/\"state.db\"\\nres=[]\\nif db_path.is_file():\\n    with sqlite3.connect(db_path) as db:\\n        for r in db.execute(\"SELECT id FROM threads WHERE archived=0 ORDER BY updated_at DESC LIMIT 50\"):\\n            res.append(r[0])\\nprint(json.dumps(res))'
        try:
            res = self.remote_json(source)
            return res if isinstance(res, list) else []
        except Exception:
            return []

    def model(self, sid):
        self._scope(sid)
        if sid in self._owned_threads and sid in self._bindings:
            return dict(self._bindings[sid])
        if not self._home:
            raise ConnectionError('远程 Codex 未报告数据目录，无法只读获取模型。', 502)
        source = 'import sqlite3,re,json\nfrom pathlib import Path\n' + inspect.getsource(stored_model) + '\nprint(json.dumps(stored_model(Path(' + repr(self._home.as_posix()) + '), ' + repr(sid) + ')))'
        binding = self.remote_json(source)
        if not binding or not binding.get('model'):
            raise ConnectionError('远程原生记录未提供模型绑定。', 502)
        return binding

    def validate_workspace(self, workspace):
        source = 'import json\nfrom pathlib import Path\np=Path(' + repr(workspace or '~') + ').expanduser().resolve()\nprint(json.dumps({"path":str(p),"exists":p.is_dir()}))'
        result = self.remote_json(source)
        if not result.get('exists'):
            raise ConnectionError('远程工作区不存在。', 422)
        return result['path']

class EnvironmentConnections:
    def __init__(self, settings, store, service, hermes, codex):
        self.settings, self.store, self.service = settings, store, service
        self.hermes, self.codex = hermes, codex
        self.remote = {}
        self.discovered = {}
        self.lock = RLock()

    def _row(self, connection_id):
        row = self.store.get_ssh_connection(connection_id)
        if row is None:
            raise ConnectionError('SSH 连接不存在。', 404)
        return row

    def discover(self, connection_id):
        if connection_id == 'local':
            return self.snapshot()['items'][0]
        row = self._row(connection_id)
        probe = RemoteCodex(self.settings, self.store, self.service, row, '')
        result = probe.remote_json(DISCOVERY)
        if not isinstance(result.get('items'), list):
            raise ConnectionError('远程 Agent 发现响应无效。', 502)
        self.discovered[connection_id] = result
        return next(item for item in self.snapshot()['items'] if item['id'] == connection_id)

    def snapshot(self):
        local = self.hermes.snapshot(self.service)['local']
        c = self.codex.snapshot()
        items = [{'id': 'local', 'name': '本机', 'method': 'local', 'discovered': True, 'agents': [
            {'kind': 'hermes', 'available': local['available'], 'state': local['state'], 'agent_id': local.get('agent_id'), 'detail': local['detail']},
            {'kind': 'codex', 'available': c['available'], 'state': c['state'], 'agent_id': c['agent_id'], 'detail': c['detail']},
        ]}]
        ssh_states = {row['id']: row for row in self.hermes.snapshot(self.service)['ssh']['items']}
        for row in self.store.list_ssh_connections():
            cid = row['id']
            discovered = self.discovered.get(cid)
            entries = []
            for kind in ('hermes', 'codex'):
                found = next((x for x in (discovered or {}).get('items', []) if x['kind'] == kind), {})
                active = self.remote[cid].snapshot() if kind == 'codex' and cid in self.remote else ssh_states.get(cid, {}) if kind == 'hermes' else {}
                entries.append({'kind': kind, 'available': found.get('available', active.get('state') == 'connected'), 'state': active.get('state', 'disconnected'), 'agent_id': active.get('agent_id'), 'detail': active.get('detail', ''), 'executable': found.get('executable')})
            items.append({'id': cid, 'name': row.get('display_name') or cid, 'method': 'ssh', 'discovered': bool(discovered), 'os': (discovered or {}).get('os'), 'agents': entries})
        return {'items': items}

    def change(self, cid, kind, connect):
        if kind not in {'hermes', 'codex'}:
            raise ConnectionError('不支持的 Agent 类型。', 422)
        with self.lock:
            if cid == 'local':
                if kind == 'codex':
                    (self.codex.connect if connect else self.codex.disconnect)()
                else:
                    (self.hermes.connect_local if connect else self.hermes.disconnect_local)(self.service)
            elif kind == 'hermes':
                self._row(cid)
                (self.hermes.connect_ssh if connect else self.hermes.disconnect_ssh)(self.service, cid)
            elif connect:
                if cid not in self.remote:
                    self.discover(cid)
                    found = next(x for x in self.discovered[cid]['items'] if x['kind'] == 'codex')
                    if not found['available']:
                        raise ConnectionError('该 SSH 环境未发现 Codex。', 404)
                    self.remote[cid] = RemoteCodex(self.settings, self.store, self.service, self._row(cid), found['executable'])
                self.remote[cid].connect()
            elif cid in self.remote:
                self.remote[cid].disconnect()
            with self.store.session() as db:
                key = cid + ':' + kind
                choice = db.get(AgentConnectionChoice, key)
                if choice is None:
                    db.add(AgentConnectionChoice(id=key, enabled=int(connect)))
                else:
                    choice.enabled = int(connect)
            return self.snapshot()

    def restore(self):
        for row in self.store.list_ssh_connections():
            try:
                self.discover(row['id'])
            except ConnectionError:
                continue
        pairs = [('local', 'hermes'), ('local', 'codex')]
        pairs += [(row['id'], kind) for row in self.store.list_ssh_connections() for kind in ('hermes', 'codex')]
        for cid, kind in pairs:
            with self.store.session() as db:
                choice = db.get(AgentConnectionChoice, cid + ':' + kind)
                legacy_id = ('local-hermes-default' if kind == 'hermes' else 'local-codex') if cid == 'local' else f'ssh-{kind}-{cid}'
                enabled = bool(choice.enabled) if choice is not None else self.store.get_agent(legacy_id) is not None
            if enabled:
                try:
                    self.change(cid, kind, True)
                except ConnectionError:
                    continue

    def for_agent(self, agent_id):
        return next((c for c in [self.codex, *self.remote.values()] if c.agent_id == agent_id), None)

    def shutdown(self):
        for connection in self.remote.values():
            connection.disconnect()
