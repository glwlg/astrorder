from __future__ import annotations

import asyncio
import json
import uuid
from dataclasses import dataclass
from typing import Any, Callable

from .agent_registry import current_agents


class AgentApiError(Exception):
    def __init__(self, code: str, message: str):
        super().__init__(message)
        self.code = code
        self.message = message


@dataclass
class AgentContext:
    store: Any
    read_messages: Callable[[str, str, str | None, int], dict[str, Any]] | None = None
    service: Any = None
    runtime_resolver: Callable[[str], Any] | None = None


CAPABILITIES: list[dict[str, Any]] = [
    {
        "id": "catalog.list",
        "summary": "List Astrorder agent APIs on this instance.",
        "input": {},
    },
   {
       "id": "sessions.list",
       "summary": "List sessions visible to this Astrorder instance.",
        "input": {
            "agent_id": "optional agent id filter",
            "limit": "optional maximum returned count (default 30, max 200)",
            "offset": "optional integer offset for pagination (default 0)",
            "ephemeral": "optional boolean filter to include only temporary or persistent sessions",
        },
   },
    {
        "id": "sessions.search",
        "summary": "Search sessions by title, workspace, project, or id.",
        "input": {"q": "query", "agent_id": "optional", "limit": "optional maximum returned count (default 20, max 50)"},
    },
    {
        "id": "sessions.read",
        "summary": "Read a session and its messages. Same-agent native ids work; cross-agent and cross-machine use the Astrorder key.",
        "input": {"key": "agent_id::session_id", "agent_id": "optional", "session_id": "optional", "before": "optional cursor for older messages", "limit": "optional maximum messages count (default 20, max 50)"},
    },
    {
        "id": "agents.list",
        "summary": "List agents on this instance, including remote SSH agents.",
        "input": {},
    },
    {
        "id": "machines.list",
        "summary": "List local and SSH machines this Astrorder instance can reach.",
        "input": {},
    },
    {
        "id": "projects.list",
        "summary": "List projects grouped by this Astrorder instance.",
        "input": {},
    },
   {
       "id": "sessions.create",
       "summary": "Create a new session under a specified agent.",
        "input": {
            "agent_id": "target agent id",
            "title": "optional session title",
            "workspace": "optional workspace path",
            "ephemeral": "optional boolean to mark as temporary/probe/review session",
            "parent_key": "optional parent session key (agent_id::session_id) for DAG tracing",
        },
   },
    {
        "id": "sessions.delete",
        "summary": "Delete one or more sessions and clean up their runtime resources.",
        "input": {
            "key": "session key to delete (agent_id::session_id)",
            "keys": "optional list of session keys for batch deletion",
            "agent_id": "optional agent id if key is omitted",
            "session_id": "optional session id if key is omitted",
        },
    },
    {
        "id": "sessions.tree",
        "summary": "Query session hierarchy and satellite DAG topology starting from a root session key, or list all mission swarm trees.",
        "input": {
            "key": "optional root session key (agent_id::session_id)",
        },
    },
   {
       "id": "sessions.send",
       "summary": "Send a prompt instruction to a session.",
        "input": {"key": "agent_id::session_id", "text": "instruction text to send", "agent_id": "optional", "session_id": "optional"},
    },
    {
        "id": "sessions.stop",
        "summary": "Stop or interrupt a running session turn.",
        "input": {"key": "agent_id::session_id", "agent_id": "optional", "session_id": "optional"},
    },
    {
        "id": "plugins.list",
        "summary": "List Astrorder plugins and artifact viewers with their status and capabilities.",
        "input": {},
    },
    {
        "id": "plugins.configure",
        "summary": "Configure or toggle an Astrorder plugin.",
        "input": {"plugin_id": "target plugin ID", "enabled": "optional boolean", "config": "optional key-value settings"},
    },
    {
        "id": "plugins.open",
        "summary": "Instruct Astrorder UI to open a specific plugin view or file artifact in the workspace sidecar panel.",
        "input": {
            "plugin_id": "plugin to open: terminal, browser, gitdiff, filetree, sidechat, agentgraph, monaco, drawio, mermaid, excalidraw, diff, three, html",
            "session_key": "optional session key (agent_id::session_id)",
            "path": "optional file or directory path for filetree/monaco/diff/drawio/three/html",
            "url": "optional url for browser or html",
            "title": "optional custom tab title"
        },
    },
    {
        "id": "plugins.close",
        "summary": "Instruct Astrorder UI to close a plugin tab or collapse the sidecar panel.",
        "input": {
            "plugin_id": "optional plugin ID to close",
            "tab_id": "optional specific tab ID to close",
            "collapse": "optional boolean to collapse the entire sidecar panel"
        },
    },
    {
        "id": "browser.run",
        "summary": "Run a bounded browser task in this Astrorder session's visible Edge tab using Jev. Later calls continue that tab and login state unless reset is true.",
        "input": {
            "url": "initial http/https page URL",
            "goal": "complete browser goal with a visible completion condition",
            "inputs": "optional object mapping visible field labels to exact values to type",
            "max_steps": "optional action limit (default 30, maximum 60)",
            "reset": "optional boolean; replace this session's browser tab without affecting other sessions",
            "include_screenshot": "optional boolean; return the final browser screenshot to the agent",
            "session_key": "calling Astrorder session key (agent_id::session_id); required when more than one session is active",
        },
    },
    {
        "id": "browser.screenshot",
        "summary": "Capture this Astrorder session's browser tab and return it as an image.",
        "input": {"session_key": "calling Astrorder session key (agent_id::session_id)"},
    },
    {
        "id": "browser.cdp",
        "summary": "Call a Chrome DevTools Protocol method on this Astrorder session's own browser tab.",
        "input": {
            "session_key": "calling Astrorder session key (agent_id::session_id)",
            "method": "CDP method in Runtime, DOM, CSS, Console, Log, Network, Performance, or Page",
            "params": "optional CDP parameters object",
        },
    },
    {
        "id": "machines.dispatch",
        "summary": "Dispatch a task or delegate session creation onto a specific target machine (local or SSH remote host).",
        "input": {
            "machine_id": "target machine ID (from machines_list: 'local' or 'ssh-...')",
            "agent_kind": "optional target agent kind on that machine ('codex', 'hermes', 'grok')",
            "title": "optional session title",
            "workspace": "optional workspace directory on that machine",
            "prompt": "optional initial prompt text to send immediately after creation"
        },
    },
    {
        "id": "monitor.sessions.add",
        "summary": "Add one or more sessions into the Astrorder Monitor room dashboard for live visual tracking.",
        "input": {
            "key": "session key (agent_id::session_id)",
            "keys": "optional list of session keys",
            "agent_id": "optional",
            "session_id": "optional"
        },
    },
    {
        "id": "monitor.sessions.remove",
        "summary": "Remove a session from the Astrorder Monitor room dashboard.",
        "input": {
            "key": "session key (agent_id::session_id)",
            "agent_id": "optional",
            "session_id": "optional"
        },
    },
    {
        "id": "monitor.layout.set",
        "summary": "Configure Astrorder Monitor room grid layout columns (1, 2, 3, or 4).",
        "input": {
            "columns": "integer 1, 2, 3, or 4"
        },
    },
    {
        "id": "blackboard.get",
        "summary": "【星序作战黑板-读取】读取共享任务状态、规格事实、共享记忆或工件（Blackboard）。当用户提到“查看黑板”、“读取黑板”、“黑板里有什么”时，直接调用本工具。严禁使用 Python 脚本读写源码或数据库。Read shared mission state, specifications or facts from the Astrorder Blackboard.",
        "input": {
            "session_key": "专属会话作用域标识（传入上下文提示中的会话 ID，如单聊 'agent_id::session_id'、群聊 'group::group_id' 或星图 'swarm::root_key'）",
            "key": "specific blackboard key, or omit to list all keys",
            "namespace": "optional custom namespace scope override (default: resolved from session_key)",
        },
    },
    {
        "id": "blackboard.set",
        "summary": "【星序作战黑板-写入/发布】将任务状态、结构化事实、备忘录、分析结论或工件写入星序作战黑板（Blackboard）。当用户指示“写到黑板”、“记入黑板”、“更新黑板”、“同步到黑板”时，必须直接调用本工具。强烈建议在 value 中显式声明 component 属性以呈现精美 Generative UI（如 'StepTimeline' 流水线、'Checklist' 验收清单、'DataTable' 表格、'MetricGrid' 指标卡、'GomokuBoard' 棋盘），未声明时系统将通过 Jev 决策模型自动推断选择最优组件补全。",
        "input": {
            "session_key": "专属会话作用域标识（传入上下文提示中的会话 ID，如单聊 'agent_id::session_id'、群聊 'group::group_id' 或星图 'swarm::root_key'）",
            "key": "blackboard key",
            "value": "data payload (object, array, string). Recommend specifying {'component': 'StepTimeline'|'Checklist'|'DataTable'|'MetricGrid'|..., ...} for rich UI presentation",
            "namespace": "optional custom namespace scope override (default: resolved from session_key)",
        },
    },
    {
        "id": "blackboard.delete",
        "summary": "【星序作战黑板-删除】从作战黑板中删除指定的 key 或清空当前命名空间。Delete a key from the Astrorder Blackboard.",
        "input": {
            "session_key": "专属会话作用域标识（传入上下文提示中的会话 ID，如单聊 'agent_id::session_id'、群聊 'group::group_id' 或星图 'swarm::root_key'）",
            "key": "blackboard key to delete, or '*' / clean_namespace: true to wipe space",
            "namespace": "optional custom namespace scope override (default: resolved from session_key)",
        },
    },
    {
        "id": "blackboard.components.list",
        "summary": "List all supported Generative UI components for Blackboard with their IDs, categories and summaries.",
        "input": {},
    },
    {
        "id": "blackboard.auto_render",
        "summary": "Analyze unformatted text or summary and use Jev decision model to automatically determine the best json-render UI component, formatting it directly to Blackboard.",
        "input": {
            "key": "blackboard key to publish to",
            "content": "raw mission status, log, test result, or facts to render",
            "title": "optional card title",
            "namespace": "optional namespace scope (default automatically resolved)"
        },
    },
    {
        "id": "blackboard.component.schema",
        "summary": "Query exact JSON schema, required fields and copyable example payload for a specific Blackboard Generative UI component.",
        "input": {
            "component_id": "component name, e.g. 'StepTimeline', 'MetricGrid', 'DataTable', 'ApiEndpointsCard', 'ResourceUsageBar', 'TestReport', 'CveSecurityReport', 'DiffViewer', 'Checklist', 'TerminalLog', 'ArchitectureFlow', 'StatusCard', 'MultiNodeClusterSummary', 'HostNodeTelemetryCard', 'MissionSpecCard', 'GomokuBoard'"
        },
    },
    {
        "id": "swarm.milestone.declare",
        "summary": "Declare an expected mission milestone or dependency gate, with optional automatic wake-up instructions once resolved.",
        "input": {
            "milestone_id": "unique milestone identifier, e.g. 'backend_api_ready'",
            "title": "milestone description or goal",
            "wake_session_key": "optional session key to automatically wake up once resolved",
            "wake_prompt": "optional prompt instruction to send to wake_session_key once resolved",
            "metadata": "optional payload/contract object"
        },
    },
    {
        "id": "swarm.milestone.resolve",
        "summary": "Mark a mission milestone as resolved/completed, automatically waking any blocked sessions and updating the DAG.",
        "input": {
            "milestone_id": "unique milestone identifier",
            "result": "optional result or contract data to publish to the blackboard"
        },
    },
    {
        "id": "swarm.milestone.list",
        "summary": "List all active, pending, and resolved mission milestones.",
        "input": {},
    },
    {
        "id": "swarm.telemetry.report",
        "summary": "Submit a structured telemetry or progress report for the current session/worker.",
        "input": {
            "progress": "integer 0 to 100 percentage",
            "phase": "current phase string, e.g. 'writing_code', 'running_tests', 'deploying'",
            "status": "health status: 'ok', 'warning', 'blocked', or 'completed'",
            "summary": "brief one-line status summary",
            "artifacts": "optional list of produced file paths or URLs",
            "key": "optional session key, defaults to current session context"
        },
    },
    {
        "id": "swarm.telemetry.get",
        "summary": "Read structured telemetry reports of sessions without reading entire chat histories.",
        "input": {
            "key": "specific session key (agent_id::session_id), or omit to get all worker telemetry reports"
        },
    },
    {
        "id": "swarm.sos.escalate",
        "summary": "Escalate a blocker, conflict, or failure to the queen/parent orchestrator session and broadcast emergency SOS signal.",
        "input": {
            "reason": "concrete description of the blocker, e.g. 'Dependency conflict or port 3000 collision'",
            "context": "optional diagnostic details, logs, or error snippet",
            "key": "optional session key of the blocked worker, defaults to current context",
            "target_session_key": "optional queen/parent session key to notify directly"
        },
    },
    {
        "id": "swarm.sos.list",
        "summary": "List active and unresolved SOS escalation alerts across the swarm.",
        "input": {},
    },
    {
        "id": "swarm.sos.resolve",
        "summary": "Resolve an SOS escalation after providing rescue assistance or unblocking the worker.",
        "input": {
            "sos_id": "escalation alert ID to resolve",
            "resolution": "optional resolution notes"
        },
    },
    {
        "id": "swarm.lock.acquire",
        "summary": "Acquire an exclusive lock on a shared resource (e.g. 'git:repo', 'port:3000', 'db:migration') to prevent multi-agent collision.",
        "input": {
            "resource": "resource identifier to lock, e.g. 'git:main' or 'db:schema'",
            "ttl_seconds": "optional lock expiration in seconds (default 300, max 3600)",
            "owner_key": "optional owner session key, defaults to current session context"
        },
    },
    {
        "id": "swarm.lock.release",
        "summary": "Release a previously acquired exclusive resource lock.",
        "input": {
            "resource": "resource identifier to release",
            "owner_key": "optional owner session key"
        },
    },
    {
        "id": "swarm.lock.list",
        "summary": "List all active resource locks across the swarm.",
        "input": {},
    },
]


