from unittest.mock import Mock

import pytest

from astrorder.connections import ConnectionError
from astrorder.native.codex import CodexConnection

SID='01992890-4444-7777-8888-000000000001'

def connection():
    result=CodexConnection(Mock(),Mock(),Mock())
    result._threads={SID:{'id':SID}}; result.state='connected'
    return result

def test_delete_calls_native_delete_and_confirms_absence_in_both_catalogs():
    value=connection(); value._request=Mock(return_value={}); value._pages=Mock(return_value=iter([]))
    value.mutate(SID,None)
    value._request.assert_called_once_with('thread/delete',{'threadId':SID})
    assert [call.args[1]['archived'] for call in value._pages.call_args_list]==[False,True]
    assert SID not in value._threads

def test_no_success_if_native_catalog_still_contains_thread():
    value=connection(); value._request=Mock(return_value={}); value._pages=Mock(return_value=iter([{'id':SID}]))
    with pytest.raises(ConnectionError): value.mutate(SID,None)
    assert SID in value._threads

def test_active_thread_is_not_deleted():
    value=connection(); value._active[SID]='turn'; value._request=Mock()
    with pytest.raises(ConnectionError): value.mutate(SID,None)
    value._request.assert_not_called()
