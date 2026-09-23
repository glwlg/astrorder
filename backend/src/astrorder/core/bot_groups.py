"""Bots Group (Multi-Agent Chat Collaboration) dedicated domain logic and endpoints."""

from __future__ import annotations

import asyncio
import json
import logging
import re
import time
from datetime import datetime, timezone
from typing import Any
from uuid import uuid4

from fastapi import APIRouter, HTTPException, Query, Request
from pydantic import BaseModel, Field

from .auth import require_browser

logger = logging.getLogger(__name__)

router = APIRouter()

PREF_PREFIX = "bot_group:"
MSG_PREF_PREFIX = "bot_group_msgs:"


class BotGroupMember(BaseModel):
    machine_id: str = Field(min_length=1, max_length=128)
    agent_id: str = Field(min_length=1, max_length=256)
    name: str | None = Field(default=None, max_length=128)
    alias: str | None = Field(default=None, max_length=128)
    system_role_prompt: str | None = Field(default=None, max_length=2000)
    model_provider: str | None = Field(default=None, max_length=160)
    model_name: str | None = Field(default=None, max_length=160)
    thinking_effort: str | None = Field(default=None, max_length=32)
    approval_policy: str | None = Field(default=None, max_length=32)
    workspace: str | None = Field(default=None, max_length=2000)


class CreateBotGroupPayload(BaseModel):
    name: str = Field(min_length=1, max_length=128)
    description: str | None = Field(default=None, max_length=512)
    members: list[BotGroupMember] = Field(min_length=1, max_length=16)
    max_hops: int = Field(default=3, ge=1, le=10)


class UpdateBotGroupPayload(BaseModel):
    name: str | None = Field(default=None, min_length=1, max_length=128)
    description: str | None = Field(default=None, max_length=512)
    members: list[BotGroupMember] | None = Field(default=None, min_length=1, max_length=16)
    max_hops: int | None = Field(default=None, ge=1, le=10)


class SendGroupMessagePayload(BaseModel):
    text: str = Field(min_length=1, max_length=32000)
    target_agent_id: str | None = Field(default=None, max_length=256)
    mentions: list[str] = Field(default_factory=list)


def _now_iso() -> str:
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def _private(request: Request) -> None:
    require_browser(request, request.app.state.settings)


def _set_member_state(
    request: Request,
    group_id: str,
    agent_id: str,
    status: str,
    *,
    session_id: str | None = None,
    error: str | None = None,
) -> None:
    from astrorder.models import WorkspacePreferenceRow
    from sqlalchemy.dialects.sqlite import insert

    store = request.app.state.store
    with store.session() as db:
        row = db.get(WorkspacePreferenceRow, f"{PREF_PREFIX}{group_id}")
        if not row or not isinstance(row.value, dict):
            return
        group = dict(row.value)
        states = dict(group.get("member_states") or {})
        state = dict(states.get(agent_id) or {})
        state.update({"status": status, "updated_at": _now_iso()})
        if session_id:
            state["session_id"] = session_id
        if error:
            state["last_error"] = error[:1000]
        else:
            state.pop("last_error", None)
        states[agent_id] = state
        group["member_states"] = states
        group["updated_at"] = _now_iso()
        stmt = insert(WorkspacePreferenceRow).values(key=f"{PREF_PREFIX}{group_id}", value=group)
        db.execute(stmt.on_conflict_do_update(index_elements=["key"], set_={"value": group}))
    if request.app.state.service is not None:
        request.app.state.service._server_event(
            "bot_group.upsert", agent_id=None, session_id=None, data=group
        )


