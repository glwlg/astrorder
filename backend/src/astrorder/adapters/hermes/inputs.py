"""Hermes attachments use native byte APIs so SSH runtimes never receive controller paths."""
import base64
import binascii
from pathlib import Path

from astrorder.core.attachments import AttachmentManager
from astrorder.connections import ConnectionError
from astrorder.native.attachments import IMAGE_TYPES


def pack_daemon_attachments(command, settings, store):
    """Resolve registered App attachments into path-free daemon wire payloads."""
    attachments = command.get("attachments") or []
    if not attachments:
        return []
    if store is None or not hasattr(settings, "attachments_dir"):
        raise ConnectionError("附件存储尚未就绪；未向守护进程发送。", 503)
    manager = AttachmentManager(settings, store)
    payloads = []
    total = 0
    for attachment in attachments:
        attachment_id = attachment.get("id") if isinstance(attachment, dict) else None
        if not isinstance(attachment_id, str) or not attachment_id:
            raise ConnectionError("附件身份无效；未向守护进程发送。", 422)
        resolved = manager.path_for(attachment_id)
        if not resolved:
            raise ConnectionError("附件文件不可用；未向守护进程发送。", 422)
        path, record = resolved
        data = Path(path).read_bytes()
        total += len(data)
        if total > settings.max_attachment_size:
            raise ConnectionError("本次附件总大小超过限制。", 413)
        payloads.append(
            {
                "name": Path(str(record.get("name") or "attachment")).name,
                "media_type": str(record["media_type"]),
                "content_base64": base64.b64encode(data).decode("ascii"),
            }
        )
    return payloads


def stage_daemon_attachments(
    rpc,
    session_id,
    text,
    attachments,
    *,
    maximum,
    allowed_types,
):
    """Validate path-free daemon payloads and attach bytes through native RPC."""
    if not isinstance(attachments, list) or len(attachments) > 32:
        raise ConnectionError("守护进程附件列表无效；未发送消息。", 422)
    allowed = {str(value).lower() for value in allowed_types}
    native_images = []
    refs = []
    total = 0
    try:
        for attachment in attachments:
            if not isinstance(attachment, dict):
                raise ConnectionError("守护进程附件内容无效；未发送消息。", 422)
            name = attachment.get("name")
            mime = attachment.get("media_type")
            encoded = attachment.get("content_base64")
            if (
                not isinstance(name, str)
                or not name
                or len(name) > 256
                or Path(name).name != name
                or "/" in name
                or "\\" in name
                or any(ord(character) < 32 or ord(character) == 127 for character in name)
            ):
                raise ConnectionError("守护进程附件名称无效；未发送消息。", 422)
            if not isinstance(mime, str) or mime.lower() not in allowed:
                raise ConnectionError("守护进程附件类型不受支持；未发送消息。", 415)
            if not isinstance(encoded, str):
                raise ConnectionError("守护进程附件编码无效；未发送消息。", 422)
            try:
                data = base64.b64decode(encoded, validate=True)
            except (binascii.Error, ValueError):
                raise ConnectionError("守护进程附件编码无效；未发送消息。", 422) from None
            total += len(data)
            if total > maximum:
                raise ConnectionError("本次附件总大小超过限制。", 413)
            if mime.lower() in IMAGE_TYPES:
                response = rpc(
                    "image.attach_bytes",
                    {
                        "session_id": session_id,
                        "content_base64": encoded,
                        "filename": name,
                    },
                    timeout=20,
                )
                result = response.get("result") if isinstance(response, dict) else None
                if not isinstance(result, dict) or not result.get("attached"):
                    raise ConnectionError("Hermes 未接收图片附件；未发送消息。", 502)
                if isinstance(result.get("path"), str):
                    native_images.append(result["path"])
            else:
                response = rpc(
                    "file.attach",
                    {
                        "session_id": session_id,
                        "name": name,
                        "data_url": f"data:{mime.lower()};base64,{encoded}",
                    },
                    timeout=20,
                )
                result = response.get("result") if isinstance(response, dict) else None
                if not isinstance(result, dict) or not result.get("attached"):
                    raise ConnectionError("Hermes 未接收文件附件；未发送消息。", 502)
                ref = result.get("ref_text")
                if isinstance(ref, str) and ref:
                    refs.append(ref)
    except ConnectionError:
        rollback(rpc, session_id, native_images)
        raise
    if refs:
        text = "\n".join(refs + ([text] if text else []))
    return text, native_images

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
