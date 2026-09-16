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
    return {
        "key": session_key(session),
        "agent_id": session.get("agent_id"),
        "title": session.get("title") or "",
        "status": session.get("status"),
        "updated_at": session.get("updated_at"),
        "project_name": session.get("project_name"),
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

    data: dict[str, Any] = {}
    if ctx.runtime_resolver is not None:
        runtime = ctx.runtime_resolver(agent_id)
        try:
            create = getattr(runtime, "create", None)
            if callable(create):
                data = create(workspace, title)
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

    event_data = {
        "action": "open",
        "plugin_id": plugin_id,
        "agent_id": agent_id,
        "session_id": session_id,
        "path": payload.get("path"),
        "url": payload.get("url"),
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
    create_payload = {
        "agent_id": candidate["id"],
        "title": payload.get("title") or f"远程派生任务 ({machine_id})",
        "workspace": payload.get("workspace"),
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
}


def invoke(capability: str, payload: dict[str, Any] | None, ctx: AgentContext) -> dict[str, Any]:
    handler = HANDLERS.get(capability)
    if handler is None:
        raise AgentApiError("unknown_capability", f"Unknown capability: {capability}")
    return handler(payload or {}, ctx)
from datetime import datetime, timezone