def _append_agent_message(
    request: Request,
    group_id: str,
    agent_id: str,
    agent_name: str,
    text: str,
    hop_count: int,
    members: list[dict[str, Any]],
) -> dict[str, Any]:
    from astrorder.models import WorkspacePreferenceRow
    from sqlalchemy.dialects.sqlite import insert

    store = request.app.state.store
    message = {
        "id": f"gmsg-{uuid4().hex[:16]}",
        "group_id": group_id,
        "sender_type": "agent",
        "sender_id": agent_id,
        "sender_name": agent_name,
        "text": text,
        "mentions": parse_mentions(text, members),
        "hop_count": hop_count,
        "created_at": _now_iso(),
    }
    with store.session() as db:
        row = db.get(WorkspacePreferenceRow, f"{MSG_PREF_PREFIX}{group_id}")
        history = list(row.value) if (row and isinstance(row.value, list)) else []
        history.append(message)
        stmt = insert(WorkspacePreferenceRow).values(
            key=f"{MSG_PREF_PREFIX}{group_id}", value=history
        )
        db.execute(stmt.on_conflict_do_update(index_elements=["key"], set_={"value": history}))
    if request.app.state.service is not None:
        request.app.state.service._server_event(
            "bot_group.message", agent_id=None, session_id=None, data=message
        )
    return message


def _finish_speaker(request: Request, group_id: str, agent_id: str) -> None:
    from astrorder.models import WorkspacePreferenceRow
    from sqlalchemy.dialects.sqlite import insert

    store = request.app.state.store
    with store.session() as db:
        row = db.get(WorkspacePreferenceRow, f"{PREF_PREFIX}{group_id}")
        if not row or not isinstance(row.value, dict):
            return
        group = dict(row.value)
        speakers = set(group.get("active_speakers") or [])
        speakers.discard(agent_id)
        group["active_speakers"] = list(speakers)
        group["active_hop"] = group.get("active_hop", 0) if speakers else 0
        group["active_speaker_agent_id"] = next(iter(speakers), None)
        stmt = insert(WorkspacePreferenceRow).values(key=f"{PREF_PREFIX}{group_id}", value=group)
        db.execute(stmt.on_conflict_do_update(index_elements=["key"], set_={"value": group}))
    if request.app.state.service is not None:
        request.app.state.service._server_event(
            "bot_group.upsert", agent_id=None, session_id=None, data=group
        )


def _blackboard_signature(store: Any, group_id: str) -> str:
    from astrorder.models import WorkspacePreferenceRow
    from sqlalchemy import select

    prefix = f"blackboard:group:{group_id}:"
    with store.session() as db:
        rows = db.scalars(
            select(WorkspacePreferenceRow).where(WorkspacePreferenceRow.key.startswith(prefix))
        ).all()
        return json.dumps(
            sorted((row.key, row.value) for row in rows),
            ensure_ascii=False,
            sort_keys=True,
            default=str,
        )


def parse_mentions(text: str, members: list[dict[str, Any]]) -> list[str]:
    lower_text = text.lower()
    if "@all" in lower_text or "@所有人" in lower_text:
        return [m.get("agent_id", "") for m in members if m.get("agent_id")]

    matches: list[tuple[int, str]] = []
    for m in members:
        aid = m.get("agent_id", "")
        name = m.get("name", "").lower()
        alias = (m.get("alias") or "").lower()
        patterns = [f"@{aid.lower()}"]
        if name:
            patterns.append(f"@{name}")
            for part in re.split(r"[\s·\-_]+", name):
                if len(part) >= 2:
                    patterns.append(f"@{part}")
        if alias:
            patterns.append(f"@{alias}")
        for p in patterns:
            pos = lower_text.find(p)
            if pos != -1:
                matches.append((pos, aid))
                break
    matches.sort(key=lambda x: x[0])
    seen: set[str] = set()
    ordered: list[str] = []
    for _, aid in matches:
        if aid not in seen:
            seen.add(aid)
            ordered.append(aid)
    return ordered


