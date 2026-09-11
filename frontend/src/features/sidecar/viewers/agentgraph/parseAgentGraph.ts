import type { Message, Task } from '../../../../domain/types'
import { describeTool } from '../../../chat/toolPresentation'

export type StepKind = 'user_prompt' | 'thinking' | 'planning' | 'tool_call' | 'subagent' | 'completed' | 'failed'
export type StepStatus = 'pending' | 'running' | 'completed' | 'failed'

export interface AgentGraphNode {
  id: string
  title: string
  kind: StepKind
  status: StepStatus
  detail?: string
  toolName?: string
  toolPayload?: unknown
  timestamp?: string
  rawMessage?: Message
  rawTask?: Task
}

function toolStatus(message: Message): StepStatus {
  const status = message.tool?.status
  if (status === 'running' || status === 'pending') return 'running'
  if (status === 'failed' || status === 'error') return 'failed'
  return 'completed'
}

function taskStatus(task: Task): StepStatus {
  if (task.status === 'running' || task.status === 'waiting_approval') return 'running'
  if (task.status === 'completed') return 'completed'
  if (task.status === 'failed' || task.status === 'cancelled') return 'failed'
  return 'pending'
}

export function parseNodesFromSession(messages: Message[], tasks: Task[]): AgentGraphNode[] {
  const nodes: AgentGraphNode[] = []

  for (const msg of messages) {
    const text = (msg.text || '').trim()

    if (msg.role === 'user' && msg.kind === 'message' && text) {
      nodes.push({
        id: `user-${msg.id}`,
        title: '用户目标指令',
        kind: 'user_prompt',
        status: 'completed',
        detail: text,
        timestamp: msg.created_at,
        rawMessage: msg,
      })
      continue
    }

    if (msg.kind === 'thinking') {
      const desc = describeTool(msg)
      nodes.push({
        id: `think-${msg.id}`,
        title: desc.fullTitle,
        kind: 'thinking',
        status: desc.isRunning ? 'running' : desc.isFailed ? 'failed' : 'completed',
        detail: text,
        timestamp: msg.created_at,
        rawMessage: msg,
      })
      continue
    }

    if (msg.kind === 'tool' || msg.tool) {
      const desc = describeTool(msg)
      const name = String(msg.tool?.name || 'tool')
      nodes.push({
        id: `tool-${msg.id}`,
        title: desc.fullTitle,
        kind: 'tool_call',
        status: toolStatus(msg),
        detail: text || desc.target,
        toolName: name,
        toolPayload: msg.tool,
        timestamp: msg.created_at,
        rawMessage: msg,
      })
      continue
    }

    if (msg.role === 'assistant' && text) {
      nodes.push({
        id: `asst-${msg.id}`,
        title: '阶段产出与决策',
        kind: 'completed',
        status: 'completed',
        detail: text,
        timestamp: msg.created_at,
        rawMessage: msg,
      })
    }
  }

  for (const task of tasks) {
    const isSubagent = task.kind === 'subagent'
    nodes.push({
      id: `task-${task.id}`,
      title: isSubagent ? `子代理: ${task.title}` : `任务: ${task.title}`,
      kind: isSubagent ? 'subagent' : 'planning',
      status: taskStatus(task),
      detail: task.command || undefined,
      rawTask: task,
      timestamp: task.updated_at || task.created_at,
    })
  }

  if (nodes.length === 0) {
    nodes.push({
      id: 'init-1',
      title: 'Agent 状态机就绪',
      kind: 'planning',
      status: 'pending',
      detail: '等待用户指令或正在初始化运行时上下文...',
    })
  }

  return nodes
}
