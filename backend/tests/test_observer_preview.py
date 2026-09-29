from types import SimpleNamespace
from unittest.mock import Mock
from astrorder.native.observers import NativeObservers, reconcile_native_status, reply_preview

def test_preview_uses_only_matching_turn_and_redacts_secrets():
    client=Mock()
    client._request.return_value={'data':[{'turnId':'other','item':{'type':'agentMessage','text':'wrong reply'}},{'turnId':'wanted','item':{'type':'agentMessage','text':'已完成。token=private-value'}}]}
    value=reply_preview(client,'session','wanted')
    assert '已完成' in value and 'private-value' not in value and 'wrong reply' not in value

def test_missing_turn_never_substitutes_another_reply():
    client=Mock()
    assert reply_preview(client,'session',None)==''
    client._request.assert_not_called()


def test_reconcile_native_status_promotes_but_never_demotes_on_empty_poll():
    # Promotion from Hermes active_list works.
    assert reconcile_native_status("idle", "running") == "running"
    assert reconcile_native_status("running", "running") is None
    # An empty poll (stream token gap / tool handoff) must NOT clear running.
    assert reconcile_native_status("running", None) is None
    assert reconcile_native_status("waiting_approval", None) is None
    # Error latched; never touched.
    assert reconcile_native_status("error", "running") is None
    assert reconcile_native_status("idle", None) is None
    # An explicit terminal state is honored.
    assert reconcile_native_status("running", "idle") == "idle"


def test_hermes_activity_updates_daemon_before_app_store():
    calls = []
    session = {"id": "native-1", "agent_id": "local-hermes", "status": "idle", "control_state": "owned"}
    runtime = SimpleNamespace(
        _state="connected",
        _agent_id="local-hermes",
        _rpc=lambda _method, _params: {
            "result": {"sessions": [{"session_key": "native-1", "status": "working"}]}
        },
    )

    class Bridge:
        async def request_control(self, action, fields):
            calls.append(action)
            return {"result": fields}

    class Store:
        def list_sessions(self, _agent_id):
            return [session]

        def upsert_session(self, updated):
            session.update(updated)
            calls.append("store.upsert")
            return session

    state = SimpleNamespace(
        connections=SimpleNamespace(local=runtime, _ssh_runtimes={}),
        store=Store(),
        service=SimpleNamespace(_server_event=lambda *args, **kwargs: None),
        daemon_bridge=Bridge(),
    )
    NativeObservers(SimpleNamespace(state=state))._sync_hermes_activity()

    assert calls == ["session.spawn", "session.observe_status", "store.upsert"]
    assert session["status"] == "running"
