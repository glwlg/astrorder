from unittest.mock import Mock
from astrorder.native.observers import reconcile_native_status, reply_preview

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
    # An empty poll (stream token gap / tool handoff) must NOT clear running:
    # that caused the rail to flicker at the 1s poll cadence. Demotion is the
    # frontend live/stale windows' job, or _sync_hermes_activity after grace.
    assert reconcile_native_status("running", None) is None
    assert reconcile_native_status("waiting_approval", None) is None
    # Error latched; never touched.
    assert reconcile_native_status("error", "running") is None
    assert reconcile_native_status("idle", None) is None
    # An explicit idle (post-grace clear) is honored.
    assert reconcile_native_status("running", "idle") == "idle"
