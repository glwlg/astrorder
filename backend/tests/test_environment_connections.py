import base64
import re
from pathlib import Path
from types import SimpleNamespace

from test_codex_connection import SID, THREAD, FakeClient

from astrorder.config import Settings
from astrorder.environment_connections import EnvironmentConnections, RemoteCodex
from astrorder.events import EventHub
from astrorder.service import ControlService
from astrorder.store import Store


def test_local_codex_environment_exposes_explicit_daemon_ownership_mode(tmp_path):
    settings = Settings(database_url=f'sqlite:///{tmp_path}/environment-mode.db', auto_connect_local_hermes=False)
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    hermes = SimpleNamespace(
        snapshot=lambda _service: {
            'local': {'available': True, 'state': 'discovered', 'agent_id': None, 'detail': 'fixture'},
            'ssh': {'items': []},
        }
    )
    codex = SimpleNamespace(
        snapshot=lambda: {
            'available': True,
            'state': 'connected',
            'agent_id': 'local-codex',
            'detail': 'fixture',
            'daemon_mode': True,
        }
    )
    try:
        snapshot = EnvironmentConnections(settings, store, service, hermes, codex).snapshot()
        assert snapshot['items'][0]['agents'][1]['daemon_mode'] is True
    finally:
        store.close()


def test_local_hermes_environment_exposes_explicit_daemon_ownership_mode(tmp_path):
    settings = Settings(database_url=f'sqlite:///{tmp_path}/environment-hermes-mode.db', auto_connect_local_hermes=False)
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    hermes = SimpleNamespace(
        snapshot=lambda _service: {
            'local': {
                'available': True,
                'state': 'connecting',
                'agent_id': 'local-hermes-default',
                'detail': 'fixture',
                'daemon_mode': True,
            },
            'ssh': {'items': []},
        }
    )
    codex = SimpleNamespace(
        snapshot=lambda: {
            'available': False,
            'state': 'disconnected',
            'agent_id': 'local-codex',
            'detail': 'fixture',
            'daemon_mode': False,
        }
    )
    try:
        snapshot = EnvironmentConnections(settings, store, service, hermes, codex).snapshot()
        assert snapshot['items'][0]['agents'][0]['daemon_mode'] is True
    finally:
        store.close()


def test_remote_hermes_environment_exposes_explicit_daemon_ownership_mode(tmp_path):
    settings = Settings(database_url=f'sqlite:///{tmp_path}/environment-ssh-mode.db', auto_connect_local_hermes=False)
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    remote = store.save_ssh_connection(
        {'host': 'remote.example', 'port': 22, 'user': 'operator'},
        state='connecting',
        detail='fixture',
    )
    connection_id = remote['id']
    hermes = SimpleNamespace(
        snapshot=lambda _service: {
            'local': {'available': False, 'state': 'offline', 'agent_id': None, 'detail': 'fixture'},
            'ssh': {
                'items': [
                    {
                        'id': connection_id,
                        'state': 'connecting',
                        'agent_id': f'ssh-hermes-{connection_id}',
                        'detail': 'fixture',
                        'daemon_mode': True,
                    }
                ]
            },
        }
    )
    codex = SimpleNamespace(
        snapshot=lambda: {
            'available': False,
            'state': 'disconnected',
            'agent_id': 'local-codex',
            'detail': 'fixture',
            'daemon_mode': False,
        }
    )
    try:
        snapshot = EnvironmentConnections(settings, store, service, hermes, codex).snapshot()
        remote_hermes = next(
            agent
            for environment in snapshot['items']
            if environment['id'] == connection_id
            for agent in environment['agents']
            if agent['kind'] == 'hermes'
        )
        assert remote_hermes['daemon_mode'] is True
    finally:
        store.close()


