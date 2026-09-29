from __future__ import annotations

import hashlib
import json
import logging
import re
import sqlite3
from collections.abc import Callable
from dataclasses import dataclass, field
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from astrorder.connections import paginate_native_session_rows

logger = logging.getLogger(__name__)

_INJECTED_HERMES_MEMORY = re.compile(
    r'(?:\r?\n)*<memory-context>\r?\n'
    r'\[System note: The following is recalled memory context, NOT new user input\.[^\r\n]*\]\r?\n'
    r'[\s\S]*?^</memory-context>(?:\r?\n)*',
    re.MULTILINE,
)
_INJECTED_PONYTAIL = re.compile(
    r'(?:\r?\n)*PONYTAIL MODE ACTIVE — level: (?:lite|full|ultra)\r?\n\r?\n# Ponytail\r?\n'
    r'[\s\S]*?\r?\nThe shortest path to done is the right path\.\s*\Z'
)


def visible_hermes_user_text(text: str) -> str:
    """Remove known model-only additions from Hermes' persisted user row."""
    text = _INJECTED_PONYTAIL.sub('', text)
    if '<memory-context>' in text:
        text = _INJECTED_HERMES_MEMORY.sub(
            lambda match: '\n\n' if text[:match.start()] and text[match.end():] else '', text
        )
    return text


@dataclass(frozen=True)
class NativeDiscovery:
    sessions: list[dict[str, Any]]
    complete: bool
    native_count: int
    project_count: int
    project_complete: bool = True
    projects: list[dict[str, Any]] = field(default_factory=list)


_ACTIVE_STATUS = {"working": "running", "running": "running", "waiting": "waiting_approval"}


def active_native_session_status(rpc: Callable[..., dict[str, Any] | None]) -> dict[str, str]:
    try:
        response = rpc("session.active_list", {})
    except Exception:
        return {}
    result = response.get("result") if isinstance(response, dict) else None
    rows = result.get("sessions") if isinstance(result, dict) else None
    if not isinstance(rows, list):
        return {}
    mapped: dict[str, str] = {}
    for row in rows:
        if not isinstance(row, dict):
            continue
        key = row.get("session_key") or row.get("id")
        raw = row.get("status")
        if isinstance(key, str) and key and raw in _ACTIVE_STATUS:
            mapped[key] = _ACTIVE_STATUS[raw]
    return mapped


def _project_info(node: dict[str, Any]) -> tuple[str | None, str | None, str | None]:
    raw_id = node.get("id") or node.get("project_id") or node.get("key")
    raw_name = node.get("name") or node.get("title") or node.get("label")
    raw_path = node.get("path") or node.get("cwd") or node.get("root") or node.get("repo_root")
    project_id = str(raw_id) if isinstance(raw_id, (str, int)) and raw_id else None
    project_name = str(raw_name) if isinstance(raw_name, str) and raw_name else None
    workspace = str(raw_path) if isinstance(raw_path, str) and raw_path else None
    return project_id, project_name, workspace


def _project_session_map(payload: dict[str, Any] | None) -> dict[str, dict[str, str | None]]:
    result: dict[str, dict[str, str | None]] = {}
    if not isinstance(payload, dict):
        return result
    projects = payload.get("projects")
    if not isinstance(projects, list) and isinstance(payload.get("project"), dict):
        projects = [payload["project"]]
    if not isinstance(projects, list):
        return result

    def is_project_node(node: dict[str, Any]) -> bool:
        node_id = str(node.get("id") or node.get("project_id") or "")
        if "::branch::" in node_id or node.get("type") == "branch":
            return False
        return bool(
            any(key in node for key in ("project_id", "sessionCount", "session_count", "folders"))
            or (
                isinstance(node.get("name") or node.get("title") or node.get("label"), str)
                and any(key in node for key in ("path", "cwd", "root", "repo_root", "repos"))
            )
        )

    def walk(node: Any, inherited: tuple[str | None, str | None, str | None]) -> None:
        if not isinstance(node, dict):
            return
        project = inherited
        if is_project_node(node):
            info = _project_info(node)
            project = tuple(value if value is not None else inherited[index] for index, value in enumerate(info))
        for key in ("sessions", "preview_sessions", "previewSessions", "recent_sessions"):
            children = node.get(key)
            if isinstance(children, list):
                for item in children:
                    if isinstance(item, dict) and isinstance(item.get("id"), (str, int)):
                        result[str(item["id"])] = {
                            "project_id": project[0],
                            "project_name": project[1],
                            "workspace": project[2],
                        }
                    elif isinstance(item, (str, int)) and item:
                        result[str(item)] = {
                            "project_id": project[0],
                            "project_name": project[1],
                            "workspace": project[2],
                        }
        for key in ("repos", "groups", "lanes", "children", "projects"):
            children = node.get(key)
            if isinstance(children, list):
                for child in children:
                    walk(child, project)

    for project in projects:
        walk(project, (None, None, None))
    return result


