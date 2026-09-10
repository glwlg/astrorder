from astrorder.attachments import AttachmentManager
from astrorder.config import Settings
from astrorder.native_attachments import bind_codex_item, bind_hermes_refs, import_local_file
from astrorder.store import Store

PNG = b'\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00\x90wS\xde'


def manager(tmp_path):
    settings = Settings(database_url=f'sqlite:///{tmp_path}/db.sqlite3', attachments_dir=tmp_path / 'att', browser_secret='x', connector_secret='y')
    return AttachmentManager(settings, Store(settings)), tmp_path / 'native'


def test_native_directory_images_are_copied_and_foreign_paths_are_ignored(tmp_path):
    att, root = manager(tmp_path)
    root.mkdir()
    inside = root / 'shot.png'
    inside.write_bytes(PNG)
    secret = tmp_path / 'private.png'
    secret.write_bytes(PNG)
    mapped = import_local_file(att, str(inside), [root])
    assert mapped['media_type'] == 'image/png'
    assert mapped['url'].startswith('/api/v1/attachments/')
    assert att.path_for(mapped['id'])[0].read_bytes() == PNG
    assert import_local_file(att, str(secret), [root]) is None
    assert import_local_file(att, str(root / '..' / 'private.png'), [root]) is None


def test_hermes_image_refs_become_previews_only_inside_native_roots(tmp_path):
    att, root = manager(tmp_path)
    root.mkdir()
    image = root / 'clip.png'
    image.write_bytes(PNG)
    message = {'role': 'user', 'text': f'看这张\n@image:{image}\n谢谢', 'attachments': []}
    bind_hermes_refs(message, att, [root])
    assert message['text'] == '看这张\n谢谢'
    assert message['attachments'][0]['name'] == 'clip.png'
    leftover = {'role': 'user', 'text': '@image:C:\\private\\upload.png', 'attachments': []}
    bind_hermes_refs(leftover, att, [root])
    assert leftover['attachments'] == []
    assert leftover['text'] == '@image:C:\\private\\upload.png'


def test_codex_local_image_and_data_url_map_without_keeping_host_paths(tmp_path):
    att, root = manager(tmp_path)
    root.mkdir()
    image = root / 'native.png'
    image.write_bytes(PNG)
    import base64
    data = 'data:image/png;base64,' + base64.b64encode(PNG).decode()
    rows = bind_codex_item({'content': [{'type': 'text', 'text': 'look'}, {'type': 'localImage', 'path': str(image)}, {'type': 'image', 'url': data}]}, att, [root])
    assert len(rows) == 2
    assert all(row['url'].startswith('/api/v1/attachments/') for row in rows)
    assert str(image) not in str(rows)
