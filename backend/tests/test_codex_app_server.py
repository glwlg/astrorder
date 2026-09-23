import io
import json

from astrorder_codex_connector.app_server import CodexAppServer, CodexRpcRejected
from astrorder_codex_connector.config import CodexConnectorConfig


class FakeProcess:
    def __init__(self):
        self.stdin = io.StringIO()
        self.stdout = io.StringIO()

    def poll(self):
        return 0


def test_app_server_passes_child_environment_and_writes_bootstrap_before_protocol(monkeypatch, tmp_path):
    captured = {}
    process = FakeProcess()

    def popen(argv, **kwargs):
        captured['argv'] = argv
        captured['kwargs'] = kwargs
        return process

    monkeypatch.setattr('astrorder_codex_connector.app_server.subprocess.Popen', popen)
    monkeypatch.setattr(
        'astrorder_codex_connector.app_server._assign_kill_on_close_job', lambda _process: None
    )
    environment = {'SYSTEM_ONLY': 'machine', 'USER_ONLY': 'user', 'PROCESS_ONLY': 'process'}
    bootstrap = {'environment': environment}
    config = CodexConnectorConfig(
        endpoint='',
        secret='',
        agent_id='agent',
        agent_name='Codex',
        executable='codex',
        workspace=tmp_path,
        allowed_workspaces=(),
    )

    server = CodexAppServer(
        config,
        lambda _frame: None,
        environment=environment,
        bootstrap_stdin=bootstrap,
        launch_argv=['ssh', 'remote', 'codex'],
    )
    server.start()

    assert captured['kwargs']['env'] == environment
    assert captured['kwargs']['cwd'] is None
    assert json.loads(process.stdin.getvalue()) == bootstrap


def test_stop_closes_the_windows_job_that_owns_the_process_tree(monkeypatch, tmp_path):
    class Job:
        closed = False

        def Close(self):
            self.closed = True

    job = Job()
    process = FakeProcess()
    monkeypatch.setattr('astrorder_codex_connector.app_server.subprocess.Popen', lambda *_a, **_k: process)
    monkeypatch.setattr(
        'astrorder_codex_connector.app_server._assign_kill_on_close_job', lambda _process: job
    )
    config = CodexConnectorConfig('', '', 'agent', 'Codex', 'codex', tmp_path, ())
    server = CodexAppServer(config, lambda _frame: None)

    server.start()
    server.stop()

    assert job.closed is True


def test_rpc_rejection_preserves_native_reason_without_exposing_credential_value():
    error = CodexRpcRejected({
        'code': -32000,
        'message': 'Missing environment variable: OPENCODEX_API_AUTH_TOKEN.',
    })

    assert 'Missing environment variable: OPENCODEX_API_AUTH_TOKEN.' in str(error)
    assert str(CodexRpcRejected({'code': -32603, 'message': 'Internal error'}, 'Grok')) == (
        'Grok rejected request (-32603; Internal error)'
    )


def test_transport_close_error_keeps_sanitized_remote_stderr(tmp_path):
    config = CodexConnectorConfig(
        endpoint='',
        secret='',
        agent_id='agent',
        agent_name='Codex',
        executable='codex',
        workspace=tmp_path,
        allowed_workspaces=(),
    )
    server = CodexAppServer(config, lambda _frame: None)
    server._stderr_tail = 'remote bootstrap failed: token=do-not-disclose'

    detail = str(server._transport_closed_error())

    assert 'remote bootstrap failed' in detail
    assert 'do-not-disclose' not in detail
    assert 'token=[REDACTED]' in detail


def test_request_timeout_keeps_sanitized_remote_stderr(tmp_path):
    config = CodexConnectorConfig(
        endpoint='',
        secret='',
        agent_id='agent',
        agent_name='Codex',
        executable='codex',
        workspace=tmp_path,
        allowed_workspaces=(),
    )
    server = CodexAppServer(config, lambda _frame: None)
    server._stderr_tail = 'remote bootstrap failed: token=do-not-disclose'

    detail = str(server._request_timeout_error('initialize'))

    assert 'initialize' in detail
    assert 'remote bootstrap failed' in detail
    assert 'do-not-disclose' not in detail
