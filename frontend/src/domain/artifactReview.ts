import type { Command, Message } from './types'

function extractToolArgsObject(tool: Message['tool'] | null | undefined): Record<string, unknown> {
  if (!tool || typeof tool !== 'object') return {}
  const record = tool as Record<string, unknown>
  if (record.arguments && typeof record.arguments === 'object' && !Array.isArray(record.arguments)) {
    const args = record.arguments as Record<string, unknown>
    if (Object.keys(args).length > 0) return args
  }
  const rawInput = record.input
  if (rawInput && typeof rawInput === 'object' && !Array.isArray(rawInput)) {
    return rawInput as Record<string, unknown>
  }
  if (typeof rawInput === 'string') {
    const trimmed = rawInput.trim()
    if (trimmed.startsWith('{') && trimmed.endsWith('}')) {
      try {
        const parsed = JSON.parse(trimmed)
        if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
          return parsed as Record<string, unknown>
        }
      } catch {}
    }
  }
  if (record.arguments && typeof record.arguments === 'object' && !Array.isArray(record.arguments)) {
    return record.arguments as Record<string, unknown>
  }
  return {}
}

function isAsyncQuestionMessage(message: Message): boolean {
  return message.role === 'assistant' && message.delivery === 'async' && Boolean(message.questions?.length)
}

export interface ArtifactReviewItem {
  messageId: string
  agentId: string
  sessionId: string
  path: string
  fileName: string
  summary: string
  userFacing: boolean
  requestFeedback: boolean
  toolName: string
  isToolRunning: boolean
  createdAt: string
  sourceIndex: number
}

export interface AnchoredArtifactReview {
  artifact: ArtifactReviewItem
  continued: boolean
  turnRunning: boolean
  turnEndAt: string
}

export interface TurnArtifactReviews {
  byMessageIndex: Map<number, AnchoredArtifactReview[]>
  byPackStart: Map<number, AnchoredArtifactReview[]>
  all: AnchoredArtifactReview[]
}

export function unquoteMaybeJsonString(raw: string): string {
  const trimmed = raw.trim()
  if (trimmed.length >= 2 && trimmed.startsWith('"') && trimmed.endsWith('"')) {
    try {
      const parsed = JSON.parse(trimmed)
      if (typeof parsed === 'string') return parsed.trim()
    } catch {}
  }
  return trimmed
}

function parseBoolFlag(raw: unknown): boolean {
  if (raw === true) return true
  if (typeof raw === 'string') return raw.trim().toLowerCase() === 'true'
  return false
}

function firstStringField(record: Record<string, unknown>, ...keys: string[]): string {
  for (const key of keys) {
    const val = record[key]
    if (typeof val === 'string' && val.trim() !== '') {
      return unquoteMaybeJsonString(val)
    }
  }
  return ''
}

export function parseArtifactMetadata(raw: unknown): Record<string, unknown> | null {
  if (!raw) return null
  if (typeof raw === 'object' && !Array.isArray(raw)) {
    return raw as Record<string, unknown>
  }
  if (typeof raw === 'string') {
    let current: unknown = raw.trim()
    for (let depth = 0; depth < 2 && typeof current === 'string'; depth += 1) {
      const trimmed = current.trim()
      if (!trimmed) return null
      try {
        current = JSON.parse(trimmed)
      } catch {
        return null
      }
    }
    if (current && typeof current === 'object' && !Array.isArray(current)) {
      return current as Record<string, unknown>
    }
  }
  return null
}

