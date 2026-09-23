from __future__ import annotations

import json
import logging
import urllib.request
import urllib.error
from typing import Any
from sqlalchemy import select
from sqlalchemy.dialects.sqlite import insert
from astrorder.models import WorkspacePreferenceRow

logger = logging.getLogger(__name__)

TYPESAFE_API_URL = "https://api.typesafe.ai/v1/systemone"
DEFAULT_JEV_MODEL = "jev-latest"

def get_jev_key(store) -> str | None:
    """从统一数据库或环境变量获取配置的 Jev API Key。"""
    try:
        with store.session() as db:
            row = db.get(WorkspacePreferenceRow, "services:jev_api_key")
            if row and isinstance(row.value, str) and row.value.strip():
                return row.value.strip()
    except Exception:
        pass
    import os
    env_key = os.environ.get("JEV_API_KEY") or os.environ.get("TYPESAFE_API_KEY")
    return env_key.strip() if env_key and env_key.strip() else None

def set_jev_key(store, key: str | None) -> None:
    clean = (key or "").strip()
    with store.session() as db:
        if clean:
            stmt = insert(WorkspacePreferenceRow).values(key="services:jev_api_key", value=clean)
            stmt = stmt.on_conflict_do_update(index_elements=["key"], set_={"value": clean})
            db.execute(stmt)
        else:
            row = db.get(WorkspacePreferenceRow, "services:jev_api_key")
            if row:
                db.delete(row)