def test_remote_codex_keeps_connection_native_identity_and_never_reads_local_model(tmp_path):
    settings = Settings(database_url=f'sqlite:///{tmp_path}/cache.db', auto_connect_local_hermes=False)
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    row = {'id': 'ssh-test', 'display_name': 'WSL', 'settings': {'host': 'fixture', 'port': 22}}
    connection = RemoteCodex(settings, store, service, row, '/usr/bin/codex')
    connection.client_factory = FakeClient
    try:
        assert connection.connect()['state'] == 'connected'
        assert store.get_session(connection.agent_id, SID)['connection_id'] == 'ssh-test'
        assert connection.agent_id == 'ssh-codex-ssh-test'
        connection.remote_json = lambda source: {'model': 'remote-model', 'provider': 'remote-provider'}
        connection._home = Path('/remote/home')
        assert connection.model(SID)['model'] == 'remote-model'
        assert not any(method == 'thread/resume' for method, _ in connection.client.calls)
        assert 'StrictHostKeyChecking=yes' in connection.ssh_argv()
        assert 'BatchMode=yes' in connection.ssh_argv()
        assert connection.ssh_argv()[-1] == 'fixture'
        assert '--' not in connection.ssh_argv()
    finally:
        connection.disconnect()
        store.close()


def test_remote_codex_effort_resumes_existing_rollout_path_when_thread_id_lookup_fails(tmp_path):
    rollout_path = '/home/luwei/.codex/sessions/2026/09/10/rollout-fixture.jsonl'

    class PathResumeClient(FakeClient):
        def request(self, method, params, timeout=30):
            if method == 'thread/resume':
                self.calls.append((method, params))
                if params.get('path') == rollout_path:
                    return {'thread': THREAD, 'model': self.model, 'modelProvider': 'native-provider'}
                from astrorder_codex_connector.app_server import CodexRpcRejected
                raise CodexRpcRejected({'code': -32600, 'message': f'thread not found: {params["threadId"]}'})
            if method == 'thread/settings/update':
                self.calls.append((method, params))
                assert params == {'threadId': SID, 'effort': 'high'}
                self.notify({
                    'method': 'thread/settings/updated',
                    'params': {'threadId': SID, 'threadSettings': {'effort': 'high'}},
                })
                return {}
            return super().request(method, params, timeout)

    settings = Settings(database_url=f'sqlite:///{tmp_path}/rollout-resume.db', auto_connect_local_hermes=False)
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    row = {'id': 'ssh-test', 'display_name': 'WSL', 'settings': {'host': 'fixture', 'port': 22}}
    connection = RemoteCodex(settings, store, service, row, '/usr/bin/codex')
    connection.client_factory = PathResumeClient
    connection.remote_json = lambda _source: rollout_path
    try:
        connection.connect()
        assert connection.set_effort(SID, 'high') == {'effort': 'high'}
        resume_calls = [params for method, params in connection.client.calls if method == 'thread/resume']
        assert resume_calls == [
            {'threadId': SID, 'excludeTurns': True},
            {'threadId': SID, 'path': rollout_path, 'excludeTurns': True},
        ]
    finally:
        connection.disconnect()
        store.close()


def test_remote_codex_forwards_all_system_environment_via_stdin_without_command_line_values(tmp_path, monkeypatch):
    captured = {}

    class CapturingAppServer:
        def __init__(self, config, on_notification, **kwargs):
            captured['config'] = config
            captured['kwargs'] = kwargs

    monkeypatch.setattr('astrorder.environment_connections.CodexAppServer', CapturingAppServer)
    settings = Settings(database_url=f'sqlite:///{tmp_path}/remote-environment.db', auto_connect_local_hermes=False)
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    row = {'id': 'ssh-test', 'display_name': 'WSL', 'settings': {'host': 'fixture', 'port': 22}}
    connection = RemoteCodex(settings, store, service, row, '/usr/bin/codex')
    environment = {'MACHINE_ONLY': 'machine', 'USER_ONLY': 'user', 'PROCESS_ONLY': 'process'}
    try:
        connection._client(object(), lambda _frame: None, environment=environment)
        assert captured['kwargs']['environment'] == environment
        assert captured['kwargs']['bootstrap_stdin'] == {'environment': environment}
        command = ' '.join(captured['kwargs']['launch_argv'])
        assert all(value not in command for value in environment.values())
    finally:
        connection.disconnect()
        store.close()


