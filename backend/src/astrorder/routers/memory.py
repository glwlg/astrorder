from __future__ import annotations

import json
import logging
from typing import Any
from fastapi import APIRouter, HTTPException, Query, Request
from pydantic import BaseModel, Field

from ..core.auth import require_browser
from ..core.model_sync_service import list_targets
from ..core.openviking_client import OpenVikingClient
from ..core.ov_sync import apply_target_ov_config, inspect_target_ov_status
from ..models import WorkspacePreferenceRow
from sqlalchemy import select

logger = logging.getLogger("astrorder.memory")

router = APIRouter()

CONFIG_KEY = "memory:openviking:config"


def _get_client(request: Request) -> OpenVikingClient:
    store = request.app.state.store
    with store.session() as db:
        row = db.scalar(select(WorkspacePreferenceRow).where(WorkspacePreferenceRow.key == CONFIG_KEY))
        if row and row.value:
            try:
                cfg = json.loads(row.value)
                return OpenVikingClient(
                    base_url=cfg.get("url") or "http://127.0.0.1:1933",
                    api_key=cfg.get("api_key") or None,
                )
            except Exception:
                pass
    # 默认回退到 ~/.openviking/ovcli.conf 或本地标准端口
    from pathlib import Path
    conf_path = Path.home() / ".openviking" / "ovcli.conf"
    if conf_path.is_file():
        try:
            data = json.loads(conf_path.read_text(encoding="utf-8"))
            return OpenVikingClient(base_url=data.get("url") or "http://127.0.0.1:1933", api_key=data.get("api_key"))
        except Exception:
            pass
    return OpenVikingClient()


class OpenVikingConfigPayload(BaseModel):
    url: str = Field(min_length=1, max_length=512)
    api_key: str | None = Field(default=None, max_length=512)


class MemorySearchPayload(BaseModel):
    query: str = Field(min_length=1, max_length=1000)
    scope: str | None = Field(default=None, max_length=512)
    limit: int = Field(default=5, ge=1, le=20)


@router.get("/api/v1/memory/config")
def get_memory_config(request: Request) -> dict[str, Any]:
    require_browser(request, request.app.state.settings)
    store = request.app.state.store
    url = "http://127.0.0.1:1933"
    masked_key = ""
    with store.session() as db:
        row = db.scalar(select(WorkspacePreferenceRow).where(WorkspacePreferenceRow.key == CONFIG_KEY))
        if row and row.value:
            try:
                cfg = json.loads(row.value)
                url = cfg.get("url") or url
                k = cfg.get("api_key") or ""
                if k:
                    masked_key = (k[:4] + "..." + k[-4:]) if len(k) > 10 else "********"
            except Exception:
                pass
        else:
            from pathlib import Path
            conf_path = Path.home() / ".openviking" / "ovcli.conf"
            if conf_path.is_file():
                try:
                    data = json.loads(conf_path.read_text(encoding="utf-8"))
                    url = data.get("url") or url
                    k = data.get("api_key") or ""
                    if k:
                        masked_key = (k[:4] + "..." + k[-4:]) if len(k) > 10 else "********"
                except Exception:
                    pass
    return {"url": url, "masked_key": masked_key}


@router.post("/api/v1/memory/config")
def update_memory_config(payload: OpenVikingConfigPayload, request: Request) -> dict[str, Any]:
    require_browser(request, request.app.state.settings)
    store = request.app.state.store
    with store.session() as db:
        # 获取原有以防保留 key
        row = db.scalar(select(WorkspacePreferenceRow).where(WorkspacePreferenceRow.key == CONFIG_KEY))
        current = {}
        if row and row.value:
            try:
                current = json.loads(row.value)
            except Exception:
                pass
        new_key = payload.api_key if payload.api_key is not None else current.get("api_key")
        saved = {"url": payload.url.strip(), "api_key": new_key}
        if row:
            row.value = json.dumps(saved)
        else:
            db.add(WorkspacePreferenceRow(key=CONFIG_KEY, value=json.dumps(saved)))
        db.commit()
    masked_key = ""
    if new_key:
        masked_key = (new_key[:4] + "..." + new_key[-4:]) if len(new_key) > 10 else "********"
    return {"url": payload.url.strip(), "masked_key": masked_key}


@router.get("/api/v1/memory/health")
async def memory_health(request: Request) -> dict[str, Any]:
    require_browser(request, request.app.state.settings)
    client = _get_client(request)
    return await client.health()


@router.get("/api/v1/memory/ls")
async def memory_ls(request: Request, uri: str = Query(default="viking://")) -> dict[str, Any]:
    require_browser(request, request.app.state.settings)
    client = _get_client(request)
    try:
        items = await client.list_dir(uri)
        return {"uri": uri, "items": items}
    except Exception as exc:
        raise HTTPException(status_code=502, detail=f"OpenViking 目录列取失败: {exc}") from exc


@router.get("/api/v1/memory/tree")
async def memory_tree(request: Request, uri: str = Query(default="viking://")) -> dict[str, Any]:
    require_browser(request, request.app.state.settings)
    client = _get_client(request)
    try:
        items = await client.get_tree(uri)
        return {"uri": uri, "items": items}
    except Exception as exc:
        raise HTTPException(status_code=502, detail=f"OpenViking 目录树获取失败: {exc}") from exc


@router.get("/api/v1/memory/content")
async def memory_content(request: Request, uri: str = Query(...)) -> dict[str, Any]:
    require_browser(request, request.app.state.settings)
    client = _get_client(request)
    try:
        content = await client.read_content(uri)
        return {"uri": uri, "content": content}
    except Exception as exc:
        raise HTTPException(status_code=502, detail=f"OpenViking 内容读取失败: {exc}") from exc


@router.post("/api/v1/memory/search")
async def memory_search(payload: MemorySearchPayload, request: Request) -> dict[str, Any]:
    require_browser(request, request.app.state.settings)
    client = _get_client(request)
    try:
        results = await client.search(payload.query, scope=payload.scope, limit=payload.limit)
        return {"items": results}
    except Exception as exc:
        raise HTTPException(status_code=502, detail=f"OpenViking 语义搜索失败: {exc}") from exc


@router.get("/api/v1/memory/targets")
def memory_targets(request: Request) -> dict[str, Any]:
    require_browser(request, request.app.state.settings)
    store = request.app.state.store
    targets = list_targets(store)
    return {"items": targets}


@router.get("/api/v1/memory/targets/{target_id}/status")
def memory_target_status(target_id: str, request: Request) -> dict[str, Any]:
    require_browser(request, request.app.state.settings)
    store = request.app.state.store
    return inspect_target_ov_status(store, target_id)


class MemoryApplyPayload(BaseModel):
    target_ids: list[str]


@router.post("/api/v1/memory/apply")
def memory_apply(payload: MemoryApplyPayload, request: Request) -> dict[str, Any]:
    require_browser(request, request.app.state.settings)
    store = request.app.state.store
    results = []
    for tid in payload.target_ids:
        res = apply_target_ov_config(store, tid)
        results.append(res)
    return {"items": results}