def discover_native_sessions(
    rpc: Callable[..., dict[str, Any] | None],
    *,
    source_id: str,
    agent_id: str,
    connection_id: str | None,
    profile_name: str,
    default_workspace: str | None,
    page_size: int = 100,
) -> NativeDiscovery:
    rows, complete = paginate_native_session_rows(rpc, page_size=page_size)
    active_status = active_native_session_status(rpc)
    project_response = rpc(
        "projects.tree",
        {
            "preview_limit": 0,
            "session_limit": 10000,
            "include_discovered": True,
            "include_archived": True,
            "include_hidden": True,
        },
    )
    project_result = project_response.get("result") if isinstance(project_response, dict) else None
    if not isinstance(project_result, dict) and isinstance(project_response, dict):
        project_result = project_response
    project_map = _project_session_map(project_result if isinstance(project_result, dict) else None)
    project_ids: set[str] = set()
    expected_session_counts: dict[str, int] = {}
    project_catalog: dict[str, dict[str, Any]] = {}
    if isinstance(project_result, dict):
        project_nodes = project_result.get("projects")
        if isinstance(project_nodes, list):
            for project in project_nodes:
                if not isinstance(project, dict) or not isinstance(project.get("id"), (str, int)):
                    continue
                project_id = str(project["id"])
                project_ids.add(project_id)
                raw_count = project.get("sessionCount", project.get("session_count"))
                if isinstance(raw_count, int) and raw_count >= 0:
                    expected_session_counts[project_id] = raw_count
                project_id_value, project_name, workspace = _project_info(project)
                project_catalog[project_id] = {
                    "project_id": project_id_value or project_id,
                    "project_name": project_name,
                    "workspace": workspace or default_workspace,
                    "source_id": source_id,
                    "connection_id": connection_id,
                    "agent_id": agent_id,
                    "profile_name": profile_name,
                    "session_count": expected_session_counts.get(project_id, 0),
                }
            project_complete = True
            for project_id in sorted(project_ids):
                detail = rpc(
                    "projects.project_sessions",
                    {
                        "project_id": project_id,
                        "session_limit": 10000,
                        "include_archived": True,
                        "include_hidden": True,
                    },
                )
                detail_result = detail.get("result") if isinstance(detail, dict) else None
                if not isinstance(detail_result, dict) and isinstance(detail, dict):
                    detail_result = detail
                project = detail_result.get("project") if isinstance(detail_result, dict) else None
                if not isinstance(project, dict):
                    if expected_session_counts.get(project_id, 0) > 0:
                        project_complete = False
                    continue
                detail_map = _project_session_map({"projects": [project]})
                project_map.update(detail_map)
                actual_count = sum(
                    1 for value in detail_map.values() if value.get("project_id") == project_id
                )
                expected_count = expected_session_counts.get(project_id)
                if expected_count is not None and actual_count < expected_count:
                    project_complete = False
        else:
            project_complete = False
    else:
        project_complete = False
    sessions: list[dict[str, Any]] = []
    for row in rows:
        native_id = row.get("id")
        if not isinstance(native_id, str) or not native_id:
            continue
        durable_id = native_id
        project = project_map.get(native_id, {})
        row_project_id = row.get("project_id")
        row_project_name = row.get("project_name") or row.get("project_title")
        row_workspace = row.get("workspace") or row.get("cwd")
        project_id = project.get("project_id") or (str(row_project_id) if row_project_id else None)
        project_name = project.get("project_name") or (str(row_project_name) if row_project_name else None)
        workspace = project.get("workspace") or row_workspace or default_workspace
        title = row.get("title") or row.get("name") or row.get("preview") or f"Hermes session {native_id[:12]}"
        raw_ts = (
            row.get("updated_at")
            or row.get("last_activity_at")
            or row.get("last_active")
            or row.get("last_active_at")
            or row.get("started_at")
            or row.get("created_at")
        )
        updated_at = None
        if isinstance(raw_ts, (int, float)):
            try:
                updated_at = datetime.fromtimestamp(raw_ts, UTC).isoformat(timespec="milliseconds").replace("+00:00", "Z")
            except Exception:
                pass
        elif isinstance(raw_ts, str) and raw_ts:
            updated_at = raw_ts
        if not updated_at:
            m = re.match(r"^(\d{4})(\d{2})(\d{2})_(\d{2})(\d{2})(\d{2})", native_id)
            if m:
                y, mo, d, h, mi, s = m.groups()
                updated_at = f"{y}-{mo}-{d}T{h}:{mi}:{s}.000Z"
            else:
                updated_at = datetime.now(UTC).isoformat(timespec="milliseconds").replace("+00:00", "Z")
        sessions.append(
            {
                "id": durable_id,
                "agent_id": agent_id,
                "title": str(title),
                "workspace": str(workspace) if workspace else None,
                "status": active_status.get(durable_id) or active_status.get(native_id) or "idle",
                "updated_at": updated_at,
                "source_id": source_id,
                "connection_id": connection_id,
                "source_session_id": native_id,
                "project_id": project_id,
                "project_name": project_name,
                "history_state": "available" if complete else "pending",
                "control_state": "native",
            }
        )
    return NativeDiscovery(
        sessions=sessions,
        complete=complete and project_complete,
        native_count=len(sessions),
        project_count=len(project_ids or {
            value.get("project_id") for value in project_map.values() if value.get("project_id")
        }),
        project_complete=project_complete,
        projects=list(project_catalog.values()),
    )