async def _dispatch_group_turn(
    request: Request,
    group_id: str,
    target_agent_id: str,
    prompt: str,
    hop_count: int,
    broadcast_queue: list[str] | None = None,
) -> None:
    from astrorder.models import WorkspacePreferenceRow
    from sqlalchemy.dialects.sqlite import insert

    store = request.app.state.store
    with store.session() as db:
        row = db.get(WorkspacePreferenceRow, f"{PREF_PREFIX}{group_id}")
        if not row or not isinstance(row.value, dict):
            return
        group = dict(row.value)
        if hop_count > group.get("max_hops", 3):
            group["active_hop"] = 0
            group["active_speaker_agent_id"] = None
            stmt = insert(WorkspacePreferenceRow).values(
                key=f"{PREF_PREFIX}{group_id}", value=group
            )
            stmt = stmt.on_conflict_do_update(index_elements=["key"], set_={"value": group})
            db.execute(stmt)
            if request.app.state.service is not None:
                request.app.state.service._server_event(
                    "bot_group.upsert", agent_id=None, session_id=None, data=group
                )
            return

        speakers = set(group.get("active_speakers") or [])
        speakers.add(target_agent_id)
        group["active_speakers"] = list(speakers)
        group["active_speaker_agent_id"] = target_agent_id
        group["active_hop"] = max(group.get("active_hop", 0), hop_count)
        stmt = insert(WorkspacePreferenceRow).values(key=f"{PREF_PREFIX}{group_id}", value=group)
        stmt = stmt.on_conflict_do_update(index_elements=["key"], set_={"value": group})
        db.execute(stmt)

    if request.app.state.service is not None:
        request.app.state.service._server_event(
            "bot_group.upsert", agent_id=None, session_id=None, data=group
        )

    member = next(
        (m for m in group.get("members", []) if m.get("agent_id") == target_agent_id), None
    )
    if member is None:
        _set_member_state(request, group_id, target_agent_id, "failed", error="群成员配置不存在")
        return
    _set_member_state(request, group_id, target_agent_id, "running")

    session_row_key = f"group_session:{group_id}:{target_agent_id}"
    agent_session_id = None
    with store.session() as db:
        srow = db.get(WorkspacePreferenceRow, session_row_key)
        if srow and isinstance(srow.value, str):
            candidate = srow.value.strip('"')
            cand_sess = store.get_session(target_agent_id, candidate)
            # Avoid reusing hijacked desktop sessions
            if cand_sess and not str(cand_sess.get("title", "")).startswith("功能导航"):
                agent_session_id = candidate

    sess = store.get_session(target_agent_id, agent_session_id) if agent_session_id else None
    configured_workspace = (member.get("workspace") or "").strip()
    if sess and configured_workspace:
        current_workspace = str(sess.get("workspace") or "").replace("\\", "/").rstrip("/").lower()
        expected_workspace = configured_workspace.replace("\\", "/").rstrip("/").lower()
        if current_workspace != expected_workspace:
            sess = None
            agent_session_id = None
    create_error: str | None = None
    if not sess or agent_session_id == "01a09e7a-b581-7882-a22d-dfb99d29c52c":
        existing_sessions = store.list_sessions(target_agent_id)
        target_ws = configured_workspace or next(
            (s.get("workspace") for s in existing_sessions if s.get("workspace")), None
        )
        try:
            from .api import CreateSessionPayload, create_session

            created = await asyncio.to_thread(
                create_session,
                CreateSessionPayload(
                    agent_id=target_agent_id,
                    workspace=target_ws,
                    title=f"群聊专属「{group.get('name')}」",
                    ephemeral=False,
                    blackboard_scope="group",
                    blackboard_scope_id=group_id,
                ),
                request,
            )
            agent_session_id = created.get("id")
        except Exception as create_err:
            create_error = str(create_err)
            logger.warning("create_session failed for %s: %s", target_agent_id, create_err)

        if agent_session_id:
            with store.session() as db:
                stmt = insert(WorkspacePreferenceRow).values(
                    key=session_row_key, value=agent_session_id
                )
                stmt = stmt.on_conflict_do_update(
                    index_elements=["key"], set_={"value": agent_session_id}
                )
                db.execute(stmt)
    if not agent_session_id:
        _set_member_state(
            request,
            group_id,
            target_agent_id,
            "failed",
            error=create_error or "无法创建群聊专属会话",
        )
        _finish_speaker(request, group_id, target_agent_id)
        return

    store.set_session_blackboard_namespace(
        target_agent_id, agent_session_id, f"group:{group_id}"
    )

    _set_member_state(request, group_id, target_agent_id, "running", session_id=agent_session_id)

    try:
        from .api import (
            SessionApprovalModeSelection,
            SessionModelSelection,
            SessionReasoningSelection,
            session_approval_mode_select,
            session_model,
            session_reasoning,
        )

        if member.get("model_provider") and member.get("model_name"):
            await asyncio.to_thread(
                session_model,
                agent_session_id,
                SessionModelSelection(
                    agent_id=target_agent_id,
                    provider=member["model_provider"],
                    model=member["model_name"],
                ),
                request,
            )
        if member.get("thinking_effort"):
            await asyncio.to_thread(
                session_reasoning,
                agent_session_id,
                SessionReasoningSelection(
                    agent_id=target_agent_id, effort=member["thinking_effort"]
                ),
                request,
            )
        if member.get("approval_policy"):
            await asyncio.to_thread(
                session_approval_mode_select,
                agent_session_id,
                SessionApprovalModeSelection(
                    agent_id=target_agent_id, mode=member["approval_policy"]
                ),
                request,
            )
    except Exception as exc:
        detail = getattr(exc, "detail", None) or str(exc) or "成员运行配置应用失败"
        _set_member_state(
            request,
            group_id,
            target_agent_id,
            "failed",
            session_id=agent_session_id,
            error=detail,
        )
        _finish_speaker(request, group_id, target_agent_id)
        return

    members = group.get("members", [])
    member_lines = []
    for m in members:
        line = f"- @{m.get('name') or m.get('agent_id')}"
        if m.get("alias"):
            line += f" ({m.get('alias')})"
        member_lines.append(line)

    with store.session() as db:
        msg_row = db.get(WorkspacePreferenceRow, f"{MSG_PREF_PREFIX}{group_id}")
        recent_history = (
            list(msg_row.value)[-6:] if (msg_row and isinstance(msg_row.value, list)) else []
        )

    formatted_history = []
    for h in recent_history:
        formatted_history.append(f"[{h.get('sender_name', '成员')}]: {h.get('text', '')}")

    instructions = [
        f"你正在参与星序多 Agent 协作群「{group.get('name')}」。",
        *(
            [f"你的群内职责：{member['system_role_prompt']}"]
            if member.get("system_role_prompt")
            else []
        ),
        "群成员列表:",
        *member_lines,
        "",
        "群聊规则:",
        "1. 直截了当地回复与解决问题。",
        "2. 若需将后续任务交接给群内其他成员，必须明确在回复中输出 @成员名称；若当前任务已解决且无需其他 Agent 动作，请不要 @ 任何人，系统将自动交还给人类指挥官。",
        f"3. 共享结果可写入群聊黑板 session_key=group::{group_id}，无论是否写入黑板，结束前都必须在群聊中发送简短的完成结果或失败原因。",
        "",
        "【最新群对话记录】:",
        *formatted_history,
    ]
    system_inst = "\n".join(instructions)

    command_id = f"cmd-g-{uuid4().hex[:16]}"
    cmd = {
        "id": command_id,
        "agent_id": target_agent_id,
        "session_id": agent_session_id,
        "action": "send",
        "text": system_inst,
        "attachment_ids": [],
        "target_id": None,
    }
    blackboard_before = _blackboard_signature(store, group_id)

    baseline_message_id = None
    with store.session() as db:
        from astrorder.models import MessageRow
        from sqlalchemy import select

        baseline = db.scalars(
            select(MessageRow)
            .where(
                MessageRow.agent_id == target_agent_id, MessageRow.session_id == agent_session_id
            )
            .order_by(MessageRow.created_at.desc())
            .limit(1)
        ).first()
        baseline_message_id = baseline.id if baseline else None

    if request.app.state.service is not None:
        try:
            submitted = await request.app.state.service.submit_browser_command(cmd)
        except Exception as exc:
            logger.warning("Group command dispatch error: %s", exc)
            _set_member_state(
                request,
                group_id,
                target_agent_id,
                "failed",
                session_id=agent_session_id,
                error=str(exc),
            )
            _finish_speaker(request, group_id, target_agent_id)
            return
    else:
        _set_member_state(
            request,
            group_id,
            target_agent_id,
            "failed",
            session_id=agent_session_id,
            error="服务端命令通道不可用",
        )
        _finish_speaker(request, group_id, target_agent_id)
        return

    async def poll_and_relay():
        start_time = time.time()
        agent_name = next(
            (m.get("name") for m in members if m.get("agent_id") == target_agent_id),
            target_agent_id,
        )
        completed_at: float | None = None
        while time.time() - start_time < 120:
            await asyncio.sleep(2)
            command = store.get_command(target_agent_id, agent_session_id, command_id)
            command_state = (command or submitted).get("state")
            if command_state in {"failed", "cancelled", "unknown"}:
                detail = (command or submitted).get("error") or f"任务状态：{command_state}"
                _append_agent_message(
                    request,
                    group_id,
                    target_agent_id,
                    agent_name,
                    f"执行失败：{detail}",
                    hop_count,
                    members,
                )
                _set_member_state(
                    request,
                    group_id,
                    target_agent_id,
                    "failed",
                    session_id=agent_session_id,
                    error=detail,
                )
                break
            if command_state == "completed" and completed_at is None:
                completed_at = time.time()
            with store.session() as db:
                from astrorder.models import MessageRow
                from sqlalchemy import select

                latest = db.scalars(
                    select(MessageRow)
                    .where(
                        MessageRow.agent_id == target_agent_id,
                        MessageRow.session_id == agent_session_id,
                    )
                    .order_by(MessageRow.created_at.desc())
                    .limit(1)
                ).first()
                if (
                    latest
                    and latest.id != baseline_message_id
                    and latest.role == "assistant"
                    and latest.text
                ):
                    reply_text = latest.text.strip()
                    bot_msg = _append_agent_message(
                        request,
                        group_id,
                        target_agent_id,
                        agent_name,
                        reply_text,
                        hop_count,
                        members,
                    )
                    agent_mentions = bot_msg["mentions"]
                    _set_member_state(
                        request,
                        group_id,
                        target_agent_id,
                        "success",
                        session_id=agent_session_id,
                    )

                    # Parallel relay: trigger any agents mentioned by this reply
                    for next_target in agent_mentions:
                        if next_target != target_agent_id:
                            asyncio.create_task(
                                _dispatch_group_turn(
                                    request, group_id, next_target, reply_text, hop_count + 1, None
                                )
                            )
                    break
            if completed_at is not None and time.time() - completed_at >= 4:
                wrote_blackboard = _blackboard_signature(store, group_id) != blackboard_before
                _append_agent_message(
                    request,
                    group_id,
                    target_agent_id,
                    agent_name,
                    "任务已完成，结果已写入群聊黑板。"
                    if wrote_blackboard
                    else "任务已完成，但 Agent 未返回群聊摘要。",
                    hop_count,
                    members,
                )
                _set_member_state(
                    request,
                    group_id,
                    target_agent_id,
                    "success",
                    session_id=agent_session_id,
                )
                break
        else:
            detail = "等待 Agent 回复超时"
            _append_agent_message(
                request,
                group_id,
                target_agent_id,
                agent_name,
                f"执行失败：{detail}",
                hop_count,
                members,
            )
            _set_member_state(
                request,
                group_id,
                target_agent_id,
                "failed",
                session_id=agent_session_id,
                error=detail,
            )
        _finish_speaker(request, group_id, target_agent_id)

    asyncio.create_task(poll_and_relay())


