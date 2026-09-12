"""Protocol contract tests only; no native runtime or user session is used."""
from unittest.mock import Mock

from astrorder.native_codex import CodexConnection


def make_connection():
    connection = object.__new__(CodexConnection)
    connection._daemon_controller = None
    connection.validate_workspace = Mock(return_value='workspace')
    connection._scope = Mock()
    connection._request = Mock(return_value={'thread': {'id': 'child'}})
    connection._record_thread = Mock(return_value={'id': 'child'})
    connection.mutate = Mock()
    return connection


def test_sidechat_forks_parent_with_no_history_in_response():
    connection = make_connection()
    result = connection.create('workspace', None, ephemeral=True, parent_session_id='parent')
    connection._scope.assert_called_once_with('parent')
    connection._request.assert_called_once_with('thread/fork', {
        'threadId': 'parent', 'cwd': 'workspace', 'ephemeral': True,
        'excludeTurns': True, 'deferGoalContinuation': True,
    })
    assert result == {'id': 'child', 'ephemeral': True}


def test_normal_creation_still_starts_a_persistent_thread():
    connection = make_connection()
    result = connection.create('workspace', None)
    connection._scope.assert_not_called()
    connection._request.assert_called_once_with('thread/start', {
        'cwd': 'workspace', 'ephemeral': False, 'persistExtendedHistory': True,
    })
    assert result == {'id': 'child'}


def test_codex_notification_preserves_ephemeral_before_broadcast():
    connection = object.__new__(CodexConnection)
    connection.agent_id = 'codex-fixture'
    connection._threads = {}
    connection.service = Mock()
    connection._record_thread({
        'id': 'child', 'ephemeral': True, 'status': {'type': 'idle'},
        'updatedAt': 1,
    })
    published = connection.service.record_native_sessions.call_args.args[0][0]
    assert published['ephemeral'] is True