def native_session_workspace(session_id: str, profile_name: str | None = None) -> str | None:
    """Resolve a local Hermes session's authoritative cwd without resuming it."""
    from astrorder.core.session_usage import _find_local_hermes_state_dbs

    for db_file in _find_local_hermes_state_dbs(profile_name):
        if not db_file.is_file():
            continue
        try:
            with sqlite3.connect(db_file.as_uri() + "?mode=ro", uri=True) as conn:
                row = conn.execute("SELECT cwd FROM sessions WHERE id = ?", (session_id,)).fetchone()
            if row and isinstance(row[0], str) and row[0].strip():
                return row[0]
        except (sqlite3.Error, OSError) as exc:
            logger.debug("Could not read native Hermes workspace from %s: %s", db_file, exc)
    return None


def discover_native_sessions_from_db(
    *,
    db_path: Path | str | None = None,
    source_id: str,
    agent_id: str,
    connection_id: str | None = None,
    profile_name: str | None = None,
    default_workspace: str | None = None,
) -> tuple[NativeDiscovery, list[str]]:
    target_db: Path | None = None
    if db_path is not None:
        p = Path(db_path)
        if p.is_file():
            target_db = p
    if target_db is None:
        from astrorder.core.session_usage import _find_local_hermes_state_dbs

        dbs = _find_local_hermes_state_dbs(profile_name)
        target_db = next((p for p in dbs if p.is_file()), None)

    if target_db is None:
        return (
            NativeDiscovery(
                sessions=[],
                complete=False,
                native_count=0,
                project_count=0,
                project_complete=False,
            ),
            [],
        )

    proj_db = target_db.parent / "projects.db"
    project_folders: dict[str, str] = {}
    project_catalog: dict[str, dict[str, Any]] = {}
    if proj_db.is_file():
        try:
            with sqlite3.connect(proj_db.as_uri() + "?mode=ro", uri=True) as p_conn:
                p_cur = p_conn.cursor()
                for r in p_cur.execute("SELECT id, name, primary_path FROM projects WHERE archived=0").fetchall():
                    pid = str(r[0])
                    pname = str(r[1]) if r[1] else pid
                    ppath = str(r[2]) if r[2] else default_workspace
                    project_catalog[pid] = {
                        "project_id": pid,
                        "project_name": pname,
                        "workspace": ppath,
                        "source_id": source_id,
                        "connection_id": connection_id,
                        "agent_id": agent_id,
                        "profile_name": profile_name or "default",
                        "session_count": 0,
                    }
                for r in p_cur.execute("SELECT project_id, path FROM project_folders").fetchall():
                    pid, fpath = str(r[0]), str(r[1])
                    clean = fpath.replace("/", "\\").lower().rstrip("\\")
                    project_folders[clean] = pid
        except Exception as exc:
            logger.debug("Failed reading Hermes projects.db at %s: %s", proj_db, exc)

    sessions: list[dict[str, Any]] = []
    archived_ids: list[str] = []
    try:
        with sqlite3.connect(target_db.as_uri() + "?mode=ro", uri=True) as s_conn:
            s_conn.row_factory = sqlite3.Row
            s_cur = s_conn.cursor()
            cols = {row[1] for row in s_cur.execute("PRAGMA table_info(sessions)").fetchall()}
            if not {"id", "title", "cwd", "started_at"}.issubset(cols):
                return (
                    NativeDiscovery(
                        sessions=[],
                        complete=False,
                        native_count=0,
                        project_count=0,
                        project_complete=False,
                    ),
                    [],
                )

            if "archived" in cols:
                for r in s_cur.execute("SELECT id FROM sessions WHERE archived = 1").fetchall():
                    archived_ids.append(str(r["id"]))

            query = "SELECT * FROM sessions"
            if "archived" in cols:
                query += " WHERE archived = 0"
            query += " ORDER BY started_at DESC"

            for row in s_cur.execute(query).fetchall():
                native_id = row["id"]
                if not isinstance(native_id, str) or not native_id:
                    continue
                cwd = row["cwd"]
                workspace = cwd or default_workspace
                project_id = None
                project_name = None
                if cwd:
                    clean_cwd = cwd.replace("/", "\\").lower().rstrip("\\")
                    project_id = project_folders.get(clean_cwd)
                    if not project_id:
                        for fpath, pid in project_folders.items():
                            if clean_cwd == fpath or clean_cwd.startswith(fpath + "\\"):
                                project_id = pid
                                break
                    if project_id and project_id in project_catalog:
                        project_name = project_catalog[project_id]["project_name"]
                        project_catalog[project_id]["session_count"] += 1

                title = row["title"] or f"Hermes session {native_id[:12]}"
                raw_ts = (
                    row["last_activity_at"]
                    if "last_activity_at" in cols and row["last_activity_at"]
                    else row["started_at"]
                )
                updated_at = None
                if isinstance(raw_ts, (int, float)) and raw_ts > 0:
                    try:
                        updated_at = (
                            datetime.fromtimestamp(raw_ts, UTC)
                            .isoformat(timespec="milliseconds")
                            .replace("+00:00", "Z")
                        )
                    except Exception:
                        pass
                if not updated_at:
                    m = re.match(r"^(\d{4})(\d{2})(\d{2})_(\d{2})(\d{2})(\d{2})", native_id)
                    if m:
                        y, mo, d, h, mi, s = m.groups()
                        updated_at = f"{y}-{mo}-{d}T{h}:{mi}:{s}.000Z"
                    else:
                        updated_at = (
                            datetime.now(UTC)
                            .isoformat(timespec="milliseconds")
                            .replace("+00:00", "Z")
                        )

                sessions.append(
                    {
                        "id": native_id,
                        "agent_id": agent_id,
                        "title": str(title),
                        "workspace": str(workspace) if workspace else None,
                        "status": "idle",
                        "updated_at": updated_at,
                        "source_id": source_id,
                        "connection_id": connection_id,
                        "source_session_id": native_id,
                        "project_id": project_id,
                        "project_name": project_name,
                        "history_state": "available",
                        "control_state": "owned",
                    }
                )
    except Exception as exc:
        logger.debug("Failed reading Hermes state.db at %s: %s", target_db, exc)
        return (
            NativeDiscovery(
                sessions=[],
                complete=False,
                native_count=0,
                project_count=0,
                project_complete=False,
            ),
            [],
        )

    return (
        NativeDiscovery(
            sessions=sessions,
            complete=True,
            native_count=len(sessions),
            project_count=len(project_catalog),
            project_complete=True,
            projects=list(project_catalog.values()),
        ),
        archived_ids,
    )


