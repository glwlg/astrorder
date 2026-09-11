import base64
import re
from pathlib import Path

from test_codex_connection import SID, FakeClient

from astrorder.config import Settings
from astrorder.environment_connections import RemoteCodex
from astrorder.events import EventHub
from astrorder.service import ControlService
from astrorder.store import Store


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
