"""Native command operations shared by local and SSH transports."""
from typing import Any


def interrupt_session(rpc, session_id: str) -> tuple[str, str | None]:
    # Do not resume here: resume can restart an interrupted recovery turn.
    response = rpc('session.active_list', {})
    if not isinstance(response, dict) or response.get('error') or not isinstance(response.get('result'), dict):
        return 'failed', '无法确认原生运行会话；未发送停止命令。'
    sessions = response['result'].get('sessions')
    if not isinstance(sessions, list):
        return 'failed', '原生运行会话列表无效；未发送停止命令。'
    matches = [row for row in sessions if isinstance(row, dict) and row.get('session_key') == session_id]
    if not matches:
        return 'failed', '原生运行时未找到该会话的活动句柄；未停止其他会话。'
    if len(matches) != 1 or not matches[0].get('id'):
        return 'failed', '原生会话句柄不唯一；未发送停止命令。'
    outcome: Any = rpc('session.interrupt', {'session_id': matches[0]['id']})
    if not isinstance(outcome, dict):
        return 'unknown', '原生运行时尚未确认停止结果；不会自动重试。'
    if outcome.get('error'):
        return 'failed', '原生运行时拒绝停止操作。'
    result = outcome.get('result')
    if not isinstance(result, dict) or result.get('status') != 'interrupted':
        return 'unknown', '原生运行时尚未确认停止结果；不会自动重试。'
    return 'accepted', None