def test_remote_codex_bootstrap_leaves_json_rpc_bytes_after_environment_payload(tmp_path, monkeypatch):
    captured = {}

    class CapturingAppServer:
        def __init__(self, _config, _on_notification, **kwargs):
            captured.update(kwargs)

    monkeypatch.setattr('astrorder.environment_connections.CodexAppServer', CapturingAppServer)
    settings = Settings(database_url=f'sqlite:///{tmp_path}/remote-bootstrap.db', auto_connect_local_hermes=False)
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    connection = RemoteCodex(
        settings,
        store,
        service,
        {'id': 'ssh-test', 'display_name': 'WSL', 'settings': {'host': 'fixture', 'port': 22}},
        '/usr/bin/codex',
    )
    try:
        connection._client(object(), lambda _frame: None, environment={})
        command = captured['launch_argv'][-1]
        source = None
        for token in re.findall(r'[A-Za-z0-9+/]{20,}={0,2}', command):
            try:
                decoded = base64.b64decode(token, validate=True)
            except ValueError:
                continue
            if b'os.execv(p' in decoded:
                source = decoded.decode('utf-8')
                break
        assert source is not None
        assert 'os.read(0, 1)' in source
        assert 'sys.stdin.readline()' not in source
    finally:
        connection.disconnect()
        store.close()


def test_remote_codex_environment_uses_daemon_controller_factory_when_enabled(tmp_path, monkeypatch):
    settings = Settings(database_url=f"sqlite:///{tmp_path}/remote-daemon-codex.db", auto_connect_local_hermes=False)
    store = Store(settings)
    service = ControlService(store, EventHub(), settings)
    remote = store.save_ssh_connection(
        {"display_name": "Debian", "host": "debian.example", "port": 22, "user": "operator"},
        state="disconnected",
        detail="fixture",
    )

    class Controller:
        def activate(self):
            return None

        async def submit(self, _command):
            return "accepted", None

        def close(self):
            return None

    hermes = SimpleNamespace(
        snapshot=lambda _service: {
            "local": {"available": False, "state": "offline", "agent_id": None, "detail": "fixture"},
            "ssh": {"items": []},
        }
    )
    local_codex = SimpleNamespace(
        snapshot=lambda: {
            "available": False,
            "state": "disconnected",
            "agent_id": "local-codex",
            "detail": "fixture",
            "daemon_mode": True,
        }
    )
    monkeypatch.setattr(
        RemoteCodex,
        "_client",
        lambda _self, config, on_notification, **kwargs: FakeClient(config, on_notification, **kwargs),
    )
    monkeypatch.setattr(
        RemoteCodex,
        "remote_json",
        lambda _self, _source: {
            "os": "Linux",
            "items": [
                {"kind": "hermes", "available": False, "executable": None},
                {"kind": "codex", "available": True, "executable": "/home/operator/.local/bin/codex"},
            ],
        },
    )
    environments = EnvironmentConnections(
        settings,
        store,
        service,
        hermes,
        local_codex,
        daemon_codex_controller_factory=lambda _connection: Controller(),
    )
    environments.discovered[remote["id"]] = {
        "os": "Linux",
        "items": [
            {"kind": "hermes", "available": False, "executable": None},
            {"kind": "codex", "available": True, "executable": "/home/operator/.local/bin/codex"},
        ],
    }
    try:
        environments.change(remote["id"], "codex", True)
        codex = next(
            agent
            for environment in environments.snapshot()["items"]
            if environment["id"] == remote["id"]
            for agent in environment["agents"]
            if agent["kind"] == "codex"
        )
        assert codex["daemon_mode"] is True
        runtime = environments.remote[remote["id"]]
        assert runtime.ssh_settings["display_name"] == "Debian"
    finally:
        environments.shutdown()
        store.close()
