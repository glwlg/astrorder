"""Hermes attachments use native byte APIs so SSH runtimes never receive controller paths."""
import base64
from pathlib import Path

from .attachments import AttachmentManager
from .connections import ConnectionError
from .native_attachments import IMAGE_TYPES

def stage(rpc, session_id, command, settings, store):
    text = command.get('text') if isinstance(command.get('text'), str) else ''
    attachments = command.get('attachments') or []
    if not attachments:
        return text, []
    if store is None or not hasattr(settings, 'attachments_dir'):
        raise ConnectionError('附件存储尚未就绪；未向原生端发送。', 503)
    manager = AttachmentManager(settings, store)
    native_images = []
    refs = []
    total = 0
    for attachment in attachments:
        resolved = manager.path_for(attachment['id'])
        if not resolved:
            raise ConnectionError('附件文件不可用；未向原生端发送。', 422)
        path, record = resolved
        data = Path(path).read_bytes()
        total += len(data)
        if total > settings.max_attachment_size:
            raise ConnectionError('本次附件总大小超过限制。', 413)
        mime = record['media_type']
        name = str(record.get('name') or 'attachment')
        if mime in IMAGE_TYPES:
            response = rpc('image.attach_bytes', {'session_id': session_id, 'content_base64': base64.b64encode(data).decode('ascii'), 'filename': Path(name).name}, timeout=20)
            result = response.get('result') if isinstance(response, dict) else None
            if not isinstance(result, dict) or not result.get('attached'):
                raise ConnectionError('Hermes 未接收图片附件；未发送消息。', 502)
            if isinstance(result.get('path'), str):
                native_images.append(result['path'])
        else:
            response = rpc('file.attach', {'session_id': session_id, 'name': Path(name).name, 'data_url': f'data:{mime};base64,{base64.b64encode(data).decode("ascii")}'}, timeout=20)
            result = response.get('result') if isinstance(response, dict) else None
            if not isinstance(result, dict) or not result.get('attached'):
                raise ConnectionError('Hermes 未接收文件附件；未发送消息。', 502)
            ref = result.get('ref_text')
            if isinstance(ref, str) and ref:
                refs.append(ref)
    if refs:
        text = '\n'.join(refs + ([text] if text else []))
    return text, native_images


def rollback(rpc, session_id, paths):
    for path in paths:
        try:
            rpc('image.detach', {'session_id': session_id, 'path': path}, timeout=10)
        except (OSError, TypeError, ValueError):
            continue


def deliver(rpc, session_id, command, settings, store):
    text, attached = stage(rpc, session_id, command, settings, store)
    response = rpc('prompt.submit', {'session_id': session_id, 'text': text, 'surface': 'hud'}, timeout=20)
    if response is None or (isinstance(response, dict) and (response.get('error') is not None or not isinstance(response.get('result'), dict))):
        rollback(rpc, session_id, attached)
    return text, response
