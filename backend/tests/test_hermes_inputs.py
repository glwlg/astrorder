from types import SimpleNamespace
from unittest.mock import Mock
import base64
import pytest
from astrorder.connections import ConnectionError
from astrorder.hermes_inputs import stage

PNG = b'\x89PNG\r\n\x1a\nimage'


def setup(tmp_path, mime='image/png', name='shot.png', raw=PNG):
    blob = tmp_path / 'safe.blob'
    blob.write_bytes(raw)
    store = Mock()
    store.get_attachment.return_value = {'storage_name': 'safe.blob', 'media_type': mime, 'name': name}
    calls = []
    def rpc(method, params, timeout=15):
        calls.append((method, params))
        if method == 'image.attach_bytes':
            assert 'C:\\' not in str(params) and str(tmp_path) not in str(params)
            return {'result': {'attached': True, 'path': '/native/session/shot.png'}}
        if method == 'file.attach':
            assert params['data_url'].startswith('data:')
            assert str(tmp_path) not in str(params)
            return {'result': {'attached': True, 'ref_text': '@file:shot.pdf'}}
        raise AssertionError(method)
    settings = SimpleNamespace(attachments_dir=tmp_path, max_attachment_size=1024)
    return rpc, calls, settings, store


def test_images_use_bytes_and_never_host_paths(tmp_path):
    rpc, calls, settings, store = setup(tmp_path)
    text, attached = stage(rpc, 'live', {'text': '看图', 'attachments': [{'id': 'a'}]}, settings, store)
    assert text == '看图'
    assert attached == ['/native/session/shot.png']
    assert calls[0][0] == 'image.attach_bytes'
    assert 'content_base64' in calls[0][1]
    assert base64.b64decode(calls[0][1]['content_base64']) == PNG


def test_documents_become_workspace_file_refs_before_submit(tmp_path):
    rpc, calls, settings, store = setup(tmp_path, mime='application/pdf', name='shot.pdf', raw=b'%PDF-1.4')
    text, attached = stage(rpc, 'live', {'text': '请读', 'attachments': [{'id': 'a'}]}, settings, store)
    assert text == '@file:shot.pdf\n请读'
    assert attached == []
    assert calls[0][0] == 'file.attach'


def test_failed_attach_does_not_submit(tmp_path):
    rpc, calls, settings, store = setup(tmp_path)
    def failing(method, params, timeout=15):
        calls.append((method, params))
        return {'error': {'message': 'no'}}
    with pytest.raises(ConnectionError, match='未接收'):
        stage(failing, 'live', {'text': 'x', 'attachments': [{'id': 'a'}]}, settings, store)
