from unittest.mock import Mock
from astrorder.native_observers import reply_preview

def test_preview_uses_only_matching_turn_and_redacts_secrets():
    client=Mock()
    client._request.return_value={'data':[{'turnId':'other','item':{'type':'agentMessage','text':'wrong reply'}},{'turnId':'wanted','item':{'type':'agentMessage','text':'已完成。token=private-value'}}]}
    value=reply_preview(client,'session','wanted')
    assert '已完成' in value and 'private-value' not in value and 'wrong reply' not in value

def test_missing_turn_never_substitutes_another_reply():
    client=Mock()
    assert reply_preview(client,'session',None)==''
    client._request.assert_not_called()
