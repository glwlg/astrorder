import type { Command } from '../../domain/types'

/** Keep the visible pause explanation aligned with App queue admission. */
export function queueBlockReason(commands: Command[], agentId: string, sessionId: string): string | null {
 const rows = commands.filter(row => row.agent_id === agentId && row.session_id === sessionId)
 if (rows.some(row => row.state === 'received')) return '自动发送已暂停：上一条指令仍在提交，等待投递回执。'
 if (rows.some(row => row.state === 'unknown')) return '自动发送已暂停：上一条消息的发送结果尚未确认，请先核对会话记录；待发内容已保留。'
 if (rows.some(row => row.state === 'running' || row.state === 'accepted' && (row.action === 'send' || row.action === 'enqueue'))) return '自动发送已暂停：上一条消息的完成回执尚未收到，待发内容已保留。'
 return null
}
