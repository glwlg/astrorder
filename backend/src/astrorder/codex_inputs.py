"""Codex image and audio inputs use inline bytes, so SSH runtimes never receive local Windows paths."""
import base64
from pathlib import Path

from .attachments import AttachmentManager
from .connections import ConnectionError
from .native_attachments import AUDIO_TYPES, IMAGE_TYPES


def command_input(settings, store, command):
    inputs = []
    if command.get('text'):
        inputs.append({'type': 'text', 'text': command['text']})
    if not command.get('attachments'):
        return inputs
    manager = AttachmentManager(settings, store)
    total = 0
    for attachment in command.get('attachments', []):
        resolved = manager.path_for(attachment['id'])
        if not resolved:
            raise ConnectionError('附件文件不可用；未向原生端发送。', 422)
        path, record = resolved
        mime = record['media_type']
        if mime not in IMAGE_TYPES and mime not in AUDIO_TYPES:
            raise ConnectionError('Codex 当前接通图片与音频附件；文档尚未映射，未发送。', 415)
        with Path(path).open('rb') as handle:
            data = handle.read(settings.max_attachment_size + 1)
        total += len(data)
        if total > settings.max_attachment_size:
            raise ConnectionError('本次附件总大小超过限制。', 413)
        kind = 'image' if mime in IMAGE_TYPES else 'audio'
        inputs.append({'type': kind, 'url': f'data:{mime};base64,' + base64.b64encode(data).decode('ascii')})
    return inputs