@router.get("/api/v1/bot-groups")
def list_bot_groups(request: Request) -> dict[str, Any]:
    _private(request)
    store = request.app.state.store
    from sqlalchemy import select
    from astrorder.models import WorkspacePreferenceRow

    groups: list[dict[str, Any]] = []
    with store.session() as db:
        rows = db.scalars(
            select(WorkspacePreferenceRow).where(WorkspacePreferenceRow.key.like(f"{PREF_PREFIX}%"))
        ).all()
        for r in rows:
            if isinstance(r.value, dict):
                groups.append(r.value)
    groups.sort(key=lambda g: str(g.get("created_at") or ""), reverse=True)
    return {"items": groups, "count": len(groups)}


@router.post("/api/v1/bot-groups")
def create_bot_group(payload: CreateBotGroupPayload, request: Request) -> dict[str, Any]:
    _private(request)
    store = request.app.state.store
    from sqlalchemy.dialects.sqlite import insert
    from astrorder.models import WorkspacePreferenceRow

    gid = f"group-{uuid4().hex[:16]}"
    now = _now_iso()
    record = {
        "id": gid,
        "name": payload.name.strip(),
        "description": (payload.description or "").strip(),
        "members": [m.model_dump() for m in payload.members],
        "max_hops": payload.max_hops,
        "active_hop": 0,
        "active_speaker_agent_id": None,
        "active_speakers": [],
        "member_states": {
            member.agent_id: {"status": "idle", "updated_at": now} for member in payload.members
        },
        "created_at": now,
        "updated_at": now,
    }

    with store.session() as db:
        stmt = insert(WorkspacePreferenceRow).values(key=f"{PREF_PREFIX}{gid}", value=record)
        stmt = stmt.on_conflict_do_update(index_elements=["key"], set_={"value": record})
        db.execute(stmt)

    if request.app.state.service is not None:
        request.app.state.service._server_event(
            "bot_group.upsert", agent_id=None, session_id=None, data=record
        )

    return {"group": record, "ok": True}


