from __future__ import annotations

import os
import secrets
from pathlib import Path, PurePath
from typing import BinaryIO
from uuid import uuid4

from fastapi import UploadFile

from .config import Settings
from .store import Store


class AttachmentError(ValueError):
    def __init__(self, detail: str, status_code: int = 400):
        self.status_code = status_code
        super().__init__(detail)


class AttachmentManager:
    def __init__(self, settings: Settings, store: Store):
        self.settings = settings
        self.store = store
        self.root = settings.attachments_dir.resolve()
        self.root.mkdir(parents=True, exist_ok=True)

    def _safe_name(self, filename: str | None) -> str:
        if not filename or filename in {".", ".."}:
            raise AttachmentError("Attachment filename is required")
        if any(ord(character) < 32 or ord(character) == 127 for character in filename):
            raise AttachmentError("Attachment filename contains control characters")
        if "/" in filename or "\\" in filename or Path(filename).name != filename:
            raise AttachmentError("Attachment filename must not contain a path")
        if PurePath(filename).is_absolute() or ":" in filename[:3]:
            raise AttachmentError("Attachment filename must be relative")
        if len(filename) > 256:
            raise AttachmentError("Attachment filename is too long")
        return filename

    def _media_type(self, value: str | None) -> str:
        media_type = (value or "application/octet-stream").split(";", 1)[0].strip().lower()
        if media_type not in self.settings.allowed_attachment_types:
            raise AttachmentError("Attachment content type is not allowed", 415)
        return media_type

    @staticmethod
    def _write_limited(source: BinaryIO, destination: BinaryIO, maximum: int) -> int:
        total = 0
        while True:
            chunk = source.read(min(1024 * 1024, maximum + 1 - total))
            if not chunk:
                return total
            total += len(chunk)
            if total > maximum:
                raise AttachmentError("Attachment is too large", 413)
            destination.write(chunk)

    async def save(self, upload: UploadFile, source_path: str | None = None) -> dict[str, str]:
        name = self._safe_name(upload.filename)
        media_type = self._media_type(upload.content_type)
        if source_path is not None:
            if (
                not source_path
                or len(source_path) > 4096
                or any(ord(character) < 32 for character in source_path)
                or not Path(source_path).is_absolute()
                or Path(source_path).name != name
            ):
                raise AttachmentError("Attachment source path is invalid")
        attachment_id = str(uuid4())
        storage_name = f"{secrets.token_hex(16)}.blob"
        target = self.root / storage_name
        if target.parent.resolve() != self.root:
            raise AttachmentError("Attachment storage path is invalid", 500)
        temporary = self.root / f".{storage_name}.tmp"
        size = 0
        try:
            with temporary.open("wb") as handle:
                size = self._write_limited(upload.file, handle, self.settings.max_attachment_size)
            os.replace(temporary, target)
            return self.store.add_attachment(
                attachment_id, name, media_type, size, storage_name, source_path
            )
        except AttachmentError:
            temporary.unlink(missing_ok=True)
            target.unlink(missing_ok=True)
            raise
        except Exception:
            temporary.unlink(missing_ok=True)
            target.unlink(missing_ok=True)
            raise

    def path_for(self, attachment_id: str) -> tuple[Path, dict[str, str | int]] | None:
        record = self.store.get_attachment(attachment_id)
        if record is None:
            return None
        storage_name = str(record.get("storage_name") or "")
        if not storage_name or "/" in storage_name or chr(92) in storage_name or ".." in storage_name:
            return None
        candidate = self.root / storage_name
        if candidate.is_file():
            return candidate, record
        try:
            resolved = candidate.resolve()
            if resolved.is_file():
                return resolved, record
        except Exception:
            pass
        legacy = Path("P:/workspace/glwlg/ai/astrorder/data/attachments") / storage_name
        if legacy.is_file():
            return legacy, record
        return None
