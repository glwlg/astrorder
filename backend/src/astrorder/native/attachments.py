"""Copy native-client media into Astrorder storage; never serve arbitrary host paths."""
from __future__ import annotations

import base64
import os
import posixpath
import re
from pathlib import Path, PurePosixPath
from uuid import uuid4

from astrorder.core.attachments import AttachmentError, AttachmentManager

IMAGE_TYPES = {'image/png', 'image/jpeg', 'image/gif', 'image/webp'}
AUDIO_TYPES = {'audio/mpeg', 'audio/mp4', 'audio/wav', 'audio/x-wav', 'audio/webm', 'audio/ogg', 'audio/aac'}
IMAGE_MAGIC = (
    (b'\x89PNG\r\n\x1a\n', 'image/png'),
    (b'\xff\xd8\xff', 'image/jpeg'),
    (b'GIF87a', 'image/gif'),
    (b'GIF89a', 'image/gif'),
)
AUDIO_MAGIC = (
    (b'ID3', 'audio/mpeg'),
    (b'\xff\xfb', 'audio/mpeg'),
    (b'\xff\xf3', 'audio/mpeg'),
    (b'RIFF', 'audio/wav'),
    (b'OggS', 'audio/ogg'),
    (b'fLaC', 'audio/flac'),
)
IMAGE_REF = re.compile(r'^@image:(?:`([^`]+)`|(\S+))\s*$')


def sniff(data: bytes) -> str | None:
    if len(data) >= 12 and data.startswith(b'RIFF') and data[8:12] == b'WEBP':
        return 'image/webp'
    if len(data) >= 12 and data.startswith(b'RIFF') and data[8:12] == b'WAVE':
        return 'audio/wav'
    for magic, media in IMAGE_MAGIC + AUDIO_MAGIC:
        if data.startswith(magic):
            return media
    return None


def hermes_roots() -> list[Path]:
    home = Path.home()
    candidates = [
        home / '.hermes',
        home / 'AppData' / 'Roaming' / 'Hermes',
        home / 'AppData' / 'Local' / 'hermes',
    ]
    if os.environ.get('APPDATA'):
        candidates.append(Path(os.environ['APPDATA']) / 'Hermes')
    if os.environ.get('LOCALAPPDATA'):
        candidates.append(Path(os.environ['LOCALAPPDATA']) / 'hermes')
    return candidates


def _under(path: Path, root: Path) -> bool:
    try:
        path.resolve().relative_to(root.resolve())
        return True
    except (OSError, ValueError):
        return False


def posix_under(path: str, root: str) -> bool:
    candidate = posixpath.normpath(path)
    base = posixpath.normpath(root)
    return candidate == base or candidate.startswith(base.rstrip('/') + '/')


def ingest(manager: AttachmentManager, name: str, media_type: str, data: bytes) -> dict[str, str]:
    if media_type not in manager.settings.allowed_attachment_types:
        raise AttachmentError('Attachment content type is not allowed', 415)
    if len(data) > manager.settings.max_attachment_size:
        raise AttachmentError('Attachment is too large', 413)
    safe = Path(name).name or 'attachment'
    if safe in {'.', '..'} or '/' in safe or '\\' in safe:
        safe = 'attachment'
    attachment_id = str(uuid4())
    storage_name = f'{os.urandom(16).hex()}.blob'
    target = manager.root / storage_name
    temporary = manager.root / f'.{storage_name}.tmp'
    try:
        with temporary.open('wb') as handle:
            handle.write(data)
        os.replace(temporary, target)
        return manager.store.add_attachment(attachment_id, safe, media_type, len(data), storage_name)
    except Exception:
        temporary.unlink(missing_ok=True)
        target.unlink(missing_ok=True)
        raise


def _read_local_media(manager: AttachmentManager, path: str, roots: list[Path]) -> tuple[Path, str, bytes] | None:
    try:
        candidate = Path(path)
        resolved = candidate.resolve()
    except (OSError, ValueError):
        return None
    if not resolved.is_file() or not any(_under(resolved, Path(root)) for root in roots):
        return None
    try:
        data = resolved.read_bytes()
    except OSError:
        return None
    media = sniff(data)
    if media is None or len(data) > manager.settings.max_attachment_size:
        return None
    return resolved, media, data