def invoke_jev(
    state: Any,
    questions: dict[str, Any],
    api_key: str | None = None,
    *,
    store = None,
    model: str = DEFAULT_JEV_MODEL,
    timeout: float = 15.0,
) -> dict[str, Any]:
    """向 TypeSafe AI Jev 决策模型发起判断请求。"""
    resolved_key = api_key or (get_jev_key(store) if store else None)
    if not resolved_key:
        raise ValueError("Jev API Key 未配置，请在设置中配置 Jev API Key。")

    headers = {
        "Authorization": f"Bearer {resolved_key}",
        "Content-Type": "application/json",
    }
    payload = {
        "model": model,
        "state": state,
        "questions": questions,
    }
    req = urllib.request.Request(TYPESAFE_API_URL, data=json.dumps(payload).encode("utf-8"), headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            data = json.loads(resp.read().decode("utf-8"))
            return {"ok": True, **data}
    except urllib.error.HTTPError as e:
        err_msg = e.read().decode("utf-8", errors="replace")
        logger.error(f"Jev API HTTP error {e.code}: {err_msg}")
        raise RuntimeError(f"Jev API 调用失败 ({e.code}): {err_msg}") from e
    except Exception as e:
        logger.error(f"Jev API error: {e}")
        raise RuntimeError(f"Jev API 网络异常: {e}") from e

def evaluate_blackboard_component(content: str, store = None, api_key: str | None = None) -> dict[str, Any]:
    """使用 Jev 评估输入文本或结构，路由推荐最契合的 json-render 黑板组件。"""
    criteria = {
        "StepTimeline": "Involves sequential pipeline stages, deployment phases, CI/CD steps, or chronological milestones.",
        "MetricGrid": "Involves numerical metrics, KPI counters, CPU/memory stats, QPS, latency, rates or counts.",
        "DataTable": "Involves tabular data, list of records, database rows, or multi-attribute entities.",
        "Checklist": "Involves todo tasks, verification items, requirements acceptance, or inspect checks.",
        "TestReport": "Involves test suite execution, unit test pass/fail results, assertions, or test run coverage.",
        "CveSecurityReport": "Involves security vulnerabilities, CVE scans, audits, advisory findings, or risk alerts.",
        "ApiEndpointsCard": "Involves REST API routes, HTTP methods, endpoints list, or service contracts.",
        "ResourceUsageBar": "Involves single or dual resource capacity, quota limits, progress percentage, or storage capacity.",
        "StatusCard": "Involves simple high-level state, health indicator, summary announcement, or alert status."
    }
    questions = {
        "recommended_component": {
            "type": "choice",
            "instructions": "Determine the single most appropriate dashboard UI component to render this status or payload visually.",
            "criteria": criteria,
        }
    }
    resp = invoke_jev(state=content[:4000], questions=questions, api_key=api_key, store=store)
    ans = (resp.get("answers") or {}).get("recommended_component") or {}
    choice = ans.get("choice") or "StatusCard"
    confidence = ans.get("confidence", 0.0)
    return {
        "ok": True,
        "component": choice,
        "confidence": confidence,
        "probabilities": ans.get("probabilities", {}),
        "usage": resp.get("usage", {}),
    }

def filter_terminal_output(command: str, output: str, store = None, api_key: str | None = None) -> dict[str, Any]:
    """使用 Jev 对长终端输出进行语义降噪过滤，提取错误与关键结论行。"""
    if not output or len(output) < 200:
        return {"ok": True, "filtered": output, "noise_pruned": False}

    lines = output.splitlines()
    state_sample = f"Command: {command}\nLines: {len(lines)}\nSample: {output[:1500]}"
    questions = {
        "log_nature": {
            "type": "choice",
            "instructions": "Classify the overall outcome of this terminal command.",
            "criteria": {
                "BuildTestFailure": "Compilation error, failed assertions, stack traces, exit non-zero.",
                "Success": "All tasks succeeded, exit zero, normal operational report.",
                "VerboseScan": "Long search, grep, find, list or telemetry dump without critical failure."
            }
        }
    }
    try:
        decision = invoke_jev(state=state_sample, questions=questions, api_key=api_key, store=store)
        nature = (decision.get("answers", {}).get("log_nature") or {}).get("choice", "Success")
    except Exception:
        nature = "Unknown"
        decision = {"fallback": True}

    key_lines = []
    error_patterns = ("error", "failed", "failure", "exception", "traceback", "fatal", "warn", "assert", "panic")
    for idx, line in enumerate(lines):
        lower = line.lower()
        if any(pat in lower for pat in error_patterns) or idx < 5 or idx >= len(lines) - 10:
            key_lines.append(line)
        elif "===" in line or "---" in line or "passed in" in lower or "failed in" in lower:
            key_lines.append(line)

    pruned_text = "\n".join(key_lines)
    if len(pruned_text) < len(output) * 0.75:
        summary_header = f"[Jev 降噪过滤: 判定为 {nature}, 已剔除冗余日志 {len(lines) - len(key_lines)} 行]\n"
        final_filtered = summary_header + pruned_text
        noise_pruned = True
    else:
        final_filtered = output
        noise_pruned = False

    return {
        "ok": True,
        "nature": nature,
        "filtered": final_filtered,
        "original_lines": len(lines),
        "filtered_lines": len(key_lines),
        "noise_pruned": noise_pruned,
        "jev_decision": decision,
    }

def curate_handoff_summary(messages: list[dict[str, Any]], source_info: dict[str, Any], store = None, api_key: str | None = None) -> dict[str, Any]:
    """使用 Jev 快速提炼会话交接包要素，产生轻量且聚焦的 Handoff 卡片。"""
    user_msgs = [m.get("text", "") for m in messages if m.get("role") == "user" and m.get("text")]
    recent_replies = [m.get("text", "") for m in messages if m.get("role") == "assistant" and m.get("text")]
    u_txt = " ".join(user_msgs[-3:])
    r_txt = " ".join(recent_replies[-2:])[:1500]
    context_text = f"Goal/User intent: {u_txt}\nRecent progress: {r_txt}"
    questions = {
        "handoff_phase": {
            "type": "choice",
            "instructions": "What is the primary phase of the current task being handed off?",
            "criteria": {
                "DebuggingFixing": "Investigating a bug, troubleshooting tests, or repairing code failure.",
                "FeatureBuilding": "Developing a new capability, implementing UI/UX, or adding endpoints.",
                "RefactoringMaintenance": "Cleaning architecture, upgrading dependencies, or documentation.",
                "PendingVerification": "Implementation is done, awaiting test run or manual approval."
            }
        },
        "blocker_severity": {
            "type": "choice",
            "instructions": "Is the task currently stuck on a blocker?",
            "criteria": {
                "NoBlocker": "Progressing smoothly or completed phase.",
                "TechnicalBlocker": "Blocked by environment, API, compilation or dependency issue."
            }
        }
    }
    try:
        decision = invoke_jev(state=context_text, questions=questions, api_key=api_key, store=store)
        phase = (decision.get("answers", {}).get("handoff_phase") or {}).get("choice", "FeatureBuilding")
        blocker = (decision.get("answers", {}).get("blocker_severity") or {}).get("choice", "NoBlocker")
    except Exception:
        phase = "TaskExecution"
        blocker = "Unknown"
        decision = {"fallback": True}

    u_target = user_msgs[0][:120] if user_msgs else "继续执行会话未竟任务"
    r_target = recent_replies[-1][:220] if recent_replies else "已建立基础上下文"
    summary_lines = [
        f"### Jev 提炼交接要点 (阶段: {phase} | 状态: {blocker})",
        f"- **原始目标**: {u_target}",
        f"- **最近进展**: {r_target}",
        "- **接手要求**: 请直接在工作区承接上述进度，核查相关文件并推进下一阶段。",
    ]
    return {"ok": True, "summary": "\n".join(summary_lines), "phase": phase, "blocker": blocker, "jev_decision": decision}


def evaluate_session_swipe_worthiness(session_info: dict[str, Any], recent_messages: list[dict[str, Any]], store = None, api_key: str | None = None) -> dict[str, Any]:
    title = session_info.get("title") or "task"
    status = session_info.get("status") or "idle"
    
    if status in {"running", "waiting_approval"}:
        return {"ok": True, "worthy": True, "category": "ActiveRunning", "confidence": 1.0}
    
    user_msgs = [m.get("text", "") for m in recent_messages if m.get("role") == "user" and m.get("text")]
    assistant_msgs = [m.get("text", "") for m in recent_messages if m.get("role") == "assistant" and m.get("text")]
    
    last_user = user_msgs[-1][:300] if user_msgs else "none"
    last_assistant = assistant_msgs[-1][:500] if assistant_msgs else "none"
    
    state_sample = f"""Session Title: {title}
Status: {status}
Last User Command: {last_user}
Last Assistant Output: {last_assistant}"""
    
    questions = {
        "swipe_action": {
            "type": "choice",
            "instructions": "Determine if this completed or idle session requires immediate human inspection or follow-up, or if it should be archived away from quick swipe deck.",
            "criteria": {
                "RetainInSwipe": "Recent active task that just produced output, has unfinished sub-steps, or is awaiting user inspection/next instruction.",
                "ArchiveFromSwipe": "Finished task, casual query completely closed, historical investigation done, or idle conversation with nothing pending."
            }
        }
    }
    
    try:
        decision = invoke_jev(state=state_sample, questions=questions, api_key=api_key, store=store)
        choice = (decision.get("answers", {}).get("swipe_action") or {}).get("choice", "ArchiveFromSwipe")
        confidence = (decision.get("answers", {}).get("swipe_action") or {}).get("confidence", 0.0)
        worthy = (choice == "RetainInSwipe")
    except Exception:
        worthy = False
        choice = "FallbackArchive"
        confidence = 0.0
        decision = {"fallback": True}
        
    return {
        "ok": True,
        "worthy": worthy,
        "category": choice,
        "confidence": confidence,
        "jev_decision": decision,
    }
