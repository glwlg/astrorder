from __future__ import annotations

import asyncio
import logging
from typing import Any
from fastapi import APIRouter, HTTPException, Query, Request
from pydantic import BaseModel, Field

from ..core.auth import require_browser
from ..connections import ConnectionError
from ..core.rtk_service import (
    get_local_rtk_status,
    get_ssh_rtk_status,
    execute_local_agent_toggle,
    execute_ssh_agent_toggle,
    execute_local_rtk_install,
    execute_ssh_rtk_install,
)

logger = logging.getLogger("astrorder.routers.rtk")

router = APIRouter(prefix="/api/v1/rtk", tags=["rtk"])


class AgentTogglePayload(BaseModel):
    enabled: bool = Field(..., description="启用或禁用 RTK 针对此 Agent 的指令重写")


@router.get("/{connection_id}")
async def get_rtk_status(
    connection_id: str,
    request: Request,
    days: str = Query(default="30", pattern=r"^(30|90|all)$"),
) -> dict[str, Any]:
    """
    获取指定环境（local 或 SSH connection_id）的 RTK 运行与配置状态
    严格按原 connection_id 查找，不 strip 前缀；错误不暴露内部敏感配置。
    """
    require_browser(request, request.app.state.settings)

    cid = connection_id.strip()

    try:
        if cid in {"local", ""}:
            return await asyncio.to_thread(get_local_rtk_status, days=days)
        else:
            return await asyncio.to_thread(
                get_ssh_rtk_status,
                request.app.state.store,
                request.app.state.settings,
                request.app.state.service,
                cid,
                days=days,
            )
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from exc
    except Exception as exc:
        logger.exception("Failed to get RTK status for %s", connection_id)
        raise HTTPException(status_code=500, detail="获取 RTK 运行状态失败，请检查服务日志与网络连接。") from exc


@router.post("/{connection_id}/install")
async def install_rtk(
    connection_id: str,
    request: Request,
) -> dict[str, Any]:
    """
    在指定环境（local 或 SSH connection_id）上安装 RTK
    严格按原 connection_id 查找，不 strip 前缀；错误不暴露内部敏感配置。
    """
    require_browser(request, request.app.state.settings)

    cid = connection_id.strip()

    try:
        if cid in {"local", ""}:
            await asyncio.to_thread(execute_local_rtk_install)
            return await asyncio.to_thread(get_local_rtk_status)
        else:
            await asyncio.to_thread(
                execute_ssh_rtk_install,
                request.app.state.store,
                request.app.state.settings,
                request.app.state.service,
                cid,
            )
            return await asyncio.to_thread(
                get_ssh_rtk_status,
                request.app.state.store,
                request.app.state.settings,
                request.app.state.service,
                cid,
            )
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from exc
    except Exception as exc:
        logger.exception("Failed to install RTK on %s", connection_id)
        raise HTTPException(status_code=500, detail="RTK 安装执行失败，请检查目标系统权限与网络。") from exc


@router.put("/{connection_id}/agents/{kind}")
async def toggle_rtk_agent(
    connection_id: str,
    kind: str,
    payload: AgentTogglePayload,
    request: Request,
) -> dict[str, Any]:
    """
    切换指定环境中特定 Agent 的 RTK 启用/禁用状态
    写后直接返回最新的完整 RTK 状态
    严格按原 connection_id 查找，不 strip 前缀；错误不暴露内部敏感配置。
    """
    require_browser(request, request.app.state.settings)

    cid = connection_id.strip()

    try:
        if cid in {"local", ""}:
            await asyncio.to_thread(execute_local_agent_toggle, kind, payload.enabled)
            return await asyncio.to_thread(get_local_rtk_status)
        else:
            await asyncio.to_thread(
                execute_ssh_agent_toggle,
                request.app.state.store,
                request.app.state.settings,
                request.app.state.service,
                cid,
                kind,
                payload.enabled,
            )
            return await asyncio.to_thread(
                get_ssh_rtk_status,
                request.app.state.store,
                request.app.state.settings,
                request.app.state.service,
                cid,
            )
    except ConnectionError as exc:
        raise HTTPException(status_code=exc.status_code, detail=exc.detail) from exc
    except Exception as exc:
        logger.exception("Failed to toggle RTK agent %s on %s", kind, connection_id)
        raise HTTPException(status_code=500, detail="切换 Agent RTK 状态失败，已安全回滚配置。") from exc
