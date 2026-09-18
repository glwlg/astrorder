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
        "input": {"agent_id": "optional agent id filter", "limit": "optional maximum returned count (default 30, max 100)"},
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
        "input": {"agent_id": "target agent id", "title": "optional session title", "workspace": "optional workspace path"},
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
        "summary": "Read shared mission state, specifications or facts from the Astrorder Blackboard.",
        "input": {
            "key": "specific blackboard key, or omit to list all keys",
            "namespace": "optional namespace scope (default 'global')"
        },
    },
    {
        "id": "blackboard.set",
        "summary": "Write or publish shared mission state, specs or artifacts to Astrorder Blackboard. To render rich Generative UI for humans, call blackboard_components_list to discover components, then blackboard_component_schema for exact field schemas and examples.",
        "input": {
            "key": "blackboard key",
            "value": "data payload (string, object, array, or number)",
            "namespace": "optional namespace scope (default 'global')"
        },
    },
    {
        "id": "blackboard.delete",
        "summary": "Delete a key from the Astrorder Blackboard.",
        "input": {
            "key": "blackboard key to delete, or '*' / clean_namespace: true to wipe space",
            "namespace": "optional namespace scope (default automatically resolved)"
        },
    },
    {
        "id": "blackboard.components.list",
        "summary": "List all supported Generative UI components for Blackboard with their IDs, categories and summaries.",
        "input": {},
    },
    {
        "id": "blackboard.component.schema",
        "summary": "Query exact JSON schema, required fields and copyable example payload for a specific Blackboard Generative UI component.",
        "input": {
            "component_id": "component name, e.g. 'StepTimeline', 'MetricGrid', 'ApiEndpointsCard', 'ResourceUsageBar', 'TestReport', 'CveSecurityReport', 'DiffViewer', 'Checklist', 'TerminalLog', 'ArchitectureFlow', 'StatusCard', 'MultiNodeClusterSummary', 'HostNodeTelemetryCard', 'MissionSpecCard', 'GomokuBoard'"
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
    limit = max(1, min(limit, 100))
    rows = ctx.store.list_sessions(agent_id if isinstance(agent_id, str) and agent_id else None)
    total = len(rows)
    sliced = rows[:limit]
    return {"items": [public_session(row) for row in sliced], "total": total, "has_more": total > limit}


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
    {"id": "blackboard", "title": "作战黑板 (Blackboard)", "description": "多 Agent 任务协同共享记忆、规格参数与产出物实时看板", "category": "collaboration", "extensions": [".json", ".yaml", ".yml", ".md"]},
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

    canonical = ctx.store.upsert_session(data)
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
    if isinstance(raw_ns, str) and raw_ns.strip() and raw_ns.strip() != "global":
        return raw_ns.strip()

    target_key = payload.get("session_key") or payload.get("parent_key")
    if not target_key:
        aid = payload.get("agent_id") or payload.get("caller_agent_id")
        sid = payload.get("session_id") or payload.get("caller_session_id")
        if aid and sid:
            target_key = f"{aid}::{sid}"

    if target_key and isinstance(target_key, str) and "::" in target_key:
        curr_key = target_key
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

    return str(raw_ns or "global").strip()


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



BLACKBOARD_COMPONENT_CATALOG: list[dict[str, Any]] = [
    {"id": "StepTimeline", "title": "任务阶段与流水线时间线", "category": "workflow", "summary": "呈现 CI/CD 流水线、任务阶段交接与步骤执行进度"},
    {"id": "MetricGrid", "title": "核心监控与 KPI 指标矩阵", "category": "metrics", "summary": "多列 Bento 呈现 QPS、耗时、内存、准确率等关键数值指标"},
    {"id": "ApiEndpointsCard", "title": "REST / RPC 接口契约清单", "category": "api", "summary": "呈现带 HTTP 方法染色、URL 路径、状态码与描述的 API 列表"},
    {"id": "ResourceUsageBar", "title": "系统资源负载与配额", "category": "system", "summary": "呈现 CPU、内存、磁盘的红黄绿阈值健康进度条"},
    {"id": "TestReport", "title": "自动化测试与压测报告", "category": "testing", "summary": "统计通过/失败/跳过用例数、耗时及大字号通过率百分比"},
    {"id": "CveSecurityReport", "title": "安全审计与漏洞合规报告", "category": "security", "summary": "按 Critical/High/Medium/Low 统计风险并罗列 CVE 漏洞清单"},
    {"id": "DiffViewer", "title": "代码补丁与配置差异对比器", "category": "code", "summary": "以等宽排版与增删染色呈现代码 diff / patch"},
    {"id": "Checklist", "title": "执行清单与交付验收表", "category": "task", "summary": "展示验收检查项，已完成项自动划线变灰并支持负责人"},
    {"id": "TerminalLog", "title": "控制台终端日志流", "category": "runtime", "summary": "深色控制台终端，带 ERROR、WARN 关键字自动染色输出"},
    {"id": "ArchitectureFlow", "title": "架构与调用链路流程图", "category": "architecture", "summary": "横向卡片箭头串联展示微服务、网关与 Agent 拓扑"},
    {"id": "GitCommitLog", "title": "Git 提交与发布变更日志", "category": "vcs", "summary": "展示代码版本提交历史、分支名、短 SHA 与时间戳"},
    {"id": "StatusCard", "title": "服务与网关状态概览卡片", "category": "status", "summary": "呈现 success、running、ready、warning、error 五态服务卡片"},
    {"id": "MultiNodeClusterSummary", "title": "多节点集群性能横向对比", "category": "cluster", "summary": "多主机 CPU/内存/磁盘/TOP 进程横向对齐对比看板"},
    {"id": "HostNodeTelemetryCard", "title": "服务器硬件与负载体检卡", "category": "host", "summary": "呈现单个主机 CPU 负载、内存可用健康条与系统版本"},
    {"id": "MissionSpecCard", "title": "作战任务契约与指挥规格卡", "category": "swarm", "summary": "呈现协同作战目标、当前推进阶段及目标机器列表"},
    {"id": "GomokuBoard", "title": "五子棋拟物对弈棋盘彩蛋", "category": "game", "summary": "15x15 原木纹理棋盘、3D 黑白立体落子与绝杀金色光环"},
]

BLACKBOARD_SCHEMAS: dict[str, dict[str, Any]] = {
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
    mid = payload.get("milestone_id")
    if not isinstance(mid, str) or not mid.strip():
        raise AgentApiError("invalid_input", "milestone_id is required")
    mid = mid.strip()

    title = str(payload.get("title") or mid).strip()
    wake_session_key = payload.get("wake_session_key")
    wake_prompt = payload.get("wake_prompt")
    metadata = payload.get("metadata") or {}

    record = {
        "id": mid,
        "title": title,
        "status": "pending",
        "wake_session_key": str(wake_session_key).strip() if wake_session_key else None,
        "wake_prompt": str(wake_prompt).strip() if wake_prompt else None,
        "metadata": metadata if isinstance(metadata, dict) else {},
        "created_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        "resolved_at": None,
        "result": None,
    }

    from sqlalchemy.dialects.sqlite import insert
    from .models import WorkspacePreferenceRow

    db_key = f"milestone:{mid}"
    with ctx.store.session() as db:
        statement = insert(WorkspacePreferenceRow).values(key=db_key, value=record)
        statement = statement.on_conflict_do_update(index_elements=["key"], set_={"value": record})
        db.execute(statement)

    if ctx.service is not None:
        ctx.service._server_event("swarm.milestone.event", agent_id=None, session_id=None, data={"action": "declare", "milestone": record})

    return {"ok": True, "milestone": record}


def _milestone_resolve(payload: dict[str, Any], ctx: AgentContext) -> dict[str, Any]:
    mid = payload.get("milestone_id")
    if not isinstance(mid, str) or not mid.strip():
        raise AgentApiError("invalid_input", "milestone_id is required")
    mid = mid.strip()
    result_data = payload.get("result")

    from sqlalchemy.dialects.sqlite import insert
    from .models import WorkspacePreferenceRow

    db_key = f"milestone:{mid}"
    record = None
    with ctx.store.session() as db:
        row = db.get(WorkspacePreferenceRow, db_key)
        if row and isinstance(row.value, dict):
            record = dict(row.value)
        else:
            record = {
                "id": mid,
                "title": mid,
                "status": "pending",
                "wake_session_key": None,
                "wake_prompt": None,
                "metadata": {},
                "created_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
            }

        record["status"] = "resolved"
        record["resolved_at"] = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
        record["result"] = result_data
        statement = insert(WorkspacePreferenceRow).values(key=db_key, value=record)
        statement = statement.on_conflict_do_update(index_elements=["key"], set_={"value": record})
        db.execute(statement)

        # Optionally sync result to blackboard under milestone result
        if result_data:
            bb_key = f"blackboard:global:milestone_{mid}"
            bb_stmt = insert(WorkspacePreferenceRow).values(key=bb_key, value=result_data)
            bb_stmt = bb_stmt.on_conflict_do_update(index_elements=["key"], set_={"value": result_data})
            db.execute(bb_stmt)

    # Trigger automatic wake-up if configured
    wake_key = record.get("wake_session_key")
    wake_prompt = record.get("wake_prompt")
    wake_dispatched = False
    if wake_key:
        prompt_to_send = wake_prompt or f"前置里程碑 '{record['title']}' ({mid}) 已达成！请开始执行后续任务。"
        try:
            _sessions_send({"key": wake_key, "text": prompt_to_send}, ctx)
            wake_dispatched = True
        except Exception:
            pass

    if ctx.service is not None:
        ctx.service._server_event("swarm.milestone.event", agent_id=None, session_id=None, data={"action": "resolve", "milestone": record, "wake_dispatched": wake_dispatched})

    return {"ok": True, "milestone": record, "wake_dispatched": wake_dispatched}


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
    "plugins.list": _plugins_list,
    "plugins.configure": _plugins_configure,
    "plugins.open": _plugins_open,
    "plugins.close": _plugins_close,
    "machines.dispatch": _machines_dispatch,
    "monitor.sessions.add": _monitor_sessions_add,
    "monitor.sessions.remove": _monitor_sessions_remove,
    "monitor.layout.set": _monitor_layout_set,
    "blackboard.get": _blackboard_get,
    "blackboard.set": _blackboard_set,
    "blackboard.delete": _blackboard_delete,
    "blackboard.components.list": _blackboard_components_list,
    "blackboard.component.schema": _blackboard_component_schema,
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