@router.get("/api/v1/bot-groups/{group_id}")
def get_bot_group(group_id: str, request: Request) -> dict[str, Any]:
    _private(request)
    store = request.app.state.store
    from astrorder.models import WorkspacePreferenceRow

    with store.session() as db:
        row = db.get(WorkspacePreferenceRow, f"{PREF_PREFIX}{group_id}")
        if not row or not isinstance(row.value, dict):
            raise HTTPException(status_code=404, detail="群聊未找到")
        return {"group": row.value}


@router.patch("/api/v1/bot-groups/{group_id}")
def update_bot_group(
    group_id: str, payload: UpdateBotGroupPayload, request: Request
) -> dict[str, Any]:
    _private(request)
    store = request.app.state.store
    from sqlalchemy.dialects.sqlite import insert
    from astrorder.models import WorkspacePreferenceRow

    with store.session() as db:
        row = db.get(WorkspacePreferenceRow, f"{PREF_PREFIX}{group_id}")
        if not row or not isinstance(row.value, dict):
            raise HTTPException(status_code=404, detail="群聊未找到")
        record = dict(row.value)
        if payload.name is not None:
            record["name"] = payload.name.strip()
        if payload.description is not None:
            record["description"] = payload.description.strip()
        if payload.members is not None:
            record["members"] = [m.model_dump() for m in payload.members]
            states = record.get("member_states") or {}
            record["member_states"] = {
                member.agent_id: states.get(member.agent_id)
                or {"status": "idle", "updated_at": _now_iso()}
                for member in payload.members
            }
        if payload.max_hops is not None:
            record["max_hops"] = payload.max_hops
        record["updated_at"] = _now_iso()

        stmt = insert(WorkspacePreferenceRow).values(key=f"{PREF_PREFIX}{group_id}", value=record)
        stmt = stmt.on_conflict_do_update(index_elements=["key"], set_={"value": record})
        db.execute(stmt)

    if request.app.state.service is not None:
        request.app.state.service._server_event(
            "bot_group.upsert", agent_id=None, session_id=None, data=record
        )

    return {"group": record, "ok": True}


