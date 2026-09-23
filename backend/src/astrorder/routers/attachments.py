from __future__ import annotations

from typing import Any
from fastapi import APIRouter, File, Form, HTTPException, Request, UploadFile
from fastapi.responses import FileResponse

from ..core.attachments import AttachmentError

router = APIRouter()


def _private(request: Request) -> None:
    from ..core.auth import require_browser
    require_browser(request, request.app.state.settings)


@router.post("/api/v1/attachments", status_code=201)
async def upload_attachment(
    request: Request,
    file: UploadFile = File(...),
    source_path: str | None = Form(None),
) -> dict[str, str]:
    _private(request)
    try:
        return await request.app.state.attachments.save(file, source_path)
    except AttachmentError as exc:
        raise HTTPException(status_code=exc.status_code, detail=str(exc)) from None


@router.get("/api/v1/attachments/{attachment_id}")
def download_attachment(attachment_id: str, request: Request) -> FileResponse:
    _private(request)
    result = request.app.state.attachments.path_for(attachment_id)
    if result is None:
        raise HTTPException(status_code=404, detail="Attachment was not found")
    path, metadata = result
    return FileResponse(
        path,
        media_type=str(metadata["media_type"]),
        filename=str(metadata["name"]),
    )
