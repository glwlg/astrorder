import base64
from types import SimpleNamespace
from unittest.mock import Mock

import pytest

from astrorder.codex_inputs import command_input
from astrorder.connections import ConnectionError


def test_images_are_embedded_not_passed_as_controller_paths(tmp_path):
    raw=b'\x89PNG\r\n\x1a\nimage'
    (tmp_path/'safe.blob').write_bytes(raw)
    store=Mock(); store.get_attachment.return_value={'storage_name':'safe.blob','media_type':'image/png','name':'image.png'}
    values=command_input(SimpleNamespace(attachments_dir=tmp_path,max_attachment_size=1024),store,{'text':'look','attachments':[{'id':'upload-id'}]})
    assert values[0]=={'type':'text','text':'look'}
    assert values[1]=={'type':'image','url':'data:image/png;base64,'+base64.b64encode(raw).decode()}
    assert str(tmp_path) not in str(values)

def test_audio_is_embedded_as_audio_input(tmp_path):
    raw=b'ID3audio'
    (tmp_path/'safe.blob').write_bytes(raw)
    store=Mock(); store.get_attachment.return_value={'storage_name':'safe.blob','media_type':'audio/mpeg','name':'clip.mp3'}
    values=command_input(SimpleNamespace(attachments_dir=tmp_path,max_attachment_size=1024),store,{'text':'listen','attachments':[{'id':'upload-id'}]})
    assert values[1]=={'type':'audio','url':'data:audio/mpeg;base64,'+base64.b64encode(raw).decode()}
    assert str(tmp_path) not in str(values)

def test_missing_and_unsupported_attachments_fail_before_native_submission(tmp_path):
    (tmp_path/'safe.blob').write_bytes(b'pdf')
    store=Mock(); store.get_attachment.return_value={'storage_name':'safe.blob','media_type':'application/pdf'}
    with pytest.raises(ConnectionError): command_input(SimpleNamespace(attachments_dir=tmp_path,max_attachment_size=1024),store,{'text':'','attachments':[{'id':'x'}]})

def test_attachment_record_cannot_escape_storage_root(tmp_path):
    outside=tmp_path.parent/'outside.txt'; outside.write_text('private')
    store=Mock(); store.get_attachment.return_value={'storage_name':'../outside.txt','media_type':'image/png'}
    with pytest.raises(ConnectionError): command_input(SimpleNamespace(attachments_dir=tmp_path,max_attachment_size=1024),store,{'text':'','attachments':[{'id':'x'}]})