@router.delete("/api/v1/bot-groups/{group_id}")
def delete_bot_group(group_id: str, request: Request) -> dict[str, Any]:
    _private(request)
    store = request.app.state.store
    from astrorder.models import WorkspacePreferenceRow

    with store.session() as db:
        row = db.get(WorkspacePreferenceRow, f"{PREF_PREFIX}{group_id}")
        if row:
            db.delete(row)
        msg_row = db.get(WorkspacePreferenceRow, f"{MSG_PREF_PREFIX}{group_id}")
        if msg_row:
            db.delete(msg_row)
        from sqlalchemy import delete

        db.execute(
            delete(WorkspacePreferenceRow).where(
                WorkspacePreferenceRow.key.startswith(f"blackboard:group:{group_id}:")
            )
        )

    if request.app.state.service is not None:
        request.app.state.service._server_event(
            "bot_group.delete", agent_id=None, session_id=None, data={"id": group_id}
        )

    return {"ok": True, "id": group_id}


@router.get("/api/v1/bot-groups/{group_id}/messages")
def list_group_messages(group_id: str, request: Request) -> dict[str, Any]:
    _private(request)
    store = request.app.state.store
    from astrorder.models import WorkspacePreferenceRow

    with store.session() as db:
        row = db.get(WorkspacePreferenceRow, f"{MSG_PREF_PREFIX}{group_id}")
        msgs: list[dict[str, Any]] = row.value if (row and isinstance(row.value, list)) else []
        return {"items": msgs, "count": len(msgs)}


