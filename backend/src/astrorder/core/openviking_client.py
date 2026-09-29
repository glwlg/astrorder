from __future__ import annotations

import asyncio
import json
import logging
from typing import Any, Literal
import httpx

logger = logging.getLogger("astrorder.memory")

DEFAULT_OPENVIKING_URL = "http://127.0.0.1:1933"


class OpenVikingClient:
    """轻量只读与查询客户端，供星序观察面与管理端使用。零源码侵入，仅通过标准 REST API 通信。"""

    def __init__(self, base_url: str = DEFAULT_OPENVIKING_URL, api_key: str | None = None, timeout: float = 3.0):
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.timeout = timeout

    def _headers(self) -> dict[str, str]:
        headers = {"Content-Type": "application/json"}
        if self.api_key:
            headers["Authorization"] = f"Bearer {self.api_key}"
        return headers

    async def health(self) -> dict[str, Any]:
        """检查服务健康状态"""
        async with httpx.AsyncClient(timeout=self.timeout) as client:
            try:
                resp = await client.get(f"{self.base_url}/health")
                if resp.status_code == 200:
                    data = resp.json()
                    return {"healthy": True, "status": data.get("status", "ok"), "version": data.get("version")}
                return {"healthy": False, "status": f"HTTP {resp.status_code}", "version": None}
            except Exception as exc:
                return {"healthy": False, "status": "unreachable", "error": str(exc), "version": None}

    async def list_dir(self, uri: str = "viking://") -> list[dict[str, Any]]:
        """获取指定目录下的直接子节点（按层懒加载）"""
        async with httpx.AsyncClient(timeout=self.timeout) as client:
            resp = await client.get(
                f"{self.base_url}/api/v1/fs/ls",
                params={"uri": uri, "recursive": False},
                headers=self._headers(),
            )
            resp.raise_for_status()
            data = resp.json()
            return data.get("result", [])

    async def get_tree(self, uri: str = "viking://", level_limit: int = 10, node_limit: int = 5000) -> list[dict[str, Any]]:
        """获取虚拟文件目录树"""
        async with httpx.AsyncClient(timeout=10.0) as client:
            resp = await client.get(
                f"{self.base_url}/api/v1/fs/tree",
                params={"uri": uri, "level_limit": level_limit, "node_limit": node_limit},
                headers=self._headers(),
            )
            resp.raise_for_status()
            data = resp.json()
            return data.get("result", [])

    async def read_content(self, uri: str) -> str:
        """读取指定 URI 的完整 Markdown 内容"""
        async with httpx.AsyncClient(timeout=self.timeout) as client:
            resp = await client.get(
                f"{self.base_url}/api/v1/content/read",
                params={"uri": uri},
                headers=self._headers(),
            )
            resp.raise_for_status()
            data = resp.json()
            return data.get("result", "")

    async def search(self, query: str, scope: str | None = None, limit: int = 5) -> list[dict[str, Any]]:
        """语义搜索记忆与知识库"""
        payload: dict[str, Any] = {"query": query}
        if scope:
            payload["scope"] = scope
        async with httpx.AsyncClient(timeout=self.timeout) as client:
            resp = await client.post(
                f"{self.base_url}/api/v1/search/search",
                json=payload,
                headers=self._headers(),
            )
            resp.raise_for_status()
            data = resp.json()
            result = data.get("result", {})
            return result.get("memories", [])
