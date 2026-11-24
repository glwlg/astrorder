import {
  hasLaterNativeInput,
  isAsyncQuestion,
  isAsyncQuestionAnswered,
  sameAsyncQuestion,
} from '../components/AsyncUserInputCard'
import { isUserInput } from '../components/UserInputCard'
import { displaySessionTitle, isHiddenRailSession, sessionActivityStatus } from '../components/sessionRailModel'
import {
  collectTurnArtifactReviews,
  isArtifactFeedbackPending,
  type ArtifactReviewItem,
} from './artifactReview'
import { scopeKey } from './semantics'
import type { Agent, Approval, Command, EventNotification, Message, Session } from './types'

export type ActionInboxKind = 'approval' | 'question' | 'error' | 'ready'

export interface ActionInboxItem {
  id: string
  kind: ActionInboxKind
  priority: number
  agentId: string
  sessionId: string
  sessionKey: string
  session?: Session
  agent?: Agent
  title: string
  summary: string
  detail?: string
  timestamp: string
  approval?: Approval
  questionMessage?: Message
  artifactReview?: ArtifactReviewItem
  artifactTurnEndAt?: string
  canApproveInline: boolean
  canQuickReply: boolean
}

export interface BuildActionInboxInput {
  sessions: Session[]
  agents: Record<string, Agent>
  approvals: Record<string, Approval>
  messages: Record<string, Record<string, Message>>
  commands?: Record<string, Command>
  notifications: Record<string, EventNotification>
  isUnread: (sessionKey: string) => boolean
}

function compactText(raw: string | null | undefined, maxLen = 140): string {
  const cleaned = (raw || '').replace(/\s+/g, ' ').trim()
  if (!cleaned) return ''
  return cleaned.length > maxLen ? `${cleaned.slice(0, maxLen)}…` : cleaned
}

function latestNoticeBySession(notifications: Record<string, EventNotification>): Map<string, EventNotification> {
  const map = new Map<string, EventNotification>()
  for (const notice of Object.values(notifications)) {
    if (notice.group_id || !notice.agent_id || !notice.session_id) continue
    const key = scopeKey(notice.agent_id, notice.session_id)
    const prev = map.get(key)
    if (!prev || (notice.cursor || 0) > (prev.cursor || 0)) {
      map.set(key, notice)
    }
  }
  return map
}

function sortedSessionMessages(bag: Record<string, Message> | undefined): Message[] {
  if (!bag) return []
  const all = Object.values(bag)
  const nativeAsync = all.filter((m) => m.delivery === 'async' && !m.id.includes(':live:'))
  return all
    .filter((m) => {
      if (m.id.includes(':live:') && m.delivery === 'async') {
        return !nativeAsync.some((native) => sameAsyncQuestion(m, native))
      }
      return true
    })
    .sort((a, b) => (a.created_at || '').localeCompare(b.created_at || ''))
}

function hasLaterAgentProgress(rows: Message[], index: number): boolean {
  const question = rows[index]
  if (!question) return false
  const questionTs = Date.parse(question.created_at || '')
  return rows.slice(index + 1).some((m) => {
    if (m.agent_id !== question.agent_id || m.session_id !== question.session_id) return false
    if (m.id === question.id) return false
    if (m.tool?.background === true) return false
    const msgTs = Date.parse(m.created_at || '')
    if (Number.isFinite(questionTs) && Number.isFinite(msgTs) && msgTs <= questionTs) return false
    if (m.kind === 'tool' || m.kind === 'thinking') return true
    return m.role === 'assistant' && m.kind === 'message' && m.delivery !== 'async' && Boolean(m.text.trim())
  })
}

