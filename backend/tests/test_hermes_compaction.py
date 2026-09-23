from unittest.mock import Mock

import pytest

from astrorder.adapters.hermes.compaction import compaction_update
from astrorder.daemon.hermes_compaction_projection import HermesCompactionFrameRouter
from astrorder.daemon.bridge import DaemonBridgeError
from astrorder.connections import LocalHermesController


def event(kind):
    return {'type': 'status.update', 'payload': {'kind': kind}}


def test_compaction_updates_one_record_and_only_confirms_explicit_completion():
    start = compaction_update(event('compacting'), None)
    assert start['state'] == 'running'
    progress = compaction_update(event('compressing'), start)
    assert progress == start
    assert compaction_update(event('ready'), start) is None
    assert compaction_update(event('compacted'), start) == {**start, 'state': 'completed'}
    assert compaction_update({'type': 'error', 'payload': {}}, start)['state'] == 'failed'
    assert compaction_update({'type': 'message.complete', 'payload': {}}, start)['state'] == 'unknown'
    assert compaction_update({'type': 'error', 'payload': {}}, None) is None


def test_compaction_projection_replays_exact_message_without_ending_turn():
    store, service, bridge = Mock(), Mock(), Mock()
    store.get_session.return_value = {'id': 'session'}
    store.upsert_message.side_effect = lambda data: data
    router = HermesCompactionFrameRouter(bridge, store, service)
    update = compaction_update(event('compacting'), None)
    for state in ('running', 'completed', 'failed', 'unknown'):
        router.project('session', {**update, 'state': state, 'agent_id': 'agent', 'session_id': 'session'})
    assert {call.args[0]['id'] for call in store.upsert_message.call_args_list} == {update['id']}
    assert service._server_event.call_count == 4
    store.update_session.assert_not_called()
    store.set_command_state.assert_not_called()
    with pytest.raises(DaemonBridgeError):
        router.project('other-session', {**update, 'agent_id': 'agent', 'session_id': 'session'})


def test_native_callback_keeps_sessions_isolated_and_survives_delivery_error():
    controller = LocalHermesController.__new__(LocalHermesController)
    controller._agent_id = 'agent'
    controller._tui_to_session = {'handle-a': 'a', 'handle-b': 'b'}
    controller._compactions = {}
    callback = Mock()
    controller.set_compaction_callback(callback)
    for handle in ('handle-a', 'handle-b'):
        controller._on_compaction_event({**event('compacting'), 'session_id': handle})
    a, b = callback.call_args_list
    assert a.args[:2] == ('agent', 'a')
    assert b.args[:2] == ('agent', 'b')
    assert a.args[2]['id'] != b.args[2]['id']
    controller._on_compaction_event({**event('compacted'), 'session_id': 'handle-a'})
    assert callback.call_args.args[2]['id'] == a.args[2]['id']
    assert 'b' in controller._compactions and 'a' not in controller._compactions
    controller._on_compaction_event({**event('compacting'), 'session_id': 'unknown'})
    assert callback.call_count == 3
    callback.side_effect = RuntimeError('transport stopped')
    controller._on_compaction_event({**event('compacted'), 'session_id': 'handle-b'})