export function extractArtifactFromMessage(
  message: Message,
  index: number,
): ArtifactReviewItem | null {
  if (!message.tool && message.kind !== 'tool' && message.role !== 'tool') {
    return null
  }
  const status = String(message.tool?.status || '').toLowerCase()
  if (status === 'failed' || status === 'error') {
    return null
  }
  const args = extractToolArgsObject(message.tool)
  const rawMeta =
    args.ArtifactMetadata ??
    args.artifactMetadata ??
    args.artifact_metadata
  const meta = parseArtifactMetadata(rawMeta)
  if (!meta) return null

  const userFacing = parseBoolFlag(meta.UserFacing ?? meta.userFacing ?? meta.user_facing)
  const requestFeedback = parseBoolFlag(
    meta.RequestFeedback ?? meta.requestFeedback ?? meta.request_feedback,
  )
  if (!userFacing && !requestFeedback) return null

  const summary = firstStringField(meta, 'Summary', 'summary')
  const path = firstStringField(args, 'path', 'Path', 'TargetFile', 'AbsolutePath', 'File')
  if (!path && !summary) return null

  const fileName = path
    ? path.replace(/[\\/]+$/, '').split(/[\\/]/).pop() || path
    : '文档产物'

  return {
    messageId: message.id,
    agentId: message.agent_id,
    sessionId: message.session_id,
    path,
    fileName,
    summary,
    userFacing,
    requestFeedback,
    toolName: String(message.tool?.name || ''),
    isToolRunning: status === 'running' || status === 'in_progress',
    createdAt: message.completed_at || message.created_at || '',
    sourceIndex: index,
  }
}

function isRealUserMessage(message: Message): boolean {
  return (
    message.role === 'user' &&
    !message.id.startsWith('optimistic-') &&
    Boolean(message.text.trim() || message.attachments.length)
  )
}

function normalizedArtifactKey(item: ArtifactReviewItem): string {
  if (item.path) {
    return item.path.replace(/\\/g, '/').toLowerCase()
  }
  return `msg:${item.messageId}`
}

/**
 * Collects Antigravity user-facing artifacts per assistant turn, deduplicating
 * multiple writes/edits to the same artifact path within a single turn, and
 * anchors each turn's card(s) immediately after the turn's final assistant reply
 * (or after its containing activity pack when no reply follows).
 */