@router.post("/api/v1/bot-groups/{group_id}/messages")
async def send_group_message(
    group_id: str, payload: SendGroupMessagePayload, request: Request
) -> dict[str, Any]:
    _private(request)
    store = request.app.state.store
    from sqlalchemy.dialects.sqlite import insert
    from astrorder.models import WorkspacePreferenceRow

    with store.session() as db:
        group_row = db.get(WorkspacePreferenceRow, f"{PREF_PREFIX}{group_id}")
        if not group_row or not isinstance(group_row.value, dict):
            raise HTTPException(status_code=404, detail="群聊不存在")
        group = dict(group_row.value)

        msg_row = db.get(WorkspacePreferenceRow, f"{MSG_PREF_PREFIX}{group_id}")
        history: list[dict[str, Any]] = (
            list(msg_row.value) if (msg_row and isinstance(msg_row.value, list)) else []
        )

    members = group.get("members", [])
    text = payload.text.strip()
    mentions = payload.mentions or parse_mentions(text, members)

    msg_id = f"gmsg-{uuid4().hex[:16]}"
    user_msg = {
        "id": msg_id,
        "group_id": group_id,
        "sender_type": "user",
        "sender_id": "human",
        "sender_name": "用户",
        "text": text,
        "mentions": mentions,
        "hop_count": 0,
        "created_at": _now_iso(),
    }
    history.append(user_msg)

    with store.session() as db:
        stmt = insert(WorkspacePreferenceRow).values(
            key=f"{MSG_PREF_PREFIX}{group_id}", value=history
        )
        stmt = stmt.on_conflict_do_update(index_elements=["key"], set_={"value": history})
        db.execute(stmt)

    if request.app.state.service is not None:
        request.app.state.service._server_event(
            "bot_group.message", agent_id=None, session_id=None, data=user_msg
        )

    # Parallel dispatch for all target agents
    targets_to_dispatch: list[str] = []
    if payload.target_agent_id:
        targets_to_dispatch = [payload.target_agent_id]
    elif mentions:
        targets_to_dispatch = list(mentions)
    elif members:
        targets_to_dispatch = [m.get("agent_id") for m in members if m.get("agent_id")]

    for aid in targets_to_dispatch:
        asyncio.create_task(_dispatch_group_turn(request, group_id, aid, text, 1, None))
    target_aid = targets_to_dispatch[0] if targets_to_dispatch else None

    return {
        "ok": True,
        "message": user_msg,
        "target_agent_id": target_aid,
        "history_count": len(history),
    }


@router.post("/api/v1/bot-groups/{group_id}/stop")
def stop_group_orchestration(group_id: str, request: Request) -> dict[str, Any]:
    _private(request)
    store = request.app.state.store
    from sqlalchemy.dialects.sqlite import insert
    from astrorder.models import WorkspacePreferenceRow

    with store.session() as db:
        row = db.get(WorkspacePreferenceRow, f"{PREF_PREFIX}{group_id}")
        if not row or not isinstance(row.value, dict):
            raise HTTPException(status_code=404, detail="群聊不存在")
        record = dict(row.value)
        record["active_hop"] = 0
        record["active_speaker_agent_id"] = None
        record["active_speakers"] = []
        record["stopped_at"] = _now_iso()

        stmt = insert(WorkspacePreferenceRow).values(key=f"{PREF_PREFIX}{group_id}", value=record)
        stmt = stmt.on_conflict_do_update(index_elements=["key"], set_={"value": record})
        db.execute(stmt)

    if request.app.state.service is not None:
        request.app.state.service._server_event(
            "bot_group.upsert", agent_id=None, session_id=None, data=record
        )

    return {"ok": True, "group": record}