def parse_session_key(payload: dict[str, Any]) -> tuple[str, str]:
    key = payload.get("key")
    if isinstance(key, str) and "::" in key:
        agent_id, session_id = key.split("::", 1)
        if agent_id and session_id:
            return agent_id, session_id
    agent_id = payload.get("agent_id")
    session_id = payload.get("session_id")
    if isinstance(agent_id, str) and agent_id and isinstance(session_id, str) and session_id:
        return agent_id, session_id
    raise AgentApiError("invalid_input", "Provide key (agent_id::session_id) or agent_id and session_id")


def session_key(session: dict[str, Any]) -> str:
    return f"{session['agent_id']}::{session['id']}"


def public_session(session: dict[str, Any]) -> dict[str, Any]:
    parent_key = None
    parent_aid = session.get("parent_agent_id")
    parent_sid = session.get("parent_session_id")
    if parent_aid and parent_sid:
        parent_key = f"{parent_aid}::{parent_sid}"
    elif parent_sid:
        parent_key = f"{session.get('agent_id')}::{parent_sid}"

    return {
        "key": session_key(session),
        "id": session.get("id"),
        "agent_id": session.get("agent_id"),
        "title": session.get("title") or "",
        "status": session.get("status"),
        "updated_at": session.get("updated_at"),
        "project_name": session.get("project_name"),
        "parent_session_key": parent_key,
        "parent_session_id": parent_sid,
        "parent_agent_id": parent_aid,
        "ephemeral": bool(session.get("ephemeral")),
    }


def public_agent(agent: dict[str, Any]) -> dict[str, Any]:
    return {
        "id": agent.get("id"),
        "kind": agent.get("kind"),
        "name": agent.get("name"),
        "status": agent.get("status"),
        "source_id": agent.get("source_id"),
        "connection_id": agent.get("connection_id"),
        "runtime_id": agent.get("runtime_id"),
        "capabilities": list(agent.get("capabilities") or []),
    }


def public_message(item: dict[str, Any]) -> dict[str, Any]:
    text = item.get("text") or ""
    # Single message text safety truncation to prevent blowout
    if len(text) > 8000:
        text = text[:8000] + "... (truncated)"
    return {
        "id": item.get("id"),
        "role": item.get("role"),
        "kind": item.get("kind"),
        "text": text,
        "created_at": item.get("created_at"),
    }


def _catalog(_payload: dict[str, Any], _ctx: AgentContext) -> dict[str, Any]:
    return {"items": CAPABILITIES}


