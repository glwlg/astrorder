"""Inert RPC regression: stopping must never submit an empty prompt."""
import threading
from types import SimpleNamespace

import pytest

from astrorder.connections import LocalHermesController
from astrorder.ssh_transport import SshNativeRuntime


@pytest.mark.parametrize('remote', [False, True])
def test_stop_targets_only_exact_native_session_without_resuming(remote):
    cls = SshNativeRuntime if remote else LocalHermesController
    runtime = object.__new__(cls)
    runtime._lock = threading.RLock()
    runtime._process = SimpleNamespace(poll=lambda: None)
    runtime._state = 'connected'
    runtime._runtime_session_id = 'unrelated-native'
    runtime._tui_session_id = 'unrelated-handle'
    calls = []

    def rpc(method, params, **kwargs):
        calls.append((method, params))
        if method == 'session.active_list':
            return {'result': {'sessions': [
                {'id': 'unrelated-handle', 'session_key': 'unrelated-native', 'status': 'working'},
                {'id': 'target-handle', 'session_key': 'target-native', 'status': 'working'},
            ]}}
        if method == 'session.interrupt':
            return {'result': {'status': 'interrupted'}}
        return {'error': {'code': 4007}}

    if remote:
        runtime.rpc = rpc
        submit = runtime.submit
    else:
        runtime._rpc = rpc
        submit = runtime.submit_tui_command
    outcome = submit({'id': 'stop-id', 'session_id': 'target-native', 'action': 'stop', 'text': '', 'target_id': 'target-native'})
    assert outcome == ('accepted', None)
    assert calls == [
        ('session.active_list', {}),
        ('session.interrupt', {'session_id': 'target-handle'}),
    ]