export function buildActionInboxItems(input: BuildActionInboxInput): ActionInboxItem[] {
  const { sessions, agents, approvals, messages, commands = {}, notifications, isUnread } = input
  const sessionMap = new Map<string, Session>()
  for (const session of sessions) {
    sessionMap.set(scopeKey(session.agent_id, session.id), session)
  }
  const commandsBySession = new Map<string, Command[]>()
  for (const cmd of Object.values(commands)) {
    const key = scopeKey(cmd.agent_id, cmd.session_id)
    const list = commandsBySession.get(key)
    if (list) list.push(cmd)
    else commandsBySession.set(key, [cmd])
  }
  const notices = latestNoticeBySession(notifications)

  const items: ActionInboxItem[] = []
  const coveredApprovalSessions = new Set<string>()
  const coveredQuestionSessions = new Set<string>()
  const coveredAnySessions = new Set<string>()

  // 1. Pending approvals and native interactive questions (highest priority)
  for (const approval of Object.values(approvals)) {
    if (approval.state !== 'pending' || !approval.agent_id || !approval.session_id) continue
    const key = scopeKey(approval.agent_id, approval.session_id)
    const session = sessionMap.get(key)
    const agent = agents[approval.agent_id]
    const question = isUserInput(approval)
    coveredAnySessions.add(key)
    if (question) coveredQuestionSessions.add(key)
    else coveredApprovalSessions.add(key)

    const summary = compactText(approval.title || approval.detail) || (question ? '等待你的回答' : '请求执行授权')
    const detail = approval.detail && approval.detail !== approval.title ? approval.detail : undefined
    items.push({
      id: `${question ? 'question' : 'approval'}:${approval.id}`,
      kind: question ? 'question' : 'approval',
      priority: question ? 2 : 1,
      agentId: approval.agent_id,
      sessionId: approval.session_id,
      sessionKey: key,
      session,
      agent,
      title: session ? displaySessionTitle(session) : (question ? '等待回答' : '待审批请求'),
      summary,
      detail,
      timestamp: (typeof approval.data?.created_at === 'string' ? approval.data.created_at : '') || session?.updated_at || '',
      approval,
      canApproveInline: !question && agent?.capabilities.includes('approvals') === true,
      canQuickReply: false,
    })
  }

  // 2. Sessions in waiting_approval status without an explicit approval record
  for (const session of sessions) {
    if (session.status !== 'waiting_approval') continue
    const key = scopeKey(session.agent_id, session.id)
    if (coveredApprovalSessions.has(key) || coveredQuestionSessions.has(key)) continue
    coveredApprovalSessions.add(key)
    coveredAnySessions.add(key)
    const agent = agents[session.agent_id]
    const notice = notices.get(key)
    items.push({
      id: `waiting:${key}`,
      kind: 'approval',
      priority: 1,
      agentId: session.agent_id,
      sessionId: session.id,
      sessionKey: key,
      session,
      agent,
      title: displaySessionTitle(session),
      summary: compactText(notice?.message) || '会话处于等待确认状态，请查看并决定是否继续',
      timestamp: notice?.created_at || session.updated_at || '',
      canApproveInline: false,
      canQuickReply: false,
    })
  }

  // 3. Unanswered async questions in loaded session transcripts
  for (const [key, bag] of Object.entries(messages)) {
    if (coveredQuestionSessions.has(key)) continue
    const session = sessionMap.get(key)
    if (session?.status === 'running') continue
    const rows = sortedSessionMessages(bag)
    const sessionCommands = commandsBySession.get(key) || []
    for (let i = rows.length - 1; i >= 0; i -= 1) {
      const msg = rows[i]
      if (!isAsyncQuestion(msg)) continue
      if (
        hasLaterNativeInput(rows, i) ||
        isAsyncQuestionAnswered(msg, rows, sessionCommands) ||
        hasLaterAgentProgress(rows, i)
      ) {
        break
      }
      const agent = agents[msg.agent_id]
      coveredQuestionSessions.add(key)
      coveredAnySessions.add(key)
      const firstQuestion = msg.questions?.[0]?.title
      items.push({
        id: `async-question:${key}:${msg.id}`,
        kind: 'question',
        priority: 2,
        agentId: msg.agent_id,
        sessionId: msg.session_id,
        sessionKey: key,
        session,
        agent,
        title: session ? displaySessionTitle(session) : '会话提问',
        summary: compactText(firstQuestion || msg.text) || 'Agent 提出了问题等待你回复',
        timestamp: msg.created_at || session?.updated_at || '',
        questionMessage: msg,
        canApproveInline: false,
        canQuickReply: false,
      })
      break
    }
  }

  // 3b. Unresolved Antigravity artifact plans awaiting Proceed confirmation
  for (const [key, bag] of Object.entries(messages)) {
    if (coveredQuestionSessions.has(key) || coveredApprovalSessions.has(key)) continue
    const session = sessionMap.get(key)
    if (session?.status === 'running' || session?.control_state === 'readonly') continue
    const rows = sortedSessionMessages(bag)
    const sessionCommands = commandsBySession.get(key) || []
    const collected = collectTurnArtifactReviews(rows)
    for (let i = collected.all.length - 1; i >= 0; i -= 1) {
      const entry = collected.all[i]
      if (!entry.artifact.requestFeedback) continue
      if (!isArtifactFeedbackPending(entry, sessionCommands, false)) {
        break
      }
      const agent = agents[entry.artifact.agentId]
      coveredQuestionSessions.add(key)
      coveredAnySessions.add(key)
      items.push({
        id: `artifact-review:${key}:${entry.artifact.messageId}`,
        kind: 'question',
        priority: 2,
        agentId: entry.artifact.agentId,
        sessionId: entry.artifact.sessionId,
        sessionKey: key,
        session,
        agent,
        title: session ? displaySessionTitle(session) : entry.artifact.fileName,
        summary: compactText(entry.artifact.summary) || `${entry.artifact.fileName} 等待确认 Proceed`,
        timestamp: entry.turnEndAt || entry.artifact.createdAt || session?.updated_at || '',
        artifactReview: entry.artifact,
        artifactTurnEndAt: entry.turnEndAt,
        canApproveInline: false,
        canQuickReply: false,
      })
      break
    }
  }

  // 4. Genuine unread execution failures (task_failed notifications).
  // Note: raw `session.status === 'error'` is intentionally ignored here—matching
  // `sessionActivityStatus`—because idle-suspended or disconnected SSH Codex
  // runtimes persist `status: 'error'` in SQLite even when all turns succeeded.
  for (const session of sessions) {
    if (isHiddenRailSession(session)) continue
    const key = scopeKey(session.agent_id, session.id)
    if (coveredAnySessions.has(key)) continue
    const activity = sessionActivityStatus(session)
    if (activity === 'running' || activity === 'waiting_approval') continue
    const notice = notices.get(key)
    if (notice?.kind !== 'task_failed' || !isUnread(key)) continue
    coveredAnySessions.add(key)
    const agent = agents[session.agent_id]
    items.push({
      id: `error:${key}`,
      kind: 'error',
      priority: 3,
      agentId: session.agent_id,
      sessionId: session.id,
      sessionKey: key,
      session,
      agent,
      title: displaySessionTitle(session),
      summary: compactText(notice.message) || '本轮任务未能正常完成，请检查或重试',
      timestamp: notice.created_at || session.updated_at || '',
      canApproveInline: false,
      canQuickReply: agent?.status === 'ready' && session.control_state !== 'readonly',
    })
  }

  // 5. Unread completed sessions ready for review & next instruction
  for (const session of sessions) {
    if (isHiddenRailSession(session)) continue
    const key = scopeKey(session.agent_id, session.id)
    if (coveredAnySessions.has(key)) continue
    const activity = sessionActivityStatus(session)
    if (activity === 'running' || activity === 'waiting_approval') continue
    if (!isUnread(key)) continue

    const agent = agents[session.agent_id]
    const notice = notices.get(key)
    const rows = sortedSessionMessages(messages[key])
    const lastAssistant = [...rows].reverse().find((m) => m.role === 'assistant' && m.kind === 'message' && m.text.trim())
    const summary = compactText(notice?.message || lastAssistant?.text) || '已完成本轮执行，点击验收结果'

    items.push({
      id: `ready:${key}`,
      kind: 'ready',
      priority: 4,
      agentId: session.agent_id,
      sessionId: session.id,
      sessionKey: key,
      session,
      agent,
      title: displaySessionTitle(session),
      summary,
      timestamp: notice?.created_at || lastAssistant?.created_at || session.updated_at || '',
      canApproveInline: false,
      canQuickReply: agent?.status === 'ready' && session.control_state !== 'readonly',
    })
  }

  items.sort((a, b) => {
    if (a.priority !== b.priority) return a.priority - b.priority
    return (b.timestamp || '').localeCompare(a.timestamp || '')
  })

  return items
}

export function countUrgentInboxItems(items: ActionInboxItem[]): number {
  return items.filter((item) => item.kind === 'approval' || item.kind === 'question' || item.kind === 'error').length
}
