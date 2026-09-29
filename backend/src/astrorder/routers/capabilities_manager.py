from __future__ import annotations

import logging
from typing import Any
from fastapi import APIRouter, HTTPException, Query, Request
from pydantic import BaseModel

from ..core.auth import require_browser
from ..core.ov_sync import list_targets
from ..core.capabilities_sync import (
    get_target_capabilities,
    update_mcp_server,
    update_plugin_state,
    update_marketplace,
    ping_mcp_server,
)
from ..core.capabilities_dispatch import dispatch_capability

logger = logging.getLogger("astrorder.capabilities_router")

router = APIRouter()


class TargetCapabilitiesResponse(BaseModel):
    target_id: str
    target_name: str | None = None
    kind: str
    counts: dict[str, int]
    skills: list[dict[str, Any]]
    mcp_servers: list[dict[str, Any]]
    plugins: list[dict[str, Any]]
    marketplaces: list[dict[str, Any]]


class McpUpdateRequest(BaseModel):
    target_id: str = "local"
    agent: str = "codex"  # "codex", "hermes", "grok"
    action: str = "save"  # "save", "delete"
    server: dict[str, Any]


class PluginUpdateRequest(BaseModel):
    target_id: str = "local"
    agent: str = "codex"
    action: str = "toggle"  # "toggle", "uninstall"
    plugin_name: str
    enabled: bool = True


class MarketplaceUpdateRequest(BaseModel):
    target_id: str = "local"
    agent: str = "codex"
    action: str = "add"  # "add", "pull", "delete"
    market: dict[str, Any]


class McpPingRequest(BaseModel):
    target_id: str = "local"
    server: dict[str, Any]


class CapabilityDispatchRequest(BaseModel):
    capability_type: str  # "skill", "mcp", "plugin"
    source_target_id: str = "local"
    source_agent: str = "all"
    item_id: str
    target_matrix: list[dict[str, Any]]
    item_data: dict[str, Any] | None = None


# ==========================================
# 1. 目标与能力总览接口
# ==========================================

@router.get("/api/v1/capabilities/targets")
async def get_all_targets(request: Request) -> list[dict[str, Any]]:
    """列出所有支持能力管控的目标环境（本机、Debian、WSL）"""
    require_browser(request, request.app.state.settings)
    store = request.app.state.store
    return list_targets(store)


@router.get("/api/v1/capabilities/summary", response_model=TargetCapabilitiesResponse)
async def get_capabilities(
    request: Request,
    target_id: str = Query("local", description="目标环境 ID"),
    agent: str | None = Query(None, description="过滤特定 Agent (codex, hermes, grok, all)"),
):
    """获取指定目标的完整 Agent 能力中心数据（Skills, MCP, Plugins, Marketplaces）"""
    require_browser(request, request.app.state.settings)
    store = request.app.state.store
    try:
        return get_target_capabilities(store, target_id, agent_filter=agent)
    except Exception as exc:
        logger.error(f"Failed to fetch capabilities for {target_id}: {exc}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(exc))


# ==========================================
# 2. MCP 完整生命周期接口 (CRUD + Ping)
# ==========================================

@router.post("/api/v1/capabilities/mcp/update")
async def handle_update_mcp(request: Request, body: McpUpdateRequest) -> dict[str, Any]:
    require_browser(request, request.app.state.settings)
    store = request.app.state.store
    try:
        return update_mcp_server(store, body.target_id, body.agent, body.action, body.server)
    except Exception as exc:
        logger.error(f"MCP update error: {exc}", exc_info=True)
        raise HTTPException(status_code=400, detail=str(exc))


@router.post("/api/v1/capabilities/mcp/ping")
async def handle_ping_mcp(request: Request, body: McpPingRequest) -> dict[str, Any]:
    require_browser(request, request.app.state.settings)
    try:
        return await ping_mcp_server(body.target_id, body.server)
    except Exception as exc:
        logger.error(f"MCP ping error: {exc}", exc_info=True)
        return {"ok": False, "latency_ms": -1, "message": str(exc)}


# ==========================================
# 3. 插件生命周期接口 (Toggle + Uninstall)
# ==========================================

@router.post("/api/v1/capabilities/plugins/update")
async def handle_update_plugin(request: Request, body: PluginUpdateRequest) -> dict[str, Any]:
    require_browser(request, request.app.state.settings)
    store = request.app.state.store
    try:
        return update_plugin_state(store, body.target_id, body.agent, body.action, body.plugin_name, body.enabled)
    except Exception as exc:
        logger.error(f"Plugin update error: {exc}", exc_info=True)
        raise HTTPException(status_code=400, detail=str(exc))


# ==========================================
# 4. 市场源生命周期接口 (Add + Pull + Delete)
# ==========================================

@router.post("/api/v1/capabilities/marketplaces/update")
async def handle_update_marketplace(request: Request, body: MarketplaceUpdateRequest) -> dict[str, Any]:
    require_browser(request, request.app.state.settings)
    store = request.app.state.store
    try:
        return update_marketplace(store, body.target_id, body.agent, body.action, body.market)
    except Exception as exc:
        logger.error(f"Marketplace update error: {exc}", exc_info=True)
        raise HTTPException(status_code=400, detail=str(exc))


# ==========================================
# 5. 跨端/跨 Agent 动态分发引擎接口 (Dispatch)
# ==========================================

@router.post("/api/v1/capabilities/dispatch")
async def handle_dispatch_capability(request: Request, body: CapabilityDispatchRequest) -> dict[str, Any]:
    require_browser(request, request.app.state.settings)
    store = request.app.state.store
    try:
        return dispatch_capability(
            store=store,
            capability_type=body.capability_type,
            source_target_id=body.source_target_id,
            source_agent=body.source_agent,
            item_id=body.item_id,
            target_matrix=body.target_matrix,
            item_data=body.item_data,
        )
    except Exception as exc:
        logger.error(f"Capability dispatch failed: {exc}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(exc))
