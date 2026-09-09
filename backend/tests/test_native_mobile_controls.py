import pytest
from astrorder.connections import ConnectionError
from astrorder.native_controls import model_choices, set_session_model, open_native_session_ids, current_session_model


def test_current_model_comes_from_each_native_session_not_global_catalog():
    def rpc(method, params):
        assert method == 'session.resume'
        assert params['lazy'] is True
        return {'result': {'session_id': 'private', 'info': {'model': 'model-' + params['session_id'], 'provider': 'native-provider', 'cwd': '/private/not-returned'}}}
    assert current_session_model(rpc, 'a') == {'model': 'model-a', 'provider': 'native-provider'}
    assert current_session_model(rpc, 'b') == {'model': 'model-b', 'provider': 'native-provider'}


def test_missing_native_model_is_not_replaced_by_a_global_default():
    def rpc(method, params):
        if method == 'session.resume': return {'result': {'session_id': 'private', 'info': {}}}
        assert method == 'session.status'
        return {'result': {'output': 'Model: (unknown) (unknown)'}}
    with pytest.raises(ConnectionError):
        current_session_model(rpc, 'native')


def test_model_metadata_does_not_request_transcript_and_preserves_native_branch():
    def rpc(method, params):
        assert method == 'session.resume'
        assert params['omit_messages'] is True
        assert params['defer_history'] is True
        return {'result': {'session_id': 'handle', 'info': {'model': 'm', 'provider': 'p', 'branch': 'feature/native', 'system_prompt': 'must not expose'}}}
    assert current_session_model(rpc, 'native') == {'model': 'm', 'provider': 'p', 'branch': 'feature/native'}


def test_open_sessions_use_native_keys_and_include_idle_handles_without_resuming():
    def rpc(method, params, timeout):
        assert method == 'session.active_list'
        assert timeout == 3
        return {'result': {'sessions': [{'id': 'private-1', 'session_key': 'native-1', 'status': 'idle'}, {'id': 'private-2', 'session_key': 'native-2', 'status': 'working'}, {'id': 'unpersisted'}]}}
    assert open_native_session_ids(rpc) == ['native-1', 'native-2']


def test_running_model_change_verifies_pending_selection_instead_of_old_live_model():
    changed = False
    def rpc(method, params):
        nonlocal changed
        if method == 'model.options': return {'result': {'providers': [{'slug': 'p', 'models': ['m']}]}}
        if method == 'session.resume': return {'result': {'session_id': 'handle', 'info': {'model': 'm' if changed else 'old', 'provider': 'p'}}}
        if method == 'config.set':
            changed = True
            return {'result': {'deferred': True, 'value': 'm', 'confirm_required': False}}
        return {'result': {'output': 'Model: old (p)'}}
    assert set_session_model(rpc, 'native', 'p', 'm') == {'provider': 'p', 'model': 'm', 'deferred': True}


def test_model_catalog_filters_unconfigured_and_strips_extra_fields():
    def rpc(method, params):
        assert method == 'model.options'
        return {'result': {'providers': [
            {'slug': 'configured', 'name': 'Configured', 'models': ['m', {'id': 'n'}], 'extra': 'never returned'},
            {'slug': 'unconfigured', 'authenticated': False, 'models': ['x']},
        ]}}
    assert model_choices(rpc) == [
        {'provider': 'configured', 'model': 'm', 'label': 'Configured · m'},
        {'provider': 'configured', 'model': 'n', 'label': 'Configured · n'},
    ]


def test_model_readback_uses_native_selection_metadata_when_agent_status_is_still_old():
    def rpc(method, params):
        if method == 'model.options': return {'result': {'providers': [{'slug': 'p', 'models': ['m']}]}}
        if method == 'session.resume': return {'result': {'session_id': 'handle', 'info': {'provider': 'p', 'model': 'm'}}}
        if method == 'config.set': return {'result': {'value': 'm'}}
        return {'result': {'output': 'Model: old (p)'}}
    assert set_session_model(rpc, 'native', 'p', 'm') == {'provider': 'p', 'model': 'm'}


@pytest.mark.parametrize('deferred', [False, True])
def test_model_switch_never_claims_success_for_unconfirmed_native_selection(deferred):
    def rpc(method, params):
        if method == 'model.options': return {'result': {'providers': [{'slug': 'p', 'models': ['m']}]}}
        if method == 'session.resume': return {'result': {'session_id': 'handle', 'info': {'model': 'old', 'provider': 'p'}}}
        if method == 'config.set': return {'result': {'deferred': deferred}}
        return {'result': {'output': 'Model: old (p)'}}
    with pytest.raises(ConnectionError, match='读回确认'):
        set_session_model(rpc, 'native', 'p', 'm')


def test_open_sessions_http_is_private_and_keeps_native_ids(tmp_path):
    from types import SimpleNamespace
    from fastapi.testclient import TestClient
    from astrorder.main import create_app
    from astrorder.config import Settings
    app = create_app(Settings(database_url=f'sqlite:///{tmp_path}/open.db', attachments_dir=tmp_path / 'attachments', browser_secret='test-only', auto_connect_local_hermes=False))
    with TestClient(app) as client:
        app.state.store.upsert_agent(dict(id='source', kind='hermes', name='inert', status='ready', capabilities=[], limitation=None))
        app.state.connections.get_runtime_by_agent_id = lambda aid: SimpleNamespace(rpc=lambda *args, **kwargs: {'result': {'sessions': [{'id': 'private-handle', 'session_key': 'native-id', 'status': 'idle'}]}})
        assert client.get('/api/v1/open-sessions').status_code == 401
        response = client.get('/api/v1/open-sessions', headers={'Authorization': 'Bearer test-only'})
        assert response.status_code == 200
        assert response.json() == {'known_agent_ids': ['source'], 'items': [{'agent_id': 'source', 'id': 'native-id'}]}


def test_session_model_switch_is_session_scoped_and_verified():
    calls = []
    def rpc(method, params):
        calls.append((method, params))
        if method == 'model.options': return {'result': {'providers': [{'slug': 'p', 'models': ['m']}]}}
        if method == 'session.resume': return {'result': {'session_id': 'handle'}}
        if method == 'config.set': return {'result': {'ok': True}}
        assert method == 'session.status'
        return {'result': {'output': 'Model: m (p)'}}
    assert set_session_model(rpc, 'native', 'p', 'm') == {'provider': 'p', 'model': 'm'}
    assert calls[2][1]['value'] == 'm --provider p --session'
    assert calls[2][1]['session_id'] == 'handle'
    with pytest.raises(ConnectionError): set_session_model(rpc, 'native', 'p', 'not-in-catalog')