def _message_text(value: Any) -> str:
    if isinstance(value, str):
        return value
    if isinstance(value, list):
        pieces: list[str] = []
        for item in value:
            if isinstance(item, str):
                pieces.append(item)
            elif isinstance(item, dict) and isinstance(item.get("text"), str):
                pieces.append(item["text"])
        return "".join(pieces)
    if value is None:
        return ""
    return json.dumps(value, ensure_ascii=False, sort_keys=True)


def history_messages(
    rpc: Callable[..., dict[str, Any] | None],
    *,
    durable_session_id: str,
    native_session_id: str,
    source_id: str,
    agent_id: str,
) -> list[dict[str, Any]]:
    response = rpc(
        "session.resume",
        {"session_id": native_session_id, "lazy": True, "defer_history": False, "omit_messages": False},
        timeout=30,
    )
    result = response.get("result") if isinstance(response, dict) else None
    raw_messages = result.get("messages") if isinstance(result, dict) else None
    if not isinstance(raw_messages, list):
        raise TypeError("native session.resume did not return messages")
    return project_history_messages(raw_messages, durable_session_id=durable_session_id, native_session_id=native_session_id, source_id=source_id, agent_id=agent_id)


def project_history_messages(raw_messages, *, durable_session_id, native_session_id, source_id, agent_id):
    from astrorder.core.handoff import visible_handoff_user_text

    messages: list[dict[str, Any]] = []
    anchor = "start"
    activity_index = 0
    previous_time = "1970-01-01T00:00:00Z"
    for item in raw_messages:
        if not isinstance(item, dict):
            continue
        item = dict(item)
        content = item.get('content')
        if isinstance(content, str) and content.startswith('\x00json:'):
            try:
                item['content'] = json.loads(content[6:])
            except ValueError:
                pass
        role = str(item.get("role") or item.get("type") or "assistant")
        native_row_id = item.get("row_id") or item.get("message_id") or item.get("id")
        if not isinstance(native_row_id, (str, int)) or not str(native_row_id):
            if role != "tool":
                continue
            # Hermes emits tool display annotations without row IDs. Scope their
            # rendering keys to the preceding native row in this full snapshot.
            # These are display annotations, never native session identities.
            activity_index += 1
            native_row_id = f"annotation:{anchor}:tool:{activity_index}"
        else:
            anchor = str(native_row_id)
            activity_index = 0
        kind = "tool" if role == "tool" else "message"
        created_at = item.get("created_at") or item.get("timestamp")
        if isinstance(created_at, (int, float)):
            created_at = datetime.fromtimestamp(created_at, UTC).isoformat(timespec="milliseconds").replace("+00:00", "Z")
        if not isinstance(created_at, str) or not created_at:
            created_at = previous_time
        previous_time = created_at
        message_key = hashlib.sha256(
            f"{source_id}\0{native_session_id}\0{native_row_id}".encode()
        ).hexdigest()[:48]
        message_text = _message_text(
            item.get("text") if "text" in item else item.get("content") or item.get("context")
        )
        if role == "user":
            message_text = visible_handoff_user_text(message_text)
            if agent_id.startswith(('local-hermes-', 'ssh-hermes-')):
                message_text = visible_hermes_user_text(message_text)
        messages.append(
            {
                "id": f"history-message-{message_key}",
                "session_id": durable_session_id,
                "agent_id": agent_id,
                "role": role if role in {"user", "assistant", "system", "tool"} else "system",
                "kind": kind,
                "text": message_text,
                "attachments": [],
                "created_at": created_at,
                "command_id": None,
                "tool": (item.get("tool") if isinstance(item.get("tool"), dict) else {
                    "name": item.get("name") or item.get("tool_name") or "tool",
                    "arguments": item.get("args") or {},
                } if kind == "tool" else None),
            }
        )
        reasoning = next((item.get(key) for key in ("reasoning", "reasoning_content", "reasoning_details", "codex_reasoning_items") if item.get(key)), None)
        base = messages[-1]
        if role == "assistant" and reasoning:
            base = messages.pop()
            thought = {**base, "kind": "thinking", "text": _message_text(reasoning)}
            if base["text"].strip():
                thought["id"] = base["id"] + ":thinking"
                messages.extend([thought, base])
            else:
                messages.append(thought)
        calls = item.get('tool_calls')
        if isinstance(calls, str):
            try:
                calls = json.loads(calls)
            except ValueError:
                calls = None
        if role == 'assistant' and isinstance(calls, list):
            for index, call in enumerate(calls):
                if not isinstance(call, dict) or not isinstance(call.get('function'), dict):
                    continue
                function = call['function']
                arguments = function.get('arguments') or {}
                if isinstance(arguments, str):
                    try:
                        arguments = json.loads(arguments)
                    except ValueError:
                        pass
                messages.append({**base, 'id': base['id'] + ':tool:' + str(call.get('id') or index), 'role': 'tool', 'kind': 'tool', 'text': '', 'tool': {'name': function.get('name') or 'tool', 'arguments': arguments}})
    return messages
