from types import SimpleNamespace

import pytest

from astrorder.connections import ConnectionController, ConnectionError


def controller(rpc):
    instance = object.__new__(ConnectionController)
    instance.local = SimpleNamespace(_rpc=rpc)
    instance.get_runtime_by_agent_id = lambda agent: instance.local if agent == 'agent' else None
    return instance


def test_rename_uses_native_id_then_private_handle_and_reads_back():
    calls = []
    def rpc(method, params):
        calls.append((method, params))
        if method == 'session.resume':
            return {'result': {'session_id': 'private-handle'}}
        return {'result': {'title': 'New'}}
    controller(rpc).mutate_session_for_agent('agent', 'native-id', {'title': 'New'})
    assert calls == [
        ('session.resume', {'session_id': 'native-id', 'lazy': True}),
        ('session.title', {'session_id': 'private-handle', 'title': 'New'}),
        ('session.title', {'session_id': 'private-handle'}),
    ]


def test_delete_reads_back_native_catalog():
    calls = []
    def rpc(method, params):
        calls.append(method)
        if method == 'session.delete':
            assert params['session_id'] == 'native-id'
            return {'result': {'deleted': 'native-id'}}
        return {'result': {'sessions': [], 'total': 0}}
    controller(rpc).mutate_session_for_agent('agent', 'native-id', None)
    assert calls == ['session.delete', 'session.list']


def test_delete_clears_cache_only_after_native_absence_is_verified():
    def rpc(method, params):
        if method == 'session.delete':
            return {'error': {'code': 4007, 'message': 'session not found'}}
        assert method == 'session.list'
        return {'result': {'sessions': [], 'total': 0}}
    controller(rpc).mutate_session_for_agent('agent', 'native-id', None)


def test_delete_verifies_exact_id_when_native_list_has_no_paging_metadata():
    def rpc(method, params, **kwargs):
        if method == 'session.delete':
            return {'result': {'deleted': 'native-id'}}
        if method == 'session.list':
            return {'result': {'sessions': []}}
        assert method == 'session.resume'
        assert params['session_id'] == 'native-id'
        return {'error': {'code': 4007, 'message': 'session not found'}}
    controller(rpc).mutate_session_for_agent('agent', 'native-id', None)


def test_delete_closes_only_idle_target_handle_before_retry():
    calls = []
    def rpc(method, params):
        calls.append((method, params))
        if method == 'session.delete':
            if sum(m == method for m, _ in calls) == 1:
                return {'error': {'code': 4023, 'message': 'cannot delete an active session'}}
            return {'result': {'deleted': 'native-id'}}
        if method == 'session.active_list':
            return {'result': {'sessions': [
                {'id': 'target-handle', 'session_key': 'native-id', 'status': 'idle'},
                {'id': 'other-handle', 'session_key': 'other', 'status': 'working'},
            ]}}
        if method == 'session.close':
            assert params == {'session_id': 'target-handle'}
            return {'result': {'closed': True}}
        return {'result': {'sessions': [], 'total': 0}}
    controller(rpc).mutate_session_for_agent('agent', 'native-id', None)
    assert [method for method, _ in calls] == ['session.delete', 'session.active_list', 'session.close', 'session.delete', 'session.list']


def test_delete_never_closes_working_session():
    def rpc(method, params):
        if method == 'session.delete':
            return {'error': {'code': 4023}}
        assert method == 'session.active_list'
        return {'result': {'sessions': [{'id': 'handle', 'session_key': 'native-id', 'status': 'working'}]}}
    with pytest.raises(ConnectionError, match='正在运行'):
        controller(rpc).mutate_session_for_agent('agent', 'native-id', None)


def test_mutation_cannot_fall_back_to_another_source():
    with pytest.raises(ConnectionError):
        controller(lambda *_: pytest.fail('must not call other source')).mutate_session_for_agent('other', 'native-id', None)


def test_rename_rejects_unverified_native_result():
    def rpc(method, params):
        return {'result': {'session_id': 'handle', 'title': 'Old'}}
    with pytest.raises(ConnectionError):
        controller(rpc).mutate_session_for_agent('agent', 'native-id', {'title': 'New'})