export function collectTurnArtifactReviews(
  messages: readonly Message[],
  packs?: Map<number, Message[]>,
  options?: { busy?: boolean },
): TurnArtifactReviews {
  const byMessageIndex = new Map<number, AnchoredArtifactReview[]>()
  const byPackStart = new Map<number, AnchoredArtifactReview[]>()
  const all: AnchoredArtifactReview[] = []
  const n = messages.length
  if (n === 0) return { byMessageIndex, byPackStart, all }

  // Map every activity message index -> start index of its owning pack.
  const indexToPackStart = new Map<number, number>()
  if (packs) {
    for (const [startIdx, pack] of packs.entries()) {
      for (let offset = 0; offset < pack.length; offset += 1) {
        indexToPackStart.set(startIdx + offset, startIdx)
      }
    }
  }

  // Precompute whether any real user message appears strictly after index i.
  const hasLaterUser = new Uint8Array(n)
  let seenRealUser = false
  for (let i = n - 1; i >= 0; i -= 1) {
    hasLaterUser[i] = seenRealUser ? 1 : 0
    if (isRealUserMessage(messages[i])) {
      seenRealUser = true
    }
  }

  let artifactsByKey = new Map<string, ArtifactReviewItem>()
  let lastAssistantMsgIdx: number | null = null
  let lastArtifactPackStart: number | null = null
  let turnEndIdx = -1
  let turnEndAt = ''

  const updateTurnEndAt = (candidate: string) => {
    if (!candidate) return
    if (!turnEndAt) {
      turnEndAt = candidate
      return
    }
    const candMs = Date.parse(candidate)
    const curMs = Date.parse(turnEndAt)
    if (Number.isFinite(candMs) && (!Number.isFinite(curMs) || candMs >= curMs)) {
      turnEndAt = candidate
    }
  }

  const flushTurn = () => {
    if (artifactsByKey.size === 0) {
      lastAssistantMsgIdx = null
      lastArtifactPackStart = null
      turnEndIdx = -1
      turnEndAt = ''
      return
    }

    const continued = turnEndIdx >= 0 ? Boolean(hasLaterUser[turnEndIdx]) : false
    const turnRunning = Boolean(options?.busy && !continued)
    const entries: AnchoredArtifactReview[] = Array.from(artifactsByKey.values()).map((artifact) => ({
      artifact,
      continued,
      turnRunning: turnRunning || artifact.isToolRunning,
      turnEndAt: turnEndAt || artifact.createdAt,
    }))

    if (lastAssistantMsgIdx !== null) {
      const existing = byMessageIndex.get(lastAssistantMsgIdx)
      if (existing) existing.push(...entries)
      else byMessageIndex.set(lastAssistantMsgIdx, [...entries])
    } else if (lastArtifactPackStart !== null) {
      const existing = byPackStart.get(lastArtifactPackStart)
      if (existing) existing.push(...entries)
      else byPackStart.set(lastArtifactPackStart, [...entries])
    } else {
      // Fallback when caller didn't pass `packs` (e.g. ActionInbox).
      const lastSourceIdx = entries[entries.length - 1].artifact.sourceIndex
      const existing = byMessageIndex.get(lastSourceIdx)
      if (existing) existing.push(...entries)
      else byMessageIndex.set(lastSourceIdx, [...entries])
    }

    all.push(...entries)
    artifactsByKey = new Map<string, ArtifactReviewItem>()
    lastAssistantMsgIdx = null
    lastArtifactPackStart = null
    turnEndIdx = -1
    turnEndAt = ''
  }

  for (let i = 0; i < n; i += 1) {
    const msg = messages[i]
    if (msg.role === 'user') {
      flushTurn()
      continue
    }

    turnEndIdx = i
    updateTurnEndAt(msg.completed_at || msg.created_at || '')

    const extracted = extractArtifactFromMessage(msg, i)
    if (extracted) {
      const key = normalizedArtifactKey(extracted)
      const prev = artifactsByKey.get(key)
      if (prev) {
        artifactsByKey.set(key, {
          ...extracted,
          summary: extracted.summary || prev.summary,
          userFacing: prev.userFacing || extracted.userFacing,
        })
      } else {
        artifactsByKey.set(key, extracted)
      }
      const packStart = indexToPackStart.get(i)
      if (packStart !== undefined) {
        lastArtifactPackStart = packStart
      }
      continue
    }

    if (
      msg.role === 'assistant' &&
      msg.kind === 'message' &&
      !isAsyncQuestionMessage(msg) &&
      (msg.text.trim() || msg.attachments.length > 0)
    ) {
      lastAssistantMsgIdx = i
    }
  }
  flushTurn()

  return { byMessageIndex, byPackStart, all }
}

export function isArtifactProceedCommand(
  command: Command,
  artifact: ArtifactReviewItem,
  turnEndAt?: string,
): boolean {
  if (
    command.agent_id !== artifact.agentId ||
    command.session_id !== artifact.sessionId ||
    command.action !== 'send' ||
    !['accepted', 'running', 'completed'].includes(command.state)
  ) {
    return false
  }
  if (
    command.target_id === artifact.messageId ||
    command.id === `artifact-proceed:${artifact.messageId}`
  ) {
    return true
  }
  const cmdTs = Date.parse(command.created_at || '')
  const refTs = Date.parse(turnEndAt || artifact.createdAt || '')
  if (Number.isFinite(cmdTs) && Number.isFinite(refTs)) {
    if (command.id.startsWith('artifact-proceed:') && cmdTs >= refTs - 5_000) {
      return true
    }
    if (cmdTs > refTs + 1_000) {
      return true
    }
  }
  return false
}

export function isArtifactFeedbackPending(
  entry: AnchoredArtifactReview,
  commands: readonly Command[] = [],
  sessionRunning = false,
): boolean {
  if (!entry.artifact.requestFeedback) return false
  if (entry.continued || entry.turnRunning || entry.artifact.isToolRunning || sessionRunning) {
    return false
  }
  if (commands.some((cmd) => isArtifactProceedCommand(cmd, entry.artifact, entry.turnEndAt))) {
    return false
  }
  return true
}