def import_local_file(manager: AttachmentManager, path: str, roots: list[Path]) -> dict[str, str] | None:
    local_media = _read_local_media(manager, path, roots)
    if local_media is None:
        return None
    resolved, media, data = local_media
    try:
        return ingest(manager, resolved.name, media, data)
    except AttachmentError:
        return None


def _matches_cached_media(
    manager: AttachmentManager,
    attachment: object,
    resolved: Path,
    media: str,
    data: bytes,
) -> bool:
    if not isinstance(attachment, dict):
        return False
    if attachment.get('name') != resolved.name or attachment.get('media_type') != media:
        return False
    size = attachment.get('size')
    if size is None and isinstance(attachment.get('id'), str):
        stored = manager.store.get_attachment(attachment['id'])
        size = stored.get('size') if stored else None
    return size == len(data)


def import_data_url(manager: AttachmentManager, url: str, name: str = 'attachment') -> dict[str, str] | None:
    if not isinstance(url, str) or not url.startswith('data:') or ';base64,' not in url:
        return None
    header, payload = url.split(';base64,', 1)
    media = header[5:].split(';', 1)[0].strip().lower() or 'application/octet-stream'
    try:
        data = base64.b64decode(payload, validate=True)
    except (ValueError, TypeError):
        return None
    sniffed = sniff(data) or media
    if sniffed not in manager.settings.allowed_attachment_types or len(data) > manager.settings.max_attachment_size:
        return None
    try:
        return ingest(manager, name, sniffed, data)
    except AttachmentError:
        return None


def parse_image_ref(line: str) -> str | None:
    match = IMAGE_REF.match(line.strip())
    if not match:
        return None
    return match.group(1) or match.group(2)


def bind_hermes_refs(message: dict, manager: AttachmentManager, roots: list[Path]) -> dict:
    if message.get('role') != 'user':
        return message
    kept: list[str] = []
    attachments = list(message.get('attachments') or [])
    for line in str(message.get('text') or '').split('\n'):
        path = parse_image_ref(line)
        if path is None:
            kept.append(line)
            continue
        local_media = _read_local_media(manager, path, roots)
        if local_media is None:
            kept.append(line)
            continue
        resolved, media, data = local_media
        if any(_matches_cached_media(manager, attachment, resolved, media, data) for attachment in attachments):
            continue
        try:
            attachments.append(ingest(manager, resolved.name, media, data))
        except AttachmentError:
            kept.append(line)
    message['text'] = '\n'.join(kept).strip('\n')
    message['attachments'] = attachments
    return message


def bind_codex_item(item: dict, manager: AttachmentManager, roots: list[Path], remote_read=None, remote_root: str | None = None) -> list[dict[str, str]]:
    content = item.get('content')
    if not isinstance(content, list):
        return []
    rows: list[dict[str, str]] = []
    for part in content:
        if not isinstance(part, dict):
            continue
        kind = part.get('type')
        if kind == 'image' and isinstance(part.get('url'), str):
            mapped = import_data_url(manager, part['url'], 'image')
            if mapped:
                rows.append(mapped)
        elif kind in {'localImage', 'localAudio', 'local_image', 'local_audio'} and isinstance(part.get('path'), str):
            mapped = import_local_file(manager, part['path'], roots)
            if mapped is None and remote_read and remote_root and posix_under(part['path'], remote_root):
                data = remote_read(part['path'])
                media = sniff(data) if isinstance(data, bytes) else None
                if media:
                    try:
                        mapped = ingest(manager, PurePosixPath(part['path']).name, media, data)
                    except AttachmentError:
                        mapped = None
            if mapped:
                rows.append(mapped)
        elif kind == 'audio' and isinstance(part.get('url'), str):
            mapped = import_data_url(manager, part['url'], 'audio')
            if mapped:
                rows.append(mapped)
    return rows