def _sessions_list(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    agent_id = payload.get("agent_id")
    if agent_id is not None and not isinstance(agent_id, str):
        raise AgentApiError("invalid_input", "agent_id must be a string")
    limit = payload.get("limit", 30)
    if not isinstance(limit, int):
        try:
            limit = int(limit)
        except (TypeError, ValueError) as exc:
            raise AgentApiError("invalid_input", "limit must be an integer") from exc
    limit = max(1, min(limit, 200))
    offset = payload.get("offset", 0)
    if not isinstance(offset, int):
        try:
            offset = int(offset)
        except (TypeError, ValueError) as exc:
            raise AgentApiError("invalid_input", "offset must be an integer") from exc
    offset = max(0, offset)
    ephemeral_filter = payload.get("ephemeral")
    if ephemeral_filter is not None and not isinstance(ephemeral_filter, bool):
        if str(ephemeral_filter).lower() in ("true", "1"):
            ephemeral_filter = True
        elif str(ephemeral_filter).lower() in ("false", "0"):
            ephemeral_filter = False
        else:
            raise AgentApiError("invalid_input", "ephemeral must be a boolean")
    rows = ctx.store.list_sessions(agent_id if isinstance(agent_id, str) and agent_id else None)
    if ephemeral_filter is not None:
        rows = [r for r in rows if bool(r.get("ephemeral")) == ephemeral_filter]
    total = len(rows)
    sliced = rows[offset:offset + limit]
    return {"items": [public_session(row) for row in sliced], "total": total, "offset": offset, "limit": limit, "has_more": total > (offset + limit)}


def _sessions_search(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    query = payload.get("q")
    if not isinstance(query, str) or not query.strip():
        raise AgentApiError("invalid_input", "q is required")
    needle = query.strip().casefold()
    agent_id = payload.get("agent_id") if isinstance(payload.get("agent_id"), str) else None
    limit = payload.get("limit", 20)
    if not isinstance(limit, int):
        try:
            limit = int(limit)
        except (TypeError, ValueError) as exc:
            raise AgentApiError("invalid_input", "limit must be an integer") from exc
    limit = max(1, min(limit, 50))
    rows = ctx.store.list_sessions(agent_id or None)
    matched = []
    for row in rows:
        hay = " ".join(
            str(part or "")
            for part in (row.get("title"), row.get("id"), row.get("agent_id"), row.get("workspace"), row.get("project_name"), row.get("source_id"))
        ).casefold()
        if needle in hay:
            matched.append(public_session(row))
    total = len(matched)
    sliced = matched[:limit]
    return {"items": sliced, "q": query.strip(), "total": total, "has_more": total > limit}


def _sessions_read(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    agent_id, session_id = parse_session_key(payload)
    session = ctx.store.get_session(agent_id, session_id)
    if session is None:
        raise AgentApiError("not_found", "Session was not found")
    limit = payload.get("limit", 20)
    if not isinstance(limit, int):
        try:
            limit = int(limit)
        except (TypeError, ValueError) as exc:
            raise AgentApiError("invalid_input", "limit must be an integer") from exc
    limit = max(1, min(limit, 50))
    before = payload.get("before") if isinstance(payload.get("before"), str) else None
    items: list[dict[str, Any]] = []
    next_cursor = None
    if ctx.read_messages is not None:
        page = ctx.read_messages(agent_id, session_id, before, limit)
        raw_items = page.get("items") if isinstance(page, dict) else None
        if isinstance(raw_items, list):
            items = [public_message(item) for item in raw_items if isinstance(item, dict)]
            next_cursor = page.get("next")
    if not items:
        stored, next_cursor = ctx.store.list_messages(agent_id, session_id, before, limit)
        items = [public_message(item) for item in stored]
    return {
        "session": public_session(session),
        "items": items,
        "next_cursor": next_cursor,
        "has_more": bool(next_cursor),
        "count": len(items),
    }


def _agents_list(_payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    return {"items": [public_agent(agent) for agent in current_agents(ctx.store)]}


def _machines_list(_payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    local = {"id": "local", "kind": "local", "display_name": "本机", "state": "ready"}
    remote = []
    listing = getattr(ctx.store, "list_ssh_connections", None)
    if callable(listing):
        for row in listing():
            settings = row.get("settings") or {}
            remote.append(
                {
                    "id": row.get("id"),
                    "kind": "ssh",
                    "display_name": row.get("display_name"),
                    "state": row.get("state"),
                    "host": settings.get("host"),
                    "agent_id": row.get("agent_id"),
                }
            )
    return {"items": [local, *remote]}


def _projects_list(_payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    listing = getattr(ctx.store, "list_projects", None)
    rows = listing() if callable(listing) else []
    return {
        "items": [
            {
                "id": row.get("id"),
                "project_name": row.get("project_name"),
                "workspace": row.get("workspace"),
                "agent_id": row.get("agent_id"),
                "source_id": row.get("source_id"),
                "connection_id": row.get("connection_id"),
                "session_count": row.get("session_count"),
            }
            for row in rows
        ]
    }


KNOWN_PLUGINS: list[dict[str, Any]] = [
    {"id": "blackboard", "title": "黑板", "description": "多 Agent 任务协同共享记忆、规格参数与产出物实时看板", "category": "collaboration", "extensions": [".json", ".yaml", ".yml", ".md"]},
    {"id": "drawio", "title": "Draw.io 架构图", "description": "交互式查看、编辑和绘制 .drawio 架构设计与流程图", "category": "design", "extensions": [".drawio", ".drawio.xml"]},
    {"id": "mermaid", "title": "Mermaid 图表", "description": "实时渲染 Mermaid 流程图、时序图、类图与状态机", "category": "diagram", "extensions": [".mmd", ".mermaid"]},
    {"id": "excalidraw", "title": "Excalidraw 手绘白板", "description": "手绘草图、原型交互画板查看与编辑", "category": "whiteboard", "extensions": [".excalidraw"]},
    {"id": "diff", "title": "文本与补丁对比", "description": "精准的 Side-by-Side 差异与补丁审查", "category": "development", "extensions": [".diff", ".patch"]},
    {"id": "three", "title": "3D 模型预览", "description": "实时渲染与三维交互预览 3D 网格模型", "category": "media", "extensions": [".gltf", ".glb", ".obj", ".stl"]},
    {"id": "html", "title": "Web 页面沙箱预览", "description": "安全的隔离式 HTML/SVG/Web 页面实时渲染与交互", "category": "web", "extensions": [".html", ".htm", ".svg"]},
    {"id": "terminal", "title": "交互式终端", "description": "xterm 原生 PTY 命令输出与交互", "category": "system", "extensions": [".log", ".term"]},
    {"id": "monaco", "title": "Monaco 代码编辑器", "description": "VSCode 同款代码高亮、大文件查看与在线修改", "category": "development", "extensions": [".ts", ".tsx", ".js", ".jsx", ".py", ".json", ".yaml", ".yml", ".md", ".rs", ".go", ".sql", ".sh", ".bash", ".ps1"]},
    {"id": "filetree", "title": "目录结构浏览器", "description": "文件树全景概览与快速导航", "category": "navigation", "extensions": []},
    {"id": "gitdiff", "title": "Git 变更集审查", "description": "工作区未暂存与已暂存变更的可视化审查面板", "category": "vcs", "extensions": []},
    {"id": "sidechat", "title": "副对话窗口", "description": "针对当前工件或长文本的局部深入多轮探讨", "category": "chat", "extensions": []},
    {"id": "agentgraph", "title": "Agent 协作拓扑", "description": "多 Agent 任务协同拓扑与调用链可视化", "category": "orchestration", "extensions": []},
]


def _sessions_create(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    agent_id = payload.get("agent_id")
    if not isinstance(agent_id, str) or not agent_id.strip():
        raise AgentApiError("invalid_input", "agent_id is required")
    agent_id = agent_id.strip()
    agent = ctx.store.get_agent(agent_id)
    if agent is None:
        raise AgentApiError("not_found", f"Agent '{agent_id}' was not found")
    title = payload.get("title") or "新会话"
    workspace = payload.get("workspace")
    parent_sid = payload.get("parent_session_id")
    parent_aid = payload.get("parent_agent_id")
    if payload.get("parent_key") and "::" in str(payload.get("parent_key")):
        parent_aid, parent_sid = str(payload.get("parent_key")).split("::", 1)
    ephemeral = False
    if "ephemeral" in payload:
        raw_eph = payload.get("ephemeral")
        ephemeral = True if raw_eph in (True, "true", "True", 1, "1") else False

    data: dict[str, Any] = {}
    if ctx.runtime_resolver is not None:
        runtime = ctx.runtime_resolver(agent_id)
        try:
            create = getattr(runtime, "create", None)
            if callable(create):
                data = create(workspace, title, parent_session_id=parent_sid) if parent_sid else create(workspace, title)
            else:
                create_session = getattr(runtime, "create_session", None)
                if callable(create_session):
                    data = create_session(workspace=workspace, title=title)
        except Exception:
            pass

    if not data or not data.get("id"):
        sid = str(uuid.uuid4())
        data = {
            "id": sid,
            "agent_id": agent_id,
            "title": title,
            "workspace": workspace,
            "status": "idle",
            "source_id": agent.get("source_id") or agent_id,
            "source_session_id": sid,
            "history_state": "available",
            "control_state": "owned",
            "updated_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        }

    if parent_sid:
        data["parent_session_id"] = parent_sid
        data["handoff_from_session_id"] = parent_sid
    if parent_aid:
        data["parent_agent_id"] = parent_aid
        data["handoff_from_agent_id"] = parent_aid
    if not data.get("source_session_id"):
        data["source_session_id"] = data.get("id")
    if not data.get("control_state"):
        data["control_state"] = "owned"
    if not data.get("history_state"):
        data["history_state"] = "available"
    if "updated_at" not in data:
        data["updated_at"] = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
    data["ephemeral"] = ephemeral

    canonical = ctx.store.upsert_session(data)
    session_namespace = f"session:{canonical['agent_id']}::{canonical['id']}"
    if parent_sid:
        root_aid = str(parent_aid or agent_id)
        root_sid = str(parent_sid)
        visited: set[str] = set()
        while f"{root_aid}::{root_sid}" not in visited:
            visited.add(f"{root_aid}::{root_sid}")
            parent = ctx.store.get_session(root_aid, root_sid)
            if not parent or not parent.get("parent_session_id"):
                break
            root_aid = str(parent.get("parent_agent_id") or root_aid)
            root_sid = str(parent["parent_session_id"])
        session_namespace = f"swarm:{root_aid}::{root_sid}"
        ctx.store.set_session_blackboard_namespace(root_aid, root_sid, session_namespace)
        ctx.store.set_session_blackboard_namespace(str(parent_aid or agent_id), str(parent_sid), session_namespace)
    ctx.store.set_session_blackboard_namespace(canonical["agent_id"], canonical["id"], session_namespace)
    if ctx.service is not None:
        ctx.service._server_event("session.upsert", agent_id=canonical["agent_id"], session_id=canonical["id"], data=canonical)
    return {"session": public_session(canonical)}


def _sessions_send(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    agent_id, session_id = parse_session_key(payload)
    session = ctx.store.get_session(agent_id, session_id)
    if session is None:
        raise AgentApiError("not_found", "Session was not found")
    text = payload.get("text")
    if not isinstance(text, str) or not text.strip():
        raise AgentApiError("invalid_input", "text is required")
    text = text.strip()

    command_id = str(uuid.uuid4())
    cmd_payload = {
        "id": command_id,
        "agent_id": agent_id,
        "session_id": session_id,
        "action": "send",
        "text": text,
        "attachment_ids": [],
        "target_id": None,
    }

    if ctx.service is not None:
        from .service import CommandRejected
        try:
            loop = asyncio.get_running_loop()
        except RuntimeError:
            loop = None
        try:
            if loop and loop.is_running():
                future = asyncio.run_coroutine_threadsafe(ctx.service.submit_browser_command(cmd_payload), loop)
                command = future.result(timeout=15)
            else:
                command = asyncio.run(ctx.service.submit_browser_command(cmd_payload))
            return {"command_id": command_id, "state": command.get("state", "received"), "session_key": session_key(session)}
        except CommandRejected as exc:
            raise AgentApiError("command_rejected", exc.detail) from None

    # Direct store fallback
    created, _ = ctx.store.create_command(command=cmd_payload, attachments=[], initial_state="received")
    return {"command_id": command_id, "state": created.get("state", "received"), "session_key": session_key(session)}


def _sessions_stop(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    agent_id, session_id = parse_session_key(payload)
    session = ctx.store.get_session(agent_id, session_id)
    if session is None:
        raise AgentApiError("not_found", "Session was not found")

    command_id = str(uuid.uuid4())
    cmd_payload = {
        "id": command_id,
        "agent_id": agent_id,
        "session_id": session_id,
        "action": "stop",
        "text": "",
        "attachment_ids": [],
        "target_id": session_id,
    }

    if ctx.service is not None:
        from .service import CommandRejected
        try:
            loop = asyncio.get_running_loop()
        except RuntimeError:
            loop = None
        try:
            if loop and loop.is_running():
                future = asyncio.run_coroutine_threadsafe(ctx.service.submit_browser_command(cmd_payload), loop)
                command = future.result(timeout=10)
            else:
                command = asyncio.run(ctx.service.submit_browser_command(cmd_payload))
            return {"command_id": command_id, "state": command.get("state", "received"), "session_key": session_key(session)}
        except CommandRejected as exc:
            raise AgentApiError("command_rejected", exc.detail) from None

    created, _ = ctx.store.create_command(command=cmd_payload, attachments=[], initial_state="received")
    return {"command_id": command_id, "state": created.get("state", "received"), "session_key": session_key(session)}


def _sessions_delete(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    keys = payload.get("keys")
    targets: list[tuple[str, str]] = []
    if isinstance(keys, list):
        for k in keys:
            if isinstance(k, str) and "::" in k:
                aid, sid = k.split("::", 1)
                targets.append((aid.strip(), sid.strip()))
    elif payload.get("key") or (payload.get("agent_id") and payload.get("session_id")):
        aid, sid = parse_session_key(payload)
        targets.append((aid, sid))
    else:
        raise AgentApiError("invalid_input", "key, keys, or agent_id+session_id is required")

    deleted: list[dict[str, Any]] = []
    failed: list[dict[str, Any]] = []
    for aid, sid in targets:
        if not aid or not sid:
            continue
        if ctx.store.get_session(aid, sid) is None:
            failed.append({"agent_id": aid, "session_id": sid, "error": "not_found"})
            continue

        if ctx.runtime_resolver is not None:
            runtime = ctx.runtime_resolver(aid)
            for name in ("mutate", "mutate_session"):
                mutate = getattr(runtime, name, None)
                if callable(mutate):
                    try:
                        mutate(sid, None)
                    except Exception:
                        pass
                    break

        success = False
        if ctx.service is not None and hasattr(ctx.service, "delete_session"):
            try:
                success = ctx.service.delete_session(aid, sid)
            except Exception as exc:
                failed.append({"agent_id": aid, "session_id": sid, "error": str(exc)})
                continue
        elif ctx.store is not None:
            try:
                success = ctx.store.delete_session(aid, sid)
            except Exception as exc:
                failed.append({"agent_id": aid, "session_id": sid, "error": str(exc)})
                continue

        if success:
            deleted.append({"agent_id": aid, "session_id": sid, "key": f"{aid}::{sid}"})
        else:
            failed.append({"agent_id": aid, "session_id": sid, "error": "delete_failed"})

    return {
        "ok": len(failed) == 0,
        "deleted": deleted,
        "failed": failed,
        "count": len(deleted),
    }


def _sessions_tree(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    root_key = payload.get("key")
    if root_key is not None and not isinstance(root_key, str):
        raise AgentApiError("invalid_input", "key must be a string")

    rows = ctx.store.list_sessions()
    sessions = [public_session(r) for r in rows]
    by_key = {s["key"]: s for s in sessions}
    children_map: dict[str, list[dict[str, Any]]] = {}

    for s in sessions:
        pkey = s.get("parent_session_key")
        if pkey:
            children_map.setdefault(pkey, []).append(s)

    def attach_children(node: dict[str, Any], depth: int = 0) -> dict[str, Any]:
        k = node["key"]
        children = children_map.get(k, [])
        sub_nodes = [attach_children(dict(c), depth + 1) for c in children] if depth < 10 else []
        return {
            **node,
            "children": sub_nodes,
            "satellite_count": len(children),
        }

    if root_key:
        root = by_key.get(root_key)
        if root is None:
            raise AgentApiError("not_found", f"Session '{root_key}' was not found")
        tree = attach_children(dict(root))
        return {"ok": True, "tree": tree}

    roots = [s for s in sessions if not s.get("parent_session_key") or s.get("parent_session_key") not in by_key]
    trees = [attach_children(dict(r)) for r in roots]
    swarm_trees = [t for t in trees if t.get("satellite_count", 0) > 0]
    return {
        "ok": True,
        "trees": trees,
        "swarm_trees": swarm_trees,
        "total_trees": len(trees),
        "swarm_count": len(swarm_trees),
    }


def _plugins_list(_payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    settings: dict[str, Any] = {}
    try:
        from sqlalchemy import select
        from .models import WorkspacePreferenceRow
        with ctx.store.session() as db:
            row = db.get(WorkspacePreferenceRow, "plugins:settings")
            if row and row.value:
                settings = row.value if isinstance(row.value, dict) else json.loads(str(row.value))
    except Exception:
        pass


    items = []
    for p in KNOWN_PLUGINS:
        pid = p["id"]
        override = settings.get(pid) or {}
        items.append({
            **p,
            "enabled": override.get("enabled", True),
            "config": override.get("config", {}),
        })
    return {"items": items, "count": len(items)}


def _plugins_configure(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    plugin_id = payload.get("plugin_id")
    if not isinstance(plugin_id, str) or not plugin_id.strip():
        raise AgentApiError("invalid_input", "plugin_id is required")
    plugin_id = plugin_id.strip()
    known = next((p for p in KNOWN_PLUGINS if p["id"] == plugin_id), None)
    if not known:
        raise AgentApiError("not_found", f"Plugin '{plugin_id}' was not found in registered plugins")

    settings: dict[str, Any] = {}
    try:
        from sqlalchemy import select
        from sqlalchemy.dialects.sqlite import insert
        from .models import WorkspacePreferenceRow
        with ctx.store.session() as db:
            row = db.get(WorkspacePreferenceRow, "plugins:settings")
            if row and row.value:
                settings = row.value if isinstance(row.value, dict) else json.loads(str(row.value))
            current = settings.get(plugin_id) or {"enabled": True, "config": {}}
            if "enabled" in payload:
                current["enabled"] = bool(payload["enabled"])
            if "config" in payload and isinstance(payload["config"], dict):
                current["config"] = {**current.get("config", {}), **payload["config"]}
            settings[plugin_id] = current
            statement = insert(WorkspacePreferenceRow).values(key="plugins:settings", value=settings)
            statement = statement.on_conflict_do_update(index_elements=["key"], set_={"value": settings})
            db.execute(statement)
    except Exception as exc:
        raise AgentApiError("database_error", f"Failed to persist plugin settings: {exc}") from exc

    return {"plugin": {**known, **current}, "ok": True}


def _plugins_open(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    plugin_id = payload.get("plugin_id")
    if not isinstance(plugin_id, str) or not plugin_id.strip():
        raise AgentApiError("invalid_input", "plugin_id is required")
    plugin_id = plugin_id.strip()

    session_key_val = payload.get("session_key")
    agent_id = None
    session_id = None
    if isinstance(session_key_val, str) and "::" in session_key_val:
        agent_id, session_id = session_key_val.split("::", 1)
    elif payload.get("session_id"):
        session_id = str(payload.get("session_id"))
        agent_id = str(payload.get("agent_id") or "")
    else:
        agent_id = str(payload.get("agent_id") or payload.get("caller_agent_id") or "") or None

    raw_path = payload.get("path")
    norm_path = str(raw_path).replace("\\", "/") if raw_path else None
    url_val = payload.get("url")
    # 如果打开 html 插件并传了本地路径，自动构建可直接预览的 readUrl
    if (plugin_id == "html" or (norm_path and norm_path.lower().endswith((".html", ".htm")))) and norm_path and not url_val:
        url_val = f"/api/v1/files/raw?path={norm_path}"

    event_data = {
        "action": "open",
        "plugin_id": plugin_id,
        "agent_id": agent_id,
        "session_id": session_id,
        "path": norm_path,
        "url": url_val,
        "title": payload.get("title"),
    }
    if ctx.service is not None:
        ctx.service._server_event("sidecar.plugin.control", agent_id=agent_id, session_id=session_id, data=event_data)

    return {"ok": True, "event": event_data}


def _plugins_close(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    event_data = {
        "action": "close",
        "plugin_id": payload.get("plugin_id"),
        "tab_id": payload.get("tab_id"),
        "collapse": bool(payload.get("collapse")),
    }
    if ctx.service is not None:
        ctx.service._server_event("sidecar.plugin.control", agent_id=None, session_id=None, data=event_data)

    return {"ok": True, "event": event_data}


def _machines_dispatch(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    machine_id = payload.get("machine_id")
    if not isinstance(machine_id, str) or not machine_id.strip():
        raise AgentApiError("invalid_input", "machine_id is required")
    machine_id = machine_id.strip()

    agent_kind = payload.get("agent_kind")
    if agent_kind is not None and isinstance(agent_kind, str):
        agent_kind = agent_kind.strip().lower()

    agents = current_agents(ctx.store)
    candidate = None
    if machine_id == "local":
        for a in agents:
            if not str(a.get("id") or "").startswith("ssh-"):
                if not agent_kind or a.get("kind") == agent_kind:
                    candidate = a
                    break
    else:
        # Match by connection_id or agent_id
        for a in agents:
            cid = a.get("connection_id")
            aid = a.get("id")
            if cid == machine_id or aid == machine_id or aid.endswith(machine_id):
                if not agent_kind or a.get("kind") == agent_kind:
                    candidate = a
                    break

    if candidate is None:
        raise AgentApiError("not_found", f"No matching agent found on machine '{machine_id}'")

    # Create session on target agent
    caller_agent_id = payload.get("caller_agent_id")
    caller_session_id = payload.get("caller_session_id")
    parent_sid = payload.get("parent_session_id") or caller_session_id
    parent_aid = payload.get("parent_agent_id") or caller_agent_id
    if payload.get("parent_key") and "::" in str(payload.get("parent_key")):
        parent_aid, parent_sid = str(payload.get("parent_key")).split("::", 1)

    create_payload = {
        "agent_id": candidate["id"],
        "title": payload.get("title") or f"远程派生任务 ({machine_id})",
        "workspace": payload.get("workspace"),
        "parent_session_id": parent_sid,
        "parent_agent_id": parent_aid,
        "parent_key": f"{parent_aid}::{parent_sid}" if parent_aid and parent_sid else payload.get("parent_key"),
    }
    created = _sessions_create(create_payload, ctx)
    created_session = created.get("session") or {}

    # If initial prompt is provided, dispatch it
    prompt = payload.get("prompt")
    sent_result = None
    if isinstance(prompt, str) and prompt.strip():
        sent_result = _sessions_send({"key": created_session.get("key"), "text": prompt.strip()}, ctx)

    return {
        "ok": True,
        "machine_id": machine_id,
        "agent": public_agent(candidate),
        "session": created_session,
        "sent": sent_result,
    }


def _monitor_sessions_add(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    keys_to_add: list[str] = []
    if isinstance(payload.get("keys"), list):
        for k in payload["keys"]:
            if isinstance(k, str) and k.strip():
                keys_to_add.append(k.strip())
    elif payload.get("key"):
        keys_to_add.append(str(payload["key"]).strip())
    elif payload.get("agent_id") and payload.get("session_id"):
        keys_to_add.append(f"{payload['agent_id']}::{payload['session_id']}")
    else:
        raise AgentApiError("invalid_input", "key or keys list is required")

    if ctx.service is not None:
        ctx.service._server_event(
            "monitor.control",
            agent_id=None,
            session_id=None,
            data={"action": "add_sessions", "keys": keys_to_add},
        )

    return {"ok": True, "added_keys": keys_to_add}


def _monitor_sessions_remove(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    try:
        agent_id, session_id = parse_session_key(payload)
        key = f"{agent_id}::{session_id}"
    except Exception:
        if payload.get("key"):
            key = str(payload["key"]).strip()
        else:
            raise AgentApiError("invalid_input", "key is required") from None

    if ctx.service is not None:
        ctx.service._server_event(
            "monitor.control",
            agent_id=None,
            session_id=None,
            data={"action": "remove_session", "key": key},
        )

    return {"ok": True, "removed_key": key}


def _monitor_layout_set(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    cols = payload.get("columns")
    if not isinstance(cols, int):
        try:
            cols = int(cols)
        except (TypeError, ValueError) as exc:
            raise AgentApiError("invalid_input", "columns must be an integer (1-4)") from exc
    cols = max(1, min(cols, 4))

    if ctx.service is not None:
        ctx.service._server_event(
            "monitor.control",
            agent_id=None,
            session_id=None,
            data={"action": "set_layout", "columns": cols},
        )

    return {"ok": True, "columns": cols}


def _resolve_blackboard_ns(payload: dict[str, Any], ctx: AgentContext) -> str:
    raw_ns = payload.get("namespace")
    val = raw_ns.strip() if isinstance(raw_ns, str) else ""
    if val and val not in {"global", "default", "session:default"}:
        if not val.startswith("session:"):
            return val
        session_ref = val[len("session:"):].strip()
        if not session_ref:
            # 如果只传了 "session:" 或空后缀，回退到上下文默认会话命名空间
            pass
        elif "::" in session_ref:
            aid, sid = session_ref.split("::", 1)
            return ctx.store.get_session_blackboard_namespace(aid, sid) or f"session:{aid}::{sid}"
        else:
            aid = str(payload.get("caller_agent_id") or payload.get("agent_id") or "")
            found = ctx.store.get_session(aid, session_ref) if aid else ctx.store.find_session_by_id(session_ref)
            if not found:
                # 容错：如果未找到确切会话，直接以 session_ref 作为命名空间 key，避免抛出 500 崩溃
                return f"session:{session_ref}"
            aid, sid = str(found["agent_id"]), str(found["id"])
            return ctx.store.get_session_blackboard_namespace(aid, sid) or f"session:{aid}::{sid}"

    raw_sk = payload.get("session_key")
    if isinstance(raw_sk, str) and raw_sk.strip():
        sk = raw_sk.strip()
        if sk.startswith("group::"):
            return f"group:{sk[len('group::'):]}"
        if sk.startswith("swarm::"):
            return f"swarm:{sk[len('swarm::'):]}"
        if "::" in sk:
            aid, sid = sk.split("::", 1)
            return ctx.store.get_session_blackboard_namespace(aid, sid) or f"session:{sk}"
        return f"session:{sk}"

    group_id = payload.get("group_id")
    if isinstance(group_id, str) and group_id.strip():
        return f"group:{group_id.strip()}"

    parent_key = payload.get("parent_key")
    if parent_key and isinstance(parent_key, str) and "::" in parent_key:
        curr_key = parent_key
        visited = set()
        while curr_key and curr_key not in visited:
            visited.add(curr_key)
            if "::" in curr_key:
                a_id, s_id = curr_key.split("::", 1)
                sess = ctx.store.get_session(a_id, s_id)
                if sess and sess.get("parent_session_id"):
                    p_aid = sess.get("parent_agent_id") or a_id
                    curr_key = f"{p_aid}::{sess.get('parent_session_id')}"
                else:
                    break
            else:
                break
        return f"swarm:{curr_key}"

    aid = payload.get("caller_agent_id") or payload.get("agent_id")
    sid = payload.get("caller_session_id") or payload.get("session_id")
    session_key = payload.get("session_key")
    if isinstance(session_key, str) and "::" in session_key:
        aid, sid = session_key.split("::", 1)
        return ctx.store.get_session_blackboard_namespace(aid, sid) or f"session:{session_key}"
    if aid and sid:
        return ctx.store.get_session_blackboard_namespace(str(aid), str(sid)) or f"session:{aid}::{sid}"
    if aid:
        active = [row for row in ctx.store.list_sessions(str(aid)) if row.get("status") in {"running", "waiting_approval"}]
        if len(active) == 1:
            sid = str(active[0]["id"])
            return ctx.store.get_session_blackboard_namespace(str(aid), sid) or f"session:{aid}::{sid}"
        raise AgentApiError("invalid_input", "No unique active session; pass session_key explicitly")
    raise AgentApiError("invalid_input", "Blackboard namespace could not be resolved; pass session_key explicitly")


def _blackboard_get(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    ns = _resolve_blackboard_ns(payload, ctx)
    key = payload.get("key")
    from sqlalchemy import select
    from .models import WorkspacePreferenceRow

    with ctx.store.session() as db:
        if key and isinstance(key, str) and key.strip():
            db_key = f"blackboard:{ns}:{key.strip()}"
            row = db.get(WorkspacePreferenceRow, db_key)
            return {
                "namespace": ns,
                "key": key.strip(),
                "value": row.value if row else None,
                "exists": row is not None,
            }

        # List all keys in namespace
        prefix = f"blackboard:{ns}:"
        rows = db.scalars(select(WorkspacePreferenceRow).where(WorkspacePreferenceRow.key.startswith(prefix))).all()
        items = {}
        for r in rows:
            short_key = r.key[len(prefix):]
            items[short_key] = r.value
        return {"namespace": ns, "items": items, "count": len(items)}


def _blackboard_set(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    key = payload.get("key")
    if not isinstance(key, str) or not key.strip():
        raise AgentApiError("invalid_input", "key is required")
    key = key.strip()
    ns = _resolve_blackboard_ns(payload, ctx)
    value = payload.get("value")

    # Jev 后置判定与自适应组件补全：如果传入的是纯文本、无 component 标记的 dict，通过 Jev / 智能推断自动补全标准结构
    if isinstance(value, dict) and "component" not in value and "type" not in value:
        try:
            from .jev_client import evaluate_blackboard_component
            content_repr = json.dumps(value, ensure_ascii=False)
            eval_res = evaluate_blackboard_component(content_repr, store=ctx.store)
            recommended_comp = eval_res.get("component")
            if recommended_comp and recommended_comp != "StatusCard":
                # 智能注入 component 标记
                value = {"component": recommended_comp, **value}
        except Exception:
            # 优雅降级：纯规则自适应推断
            if any(k in value for k in ("steps", "pipeline", "stages", "milestones")):
                value = {"component": "StepTimeline", **value}
            elif any(k in value for k in ("items", "checklist", "todos", "tasks")):
                items = value.get("items") or value.get("checklist") or value.get("todos") or value.get("tasks")
                if isinstance(items, list) and any(isinstance(it, dict) and any(f in it for f in ("status", "done", "completed")) for it in items):
                    value = {"component": "StepTimeline", **value}
                else:
                    value = {"component": "Checklist", **value}
            elif any(k in value for k in ("metrics", "counters", "stats", "kpi")):
                value = {"component": "MetricGrid", **value}
            elif any(k in value for k in ("rows", "records", "data")) and isinstance(value.get("rows") or value.get("records") or value.get("data"), list):
                value = {"component": "DataTable", **value}
            elif any(k in value for k in ("endpoints", "apis", "routes")) and isinstance(value.get("endpoints") or value.get("apis") or value.get("routes"), list):
                value = {"component": "ApiEndpointsCard", **value}
    elif isinstance(value, str) and len(value.strip()) > 0:
        # 如果 Agent 直接传了长文本字符串，尝试用 Jev 判定自动结构化渲染为卡片
        try:
            from .jev_client import evaluate_blackboard_component
            eval_res = evaluate_blackboard_component(value.strip(), store=ctx.store)
            comp = eval_res.get("component") or "StatusCard"
            value = {"component": comp, "title": key.replace("_", " ").title(), "content": value.strip()}
        except Exception:
            pass

    from sqlalchemy.dialects.sqlite import insert
    from .models import WorkspacePreferenceRow

    db_key = f"blackboard:{ns}:{key}"
    with ctx.store.session() as db:
        statement = insert(WorkspacePreferenceRow).values(key=db_key, value=value)
        statement = statement.on_conflict_do_update(index_elements=["key"], set_={"value": value})
        db.execute(statement)

    if ctx.service is not None:
        ctx.service._server_event(
            "blackboard.change",
            agent_id=None,
            session_id=None,
            data={"action": "set", "namespace": ns, "key": key, "value": value},
        )
        # 若写入群聊黑板，向群聊历史中追加轻量系统事件，使其他群成员 Agent 在后续轮次即时获知
        if ns.startswith("group:"):
            gid = ns[len("group:"):]
            try:
                from .models import WorkspacePreferenceRow
                from sqlalchemy.dialects.sqlite import insert
                from datetime import datetime, timezone
                sender = str(payload.get("caller_agent_id") or payload.get("agent_id") or "Agent")
                notice = {
                    "id": f"gmsg-bb-{uuid.uuid4().hex[:12]}",
                    "group_id": gid,
                    "sender_type": "system",
                    "sender_id": "astrorder:blackboard",
                    "sender_name": "作战黑板",
                    "text": f"📋 【黑板更新】{sender} 更新了黑板条目「{key}」",
                    "mentions": [],
                    "hop_count": 0,
                    "created_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
                }
                with ctx.store.session() as db:
                    row = db.get(WorkspacePreferenceRow, f"group_messages:{gid}")
                    history = list(row.value) if (row and isinstance(row.value, list)) else []
                    history.append(notice)
                    stmt = insert(WorkspacePreferenceRow).values(key=f"group_messages:{gid}", value=history)
                    db.execute(stmt.on_conflict_do_update(index_elements=["key"], set_={"value": history}))
                ctx.service._server_event("bot_group.message", agent_id=None, session_id=None, data=notice)
            except Exception:
                pass

    return {"ok": True, "namespace": ns, "key": key, "value": value}


def _blackboard_delete(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    key = payload.get("key")
    ns = _resolve_blackboard_ns(payload, ctx)
    from .models import WorkspacePreferenceRow
    from sqlalchemy import delete

    clean_ns = payload.get("clean_namespace") is True or key == "*"
    with ctx.store.session() as db:
        if clean_ns:
            prefix = f"blackboard:{ns}:"
            db.execute(delete(WorkspacePreferenceRow).where(WorkspacePreferenceRow.key.startswith(prefix)))
            key = "*"
        else:
            if not isinstance(key, str) or not key.strip():
                raise AgentApiError("invalid_input", "key is required")
            key = key.strip()
            db_key = f"blackboard:{ns}:{key}"
            row = db.get(WorkspacePreferenceRow, db_key)
            if row:
                db.delete(row)

    if ctx.service is not None:
        ctx.service._server_event(
            "blackboard.change",
            agent_id=None,
            session_id=None,
            data={"action": "delete", "namespace": ns, "key": key},
        )

    return {"ok": True, "namespace": ns, "key": key}



from .blackboard_catalog import BLACKBOARD_COMPONENT_CATALOG

BLACKBOARD_SCHEMAS: dict[str, dict[str, Any]] = {
    "DataTable": {
        "component_id": "DataTable",
        "title": "结构化数据表格",
        "schema": {
            "title": "string (可选表格总标题)",
            "columns": [
                {
                    "key": "string (字段键名，必需)",
                    "label": "string (表头展示名称，可选，缺省同 key)",
                    "type": "'text' | 'badge' | 'mono' (列展示样式，可选)"
                }
            ],
            "rows": [
                "object (键值对应各列 key 的数据行对象，必需)"
            ],
            "caption": "string (表格底部备注说明，可选)"
        },
        "example": {
            "title": "全星域 Agent 会话健康度与审计状态表",
            "columns": [
                {"key": "agent", "label": "Agent 标识", "type": "mono"},
                {"key": "machine", "label": "所在服务器"},
                {"key": "status", "label": "健康度", "type": "badge"},
                {"key": "sessions", "label": "活跃会话数"}
            ],
            "rows": [
                {"agent": "local-codex", "machine": "本机", "status": "ok", "sessions": 43},
                {"agent": "ssh-codex-debian", "machine": "Debian", "status": "ok", "sessions": 19},
                {"agent": "ssh-hermes-wsl", "machine": "WSL", "status": "ok", "sessions": 4}
            ],
            "caption": "审计耗时 1.2s，共扫描 3 台主机节点"
        }
    },
    "StepTimeline": {
        "component_id": "StepTimeline",
        "title": "任务阶段与流水线时间线",
        "schema": {
            "title": "string (可选总标题)",
            "steps": [
                {
                    "title": "string (阶段标题，必需)",
                    "status": "'completed' | 'running' | 'pending' | 'failed' (状态，必需)",
                    "description": "string (阶段说明或产出物描述，可选)",
                    "time": "string (耗时或完成时间，可选)"
                }
            ]
        },
        "example": {
            "title": "生产构建与发布流水线",
            "steps": [
                {"title": "代码依赖审计", "status": "completed", "time": "2.4s"},
                {"title": "单元与集成测试", "status": "completed", "time": "14.2s"},
                {"title": "Docker 镜像构建与安全扫描", "status": "running", "description": "正在执行 CVE 漏洞扫描..."},
                {"title": "灰度放量发布", "status": "pending"}
            ]
        }
    },
    "MetricGrid": {
        "component_id": "MetricGrid",
        "title": "核心监控与 KPI 指标矩阵",
        "schema": {
            "title": "string (可选总标题)",
            "metrics": [
                {
                    "label": "string (指标名称，必需)",
                    "value": "string | number (指标数值，必需)",
                    "unit": "string (单位，如 'ms', 'MB', '%', 可选)",
                    "change": "string (同比/环比浮动，如 '+12%', '-5%', 可选)",
                    "status": "'good' | 'warn' | 'bad' (健康状态，可选)"
                }
            ]
        },
        "example": {
            "title": "推理网关实时性能看板",
            "metrics": [
                {"label": "QPS 吞吐", "value": 1420, "unit": "req/s", "change": "+8.4%", "status": "good"},
                {"label": "P99 时延", "value": 42, "unit": "ms", "change": "-4ms", "status": "good"},
                {"label": "显存占用", "value": 82.5, "unit": "%", "status": "warn"},
                {"label": "错误率", "value": 0.01, "unit": "%", "status": "good"}
            ]
        }
    },
    "ApiEndpointsCard": {
        "component_id": "ApiEndpointsCard",
        "title": "REST / RPC 接口契约清单",
        "schema": {
            "title": "string (清单标题，可选)",
            "baseUrl": "string (基础地址，如 'https://api.domain.com', 可选)",
            "endpoints": [
                {
                    "method": "'GET' | 'POST' | 'PUT' | 'DELETE' | 'PATCH' (请求方法，必需)",
                    "path": "string (接口路径，必需)",
                    "desc": "string (接口描述或功能说明，可选)",
                    "status": "number (返回状态码如 200, 201, 可选)"
                }
            ]
        },
        "example": {
            "title": "用户认证与授权服务 API 契约",
            "baseUrl": "https://auth.internal.net",
            "endpoints": [
                {"method": "POST", "path": "/api/v1/auth/login", "desc": "账密登录获取 JWT", "status": 200},
                {"method": "GET", "path": "/api/v1/users/me", "desc": "查询当前登录用户会话与权限", "status": 200},
                {"method": "POST", "path": "/api/v1/tokens/refresh", "desc": "静默刷新访问令牌", "status": 200}
            ]
        }
    },
    "ResourceUsageBar": {
        "component_id": "ResourceUsageBar",
        "title": "系统资源负载与配额",
        "schema": {
            "title": "string (标题，可选)",
            "resources": [
                {
                    "name": "string (资源名称，如 'CPU 核心', '物理内存', 必需)",
                    "used": "number (已使用量，可选)",
                    "total": "number (总配额量，可选)",
                    "unit": "string (单位，如 'GB', 'Core', 可选)",
                    "percent": "number (百分比 0-100，可选)"
                }
            ]
        },
        "example": {
            "title": "推理宿主机当前负载配额",
            "resources": [
                {"name": "CPU 负载", "percent": 45, "display": "45% (8/16 Cores)"},
                {"name": "物理内存", "used": 18, "total": 32, "unit": "GB", "percent": 56},
                {"name": "NVMe 根盘", "used": 120, "total": 512, "unit": "GB", "percent": 23}
            ]
        }
    },
    "TestReport": {
        "component_id": "TestReport",
        "title": "自动化测试与压测报告",
        "schema": {
            "title": "string (报告标题，可选)",
            "passed": "number (通过用例数，必需)",
            "failed": "number (失败用例数，必需)",
            "skipped": "number (跳过用例数，可选)",
            "duration": "string (总耗时，如 '14.2s', 可选)",
            "suite": "string (测试套件名，可选)",
            "cases": [{"name": "string", "status": "'passed' | 'failed'", "duration": "string"}]
        },
        "example": {
            "title": "核心端到端集成测试报告",
            "suite": "pytest --e2e backend/tests",
            "passed": 42,
            "failed": 0,
            "skipped": 1,
            "duration": "8.45s",
            "cases": [
                {"name": "test_mcp_gateway_dispatch", "status": "passed", "duration": "0.12s"},
                {"name": "test_blackboard_namespace_scoping", "status": "passed", "duration": "0.08s"},
                {"name": "test_agent_websocket_heartbeat", "status": "passed", "duration": "0.15s"}
            ]
        }
    },
    "CveSecurityReport": {
        "component_id": "CveSecurityReport",
        "title": "安全审计与漏洞合规报告",
        "schema": {
            "title": "string (报告标题，可选)",
            "critical": "number (严重漏洞数，必需)",
            "high": "number (高危漏洞数，必需)",
            "medium": "number (中危漏洞数，必需)",
            "low": "number (低危漏洞数，必需)",
            "vulnerabilities": [{"cve": "string", "package": "string", "severity": "'CRITICAL' | 'HIGH' | 'MEDIUM'"}]
        },
        "example": {
            "title": "容器基础镜像 CVE 安全审计结果",
            "critical": 0,
            "high": 1,
            "medium": 2,
            "low": 5,
            "vulnerabilities": [
                {"cve": "CVE-2026-3829", "package": "openssl-3.0.12", "severity": "HIGH"},
                {"cve": "CVE-2026-1920", "package": "curl-8.5.0", "severity": "MEDIUM"}
            ]
        }
    },
    "DiffViewer": {
        "component_id": "DiffViewer",
        "title": "代码补丁与配置差异对比器",
        "schema": {
            "file": "string (修改的文件路径，可选)",
            "diff": "string (标准统一 diff 文本，包含 +/-/@@ 标记，必需)"
        },
        "example": {
            "file": "backend/src/astrorder/store.py",
            "diff": "@@ -102,3 +102,3 @@\n-  namespace = 'global'\n+  namespace = f'swarm:{root_key}'\n   db.flush()"
        }
    },
    "Checklist": {
        "component_id": "Checklist",
        "title": "执行清单与交付验收表",
        "schema": {
            "title": "string (清单标题，可选)",
            "items": [
                {
                    "label": "string (检查项描述，必需)",
                    "done": "boolean (是否已完成，必需)",
                    "assignee": "string (负责人/执行 Agent，可选)"
                }
            ]
        },
        "example": {
            "title": "生产上线前就绪检查清单",
            "items": [
                {"label": "数据库无锁热迁移", "done": True, "assignee": "Codex"},
                {"label": "全链路接口拨测通过", "done": True, "assignee": "Hermes"},
                {"label": "CDN 静态缓存预热与回源验证", "done": False, "assignee": "Grok"}
            ]
        }
    },
    "TerminalLog": {
        "component_id": "TerminalLog",
        "title": "控制台终端日志流",
        "schema": {
            "title": "string (终端标题，可选)",
            "status": "string (状态标记，如 'ok', 'error', 可选)",
            "lines": "string[] (日志行数组，包含 ERROR/WARN 将自动染色，必需)"
        },
        "example": {
            "title": "集群编译构建输出",
            "status": "ok",
            "lines": [
                "[INFO] Starting build for astrorder-worker:v2.4",
                "[INFO] Compiling native Rust / PTY bridge",
                "[WARN] Deprecated dependency detected in build graph",
                "[INFO] Build finished successfully in 4.2s"
            ]
        }
    },
    "ArchitectureFlow": {
        "component_id": "ArchitectureFlow",
        "title": "架构与调用链路流程图",
        "schema": {
            "title": "string (架构标题，可选)",
            "nodes": [
                {
                    "name": "string (服务/节点名称，必需)",
                    "role": "string (职责/类型如 'Gateway', 'Core API', 'DB', 必需)",
                    "status": "'ok' | 'warn' (健康状态，可选)",
                    "desc": "string (补充描述，可选)"
                }
            ]
        },
        "example": {
            "title": "多智能体任务协同链路",
            "nodes": [
                {"name": "Codex (主星)", "role": "Orchestrator", "status": "ok", "desc": "指挥与门禁裁决"},
                {"name": "Hermes (伴星)", "role": "WSL Worker", "status": "ok", "desc": "性能数据采集"},
                {"name": "Grok (伴星)", "role": "Debian Worker", "status": "ok", "desc": "集群状态汇总"}
            ]
        }
    },
    "StatusCard": {
        "component_id": "StatusCard",
        "title": "服务与网关状态概览卡片",
        "schema": {
            "title": "string (卡片标题，必需)",
            "status": "'success' | 'running' | 'ready' | 'warning' | 'error' | 'info' (状态枚举，必需)",
            "summary": "string (核心摘要描述，可选)",
            "badge": "string (右上角徽标文字，可选)",
            "timestamp": "string (时间戳，可选)",
            "details": "object (键值补充参数对象，可选)"
        },
        "example": {
            "title": "多节点负载探针任务状态",
            "status": "success",
            "summary": "已顺利完成 WSL2 与 Debian 远端主机全部 14 项基准指标采集。",
            "badge": "COMPLETED",
            "details": {"wsl_latency": "12ms", "debian_latency": "34ms", "nodes": 2}
        }
    }
}


def _blackboard_components_list(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    return {"ok": True, "count": len(BLACKBOARD_COMPONENT_CATALOG), "items": BLACKBOARD_COMPONENT_CATALOG}


def _blackboard_auto_render(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    key = payload.get("key")
    content = payload.get("content")
    if not isinstance(key, str) or not key.strip():
        raise AgentApiError("invalid_input", "key is required")
    if not isinstance(content, str) or not content.strip():
        raise AgentApiError("invalid_input", "content is required")
    title = payload.get("title") or key.strip().replace("_", " ").title()
    
    from .jev_client import evaluate_blackboard_component
    try:
        eval_res = evaluate_blackboard_component(content.strip(), store=ctx.store)
        comp = eval_res.get("component") or "StatusCard"
    except Exception as e:
        # Jev key 未配置或调用失败时的优雅回退
        comp = "StatusCard"
        eval_res = {"fallback": True, "error": str(e)}

    # 根据推荐的组件类型组装 json-render 结构
    rendered_value: dict[str, Any] = {"component": comp, "title": title}
    lines = [line.strip() for line in content.strip().splitlines() if line.strip()]

    if comp == "StepTimeline":
        steps = []
        clean_lines = [l for l in lines if not l.endswith("：") and not l.endswith(":") and "路线图" not in l]
        for idx, line in enumerate(clean_lines, 1):
            is_done = any(w in line.lower() for w in ["completed", "complete", "ok", "pass", "done", "成功", "完成", "已完成", "已落地", "已建立", "已声明"])
            is_failed = any(w in line.lower() for w in ["failed", "fail", "error", "err", "失败", "异常"])
            if is_done:
                status = "completed"
            elif is_failed:
                status = "failed"
            elif idx == len(clean_lines):
                status = "running"
            else:
                status = "pending"
            steps.append({"title": line, "status": status})
        rendered_value["steps"] = steps or [{"title": content, "status": "completed"}]
    elif comp == "MetricGrid":
        metrics = []
        for line in lines[:8]:
            if ":" in line or "=" in line or "：" in line:
                parts = line.replace("：", ":").replace("=", ":").split(":", 1)
                metrics.append({"label": parts[0].strip(), "value": parts[1].strip()})
            else:
                metrics.append({"label": f"Metric {len(metrics)+1}", "value": line})
        rendered_value["metrics"] = metrics
    elif comp == "Checklist":
        items = []
        for line in lines:
            checked = any(w in line.lower() for w in ["[x]", "done", "pass", "ok", "完成", "通过"])
            clean_label = line.replace("[x]", "").replace("[ ]", "").strip()
            items.append({"label": clean_label, "checked": checked})
        rendered_value["items"] = items
    elif comp == "CveSecurityReport":
        rendered_value["summary"] = content[:300]
        rendered_value["severity"] = "high" if any(w in content.lower() for w in ["high", "critical", "严重"]) else "medium"
        rendered_value["findings"] = [{"title": l, "severity": "medium"} for l in lines[:5]]
    elif comp == "TestReport":
        rendered_value["total"] = len(lines)
        passed = sum(1 for l in lines if any(w in l.lower() for w in ["pass", "ok", "通过", "成功"]))
        rendered_value["passed"] = passed
        rendered_value["failed"] = len(lines) - passed
        rendered_value["tests"] = [{"name": l, "status": "passed" if any(w in l.lower() for w in ["pass", "ok"]) else "failed"} for l in lines[:10]]
    else:
        rendered_value["status"] = "info"
        rendered_value["description"] = content

    # 写入黑板
    set_res = _blackboard_set({**payload, "key": key, "value": rendered_value}, ctx)
    return {"ok": True, "namespace": set_res["namespace"], "key": key, "component": comp, "jev_decision": eval_res, "rendered": rendered_value}


def _blackboard_component_schema(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    cid = payload.get("component_id")
    if not isinstance(cid, str) or not cid.strip():
        raise AgentApiError("invalid_input", "component_id is required")
    cid = cid.strip()

    matched = None
    for k, v in BLACKBOARD_SCHEMAS.items():
        if k.lower() == cid.lower():
            matched = v
            break

    if not matched:
        raise AgentApiError("not_found", f"Component '{cid}' not found. Call blackboard_components_list to see all valid IDs.")

    return {"ok": True, **matched}

def _milestone_declare(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    from .swarm_service import milestone_declare
    try:
        return milestone_declare(payload, store=ctx.store, service=ctx.service)
    except ValueError as e:
        raise AgentApiError("invalid_input", str(e)) from e


def _milestone_resolve(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    from .swarm_service import milestone_resolve
    try:
        return milestone_resolve(payload, store=ctx.store, service=ctx.service)
    except ValueError as e:
        raise AgentApiError("invalid_input", str(e)) from e


def _milestone_list(_payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    from sqlalchemy import select
    from .models import WorkspacePreferenceRow

    prefix = "milestone:"
    with ctx.store.session() as db:
        rows = db.scalars(select(WorkspacePreferenceRow).where(WorkspacePreferenceRow.key.startswith(prefix))).all()
        items = [r.value for r in rows if isinstance(r.value, dict)]
    return {"items": items, "count": len(items)}


def _telemetry_report(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    key = payload.get("key")
    agent_id = None
    session_id = None
    if key and isinstance(key, str) and "::" in key:
        agent_id, session_id = key.split("::", 1)
    elif payload.get("session_id") and payload.get("agent_id"):
        agent_id = str(payload.get("agent_id"))
        session_id = str(payload.get("session_id"))
    else:
        # Fallback to key or anonymous worker tag
        key = str(payload.get("key") or "anonymous_worker")

    canonical_key = f"{agent_id}::{session_id}" if agent_id and session_id else str(key)

    progress = payload.get("progress", 0)
    if not isinstance(progress, (int, float)):
        try:
            progress = int(progress)
        except Exception:
            progress = 0
    progress = max(0, min(int(progress), 100))

    phase = str(payload.get("phase") or "working").strip()
    status = str(payload.get("status") or "ok").strip()
    summary = str(payload.get("summary") or "").strip()
    artifacts = payload.get("artifacts") or []
    if not isinstance(artifacts, list):
        artifacts = [str(artifacts)]

    report = {
        "key": canonical_key,
        "agent_id": agent_id,
        "session_id": session_id,
        "progress": progress,
        "phase": phase,
        "status": status,
        "summary": summary,
        "artifacts": artifacts,
        "updated_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    }

    from sqlalchemy.dialects.sqlite import insert
    from .models import WorkspacePreferenceRow

    db_key = f"telemetry:{canonical_key}"
    with ctx.store.session() as db:
        statement = insert(WorkspacePreferenceRow).values(key=db_key, value=report)
        statement = statement.on_conflict_do_update(index_elements=["key"], set_={"value": report})
        db.execute(statement)

    if ctx.service is not None:
        ctx.service._server_event(
            "swarm.telemetry.event",
            agent_id=agent_id,
            session_id=session_id,
            data=report,
        )

    return {"ok": True, "report": report}


def _telemetry_get(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    key = payload.get("key")
    from sqlalchemy import select
    from .models import WorkspacePreferenceRow

    with ctx.store.session() as db:
        if key and isinstance(key, str) and key.strip():
            db_key = f"telemetry:{key.strip()}"
            row = db.get(WorkspacePreferenceRow, db_key)
            return {
                "key": key.strip(),
                "report": row.value if row else None,
                "exists": row is not None,
            }

        prefix = "telemetry:"
        rows = db.scalars(select(WorkspacePreferenceRow).where(WorkspacePreferenceRow.key.startswith(prefix))).all()
        items = {}
        for r in rows:
            k = r.key[len(prefix):]
            items[k] = r.value
        return {"items": items, "count": len(items)}


def _sos_escalate(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    reason = payload.get("reason")
    if not isinstance(reason, str) or not reason.strip():
        raise AgentApiError("invalid_input", "reason is required")
    reason = reason.strip()

    key = payload.get("key")
    context_detail = payload.get("context") or ""
    target_key = payload.get("target_session_key")

    worker_session = None
    if key and isinstance(key, str) and "::" in key:
        aid, sid = key.split("::", 1)
        worker_session = ctx.store.get_session(aid, sid)

    if not target_key and worker_session:
        p_aid = worker_session.get("parent_agent_id") or worker_session.get("handoff_from_agent_id")
        p_sid = worker_session.get("parent_session_id") or worker_session.get("handoff_from_session_id")
        if p_aid and p_sid:
            target_key = f"{p_aid}::{p_sid}"

    sos_id = f"sos_{uuid.uuid4().hex[:12]}"
    record = {
        "id": sos_id,
        "worker_key": key or "anonymous_worker",
        "target_key": target_key,
        "reason": reason,
        "context": context_detail,
        "status": "active",
        "created_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        "resolved_at": None,
        "resolution": None,
    }

    from sqlalchemy.dialects.sqlite import insert
    from .models import WorkspacePreferenceRow

    db_key = f"sos:{sos_id}"
    with ctx.store.session() as db:
        statement = insert(WorkspacePreferenceRow).values(key=db_key, value=record)
        statement = statement.on_conflict_do_update(index_elements=["key"], set_={"value": record})
        db.execute(statement)

    # Notify queen / parent session if target_key exists
    dispatched = False
    if target_key:
        sos_msg = (
            f"🚨 【工蜂紧急求援 SOS】 节点: {record['worker_key']}\n"
            f"遇险原因: {reason}\n"
            f"上下文详情: {context_detail or '无更多错误细节'}\n"
            f"SOS 编号: {sos_id}。请评估是否派发辅助任务或下发纠偏指令。"
        )
        try:
            _sessions_send({"key": target_key, "text": sos_msg}, ctx)
            dispatched = True
        except Exception:
            pass

    if ctx.service is not None:
        ctx.service._server_event(
            "swarm.sos.event",
            agent_id=None,
            session_id=None,
            data={"action": "escalate", "sos": record, "notified_queen": dispatched},
        )

    return {"ok": True, "sos": record, "notified_queen": dispatched}


def _sos_list(_payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    from sqlalchemy import select
    from .models import WorkspacePreferenceRow

    prefix = "sos:"
    with ctx.store.session() as db:
        rows = db.scalars(select(WorkspacePreferenceRow).where(WorkspacePreferenceRow.key.startswith(prefix))).all()
        items = [r.value for r in rows if isinstance(r.value, dict) and r.value.get("status") == "active"]
    return {"items": items, "count": len(items)}


def _sos_resolve(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    sos_id = payload.get("sos_id")
    if not isinstance(sos_id, str) or not sos_id.strip():
        raise AgentApiError("invalid_input", "sos_id is required")
    sos_id = sos_id.strip()
    resolution = payload.get("resolution") or "Resolved by commander or orchestrator"

    from sqlalchemy.dialects.sqlite import insert
    from .models import WorkspacePreferenceRow

    db_key = f"sos:{sos_id}"
    with ctx.store.session() as db:
        row = db.get(WorkspacePreferenceRow, db_key)
        if not row or not isinstance(row.value, dict):
            raise AgentApiError("not_found", f"SOS alert '{sos_id}' was not found")
        record = dict(row.value)
        record["status"] = "resolved"
        record["resolved_at"] = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
        record["resolution"] = resolution
        statement = insert(WorkspacePreferenceRow).values(key=db_key, value=record)
        statement = statement.on_conflict_do_update(index_elements=["key"], set_={"value": record})
        db.execute(statement)

    if ctx.service is not None:
        ctx.service._server_event(
            "swarm.sos.event",
            agent_id=None,
            session_id=None,
            data={"action": "resolve", "sos": record},
        )

    return {"ok": True, "sos": record}


def _lock_acquire(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    resource = payload.get("resource")
    if not isinstance(resource, str) or not resource.strip():
        raise AgentApiError("invalid_input", "resource identifier is required")
    resource = resource.strip()

    owner = str(payload.get("owner_key") or "anonymous_worker").strip()
    ttl = payload.get("ttl_seconds", 300)
    if not isinstance(ttl, (int, float)):
        try:
            ttl = int(ttl)
        except Exception:
            ttl = 300
    ttl = max(10, min(int(ttl), 3600))

    from datetime import timedelta
    from sqlalchemy.dialects.sqlite import insert
    from .models import WorkspacePreferenceRow

    now = datetime.now(timezone.utc)
    db_key = f"lock:{resource}"
    with ctx.store.session() as db:
        row = db.get(WorkspacePreferenceRow, db_key)
        if row and isinstance(row.value, dict):
            expires_at_str = row.value.get("expires_at")
            if expires_at_str:
                try:
                    exp = datetime.fromisoformat(expires_at_str.replace("Z", "+00:00"))
                    if exp > now and row.value.get("owner_key") != owner:
                        return {
                            "acquired": False,
                            "resource": resource,
                            "current_owner": row.value.get("owner_key"),
                            "expires_at": expires_at_str,
                            "message": f"Resource '{resource}' is locked by {row.value.get('owner_key')} until {expires_at_str}",
                        }
                except Exception:
                    pass

        expires_at = now + timedelta(seconds=ttl)
        lock_record = {
            "resource": resource,
            "owner_key": owner,
            "acquired_at": now.isoformat().replace("+00:00", "Z"),
            "expires_at": expires_at.isoformat().replace("+00:00", "Z"),
            "ttl_seconds": ttl,
        }
        statement = insert(WorkspacePreferenceRow).values(key=db_key, value=lock_record)
        statement = statement.on_conflict_do_update(index_elements=["key"], set_={"value": lock_record})
        db.execute(statement)

    if ctx.service is not None:
        ctx.service._server_event("swarm.lock.event", agent_id=None, session_id=None, data={"action": "acquired", "lock": lock_record})

    return {"acquired": True, "lock": lock_record}


def _lock_release(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    resource = payload.get("resource")
    if not isinstance(resource, str) or not resource.strip():
        raise AgentApiError("invalid_input", "resource identifier is required")
    resource = resource.strip()
    owner = str(payload.get("owner_key") or "").strip()

    from .models import WorkspacePreferenceRow

    db_key = f"lock:{resource}"
    released = False
    with ctx.store.session() as db:
        row = db.get(WorkspacePreferenceRow, db_key)
        if row and isinstance(row.value, dict):
            if not owner or row.value.get("owner_key") == owner:
                db.delete(row)
                released = True
            else:
                return {"released": False, "message": f"Cannot release lock owned by {row.value.get('owner_key')}"}
        else:
            released = True

    if ctx.service is not None and released:
        ctx.service._server_event("swarm.lock.event", agent_id=None, session_id=None, data={"action": "released", "resource": resource})

    return {"released": released, "resource": resource}


def _lock_list(_payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    from sqlalchemy import select
    from .models import WorkspacePreferenceRow

    now = datetime.now(timezone.utc)
    prefix = "lock:"
    active_locks = []
    with ctx.store.session() as db:
        rows = db.scalars(select(WorkspacePreferenceRow).where(WorkspacePreferenceRow.key.startswith(prefix))).all()
        for r in rows:
            if isinstance(r.value, dict):
                exp_str = r.value.get("expires_at")
                if exp_str:
                    try:
                        exp = datetime.fromisoformat(exp_str.replace("Z", "+00:00"))
                        if exp > now:
                            active_locks.append(r.value)
                            continue
                    except Exception:
                        pass
                db.delete(r)  # clean expired

    return {"items": active_locks, "count": len(active_locks)}


def _resolve_browser_session_key(payload: dict[str, Any], ctx: AgentContext) -> str:
    raw_key = payload.get("session_key")
    if isinstance(raw_key, str) and "::" in raw_key:
        agent_id, session_id = raw_key.split("::", 1)
        if ctx.store.get_session(agent_id, session_id):
            return f"{agent_id}::{session_id}"
        raise AgentApiError("not_found", f"Session '{raw_key}' was not found")
    agent_id = str(payload.get("caller_agent_id") or payload.get("agent_id") or "")
    session_id = str(payload.get("caller_session_id") or payload.get("session_id") or "")
    if agent_id and session_id and ctx.store.get_session(agent_id, session_id):
        return f"{agent_id}::{session_id}"
    if agent_id:
        active = [
            row for row in ctx.store.list_sessions(agent_id)
            if row.get("status") in {"running", "waiting_approval"}
        ]
        if len(active) == 1:
            return f"{agent_id}::{active[0]['id']}"
    raise AgentApiError("invalid_input", "browser_run requires session_key because the calling session is ambiguous")


def _browser_run(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    url = payload.get("url")
    goal = payload.get("goal")
    inputs = payload.get("inputs")
    if not isinstance(url, str) or not isinstance(goal, str):
        raise AgentApiError("invalid_input", "url and goal are required")
    if inputs is not None and not isinstance(inputs, dict):
        raise AgentApiError("invalid_input", "inputs must be an object mapping field labels to values")
    if "include_screenshot" in payload and not isinstance(payload["include_screenshot"], bool):
        raise AgentApiError("invalid_input", "include_screenshot must be a boolean")
    session_key = _resolve_browser_session_key(payload, ctx)
    try:
        max_steps = int(payload.get("max_steps", 30))
        from .jev_browser import run_browser_task

        result = run_browser_task(
            url=url,
            goal=goal,
            inputs=inputs,
            store=ctx.store,
            max_steps=max_steps,
            reset=payload.get("reset") is True,
            include_screenshot=payload.get("include_screenshot") is True,
            session_key=session_key,
        )
        if ctx.service and result.get("screenshot_revision"):
            ctx.service._server_event(
                "browser.mirror.updated",
                agent_id=None,
                session_id=None,
                data={
                    "revision": result["screenshot_revision"],
                    "url": result.get("url", ""),
                    "title": result.get("title", ""),
                    "session_key": session_key,
                },
            )
        return result
    except (ValueError, RuntimeError) as exc:
        raise AgentApiError("browser_task_failed", str(exc)) from exc


def _browser_screenshot(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    try:
        from .jev_browser import capture_browser_screenshot

        return capture_browser_screenshot(_resolve_browser_session_key(payload, ctx), require_owner=True)
    except RuntimeError as exc:
        raise AgentApiError("browser_task_failed", str(exc)) from exc


def _browser_cdp(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    method = payload.get("method")
    params = payload.get("params")
    if not isinstance(method, str) or not method:
        raise AgentApiError("invalid_input", "method is required")
    if params is not None and not isinstance(params, dict):
        raise AgentApiError("invalid_input", "params must be an object")
    try:
        from .jev_browser import call_browser_cdp

        return call_browser_cdp(
            session_key=_resolve_browser_session_key(payload, ctx),
            method=method,
            params=params,
        )
    except (RuntimeError, ValueError) as exc:
        raise AgentApiError("browser_cdp_failed", str(exc)) from exc


HANDLERS: dict[str, Callable[[dict[str, Any], AgentContext], dict[str, Any]]] = {
    "catalog.list": _catalog,
    "sessions.list": _sessions_list,
    "sessions.search": _sessions_search,
    "sessions.read": _sessions_read,
    "agents.list": _agents_list,
    "machines.list": _machines_list,
    "projects.list": _projects_list,
    "sessions.create": _sessions_create,
    "sessions.send": _sessions_send,
    "sessions.stop": _sessions_stop,
    "sessions.delete": _sessions_delete,
    "sessions.tree": _sessions_tree,
    "plugins.list": _plugins_list,
    "plugins.configure": _plugins_configure,
    "plugins.open": _plugins_open,
    "plugins.close": _plugins_close,
    "browser.run": _browser_run,
    "browser.screenshot": _browser_screenshot,
    "browser.cdp": _browser_cdp,
    "machines.dispatch": _machines_dispatch,
    "monitor.sessions.add": _monitor_sessions_add,
    "monitor.sessions.remove": _monitor_sessions_remove,
    "monitor.layout.set": _monitor_layout_set,
    "blackboard.get": _blackboard_get,
    "blackboard.set": _blackboard_set,
    "blackboard.delete": _blackboard_delete,
    "blackboard.components.list": _blackboard_components_list,
    "blackboard.component.schema": _blackboard_component_schema,
    "blackboard.auto_render": _blackboard_auto_render,
    "swarm.milestone.declare": _milestone_declare,
    "swarm.milestone.resolve": _milestone_resolve,
    "swarm.milestone.list": _milestone_list,
    "swarm.telemetry.report": _telemetry_report,
    "swarm.telemetry.get": _telemetry_get,
    "swarm.sos.escalate": _sos_escalate,
    "swarm.sos.list": _sos_list,
    "swarm.sos.resolve": _sos_resolve,
    "swarm.lock.acquire": _lock_acquire,
    "swarm.lock.release": _lock_release,
    "swarm.lock.list": _lock_list,
}


def invoke(capability: str, payload: dict[str, Any] | None, ctx: AgentContext) -> dict[str, Any]:
    handler = HANDLERS.get(capability)
    if handler is None:
        raise AgentApiError("unknown_capability", f"Unknown capability: {capability}")
    return handler(payload or {}, ctx)
from datetime import datetime, timezone
