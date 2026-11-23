import type { SessionCheckpoint } from '../api/client'
import type { Message } from '../domain/types'

function parseTimeMs(iso: string | undefined | null): number | null {
  if (!iso) return null
  const ms = Date.parse(iso)
  return Number.isFinite(ms) ? ms : null
}

/**
 * Pairs per-turn checkpoints with the final visible assistant reply of the
 * corresponding turn so transcripts can render inline checkpoint pills.
 */
export function mapCheckpointsToMessages(
  messages: Message[],
  checkpoints: SessionCheckpoint[],
): Map<string, SessionCheckpoint> {
  const result = new Map<string, SessionCheckpoint>()
  if (!messages.length || !checkpoints.length) return result

  // Only surface meaningful end-of-turn / manual / backup checkpoints inline.
  // Skip pure baseline snapshots (`auto_before`) with zero file changes.
  const candidates = checkpoints.filter(
    (cp) => cp.kind !== 'auto_before' || cp.files_changed > 0,
  )
  if (!candidates.length) return result

  // Identify final visible assistant message for each conversational turn
  // (delimited by user messages).
  type TurnAnchor = {
    assistantMessage: Message
    userCommandId: string | null
    assistantTs: number | null
  }
  const anchors: TurnAnchor[] = []
  let currentUserCommandId: string | null = null
  let lastAssistantInTurn: Message | null = null

  const flushTurn = () => {
    if (lastAssistantInTurn) {
      anchors.push({
        assistantMessage: lastAssistantInTurn,
        userCommandId: currentUserCommandId,
        assistantTs: parseTimeMs(lastAssistantInTurn.completed_at || lastAssistantInTurn.created_at),
      })
      lastAssistantInTurn = null
    }
  }

  for (const msg of messages) {
    if (msg.role === 'user') {
      flushTurn()
      currentUserCommandId = msg.command_id || null
      continue
    }
    if (msg.role === 'assistant' && msg.kind === 'message') {
      lastAssistantInTurn = msg
    }
  }
  flushTurn()

  if (!anchors.length) return result

  // Process newest checkpoints first so each turn displays its latest snapshot.
  const sortedCandidates = [...candidates].sort((a, b) => b.turn_number - a.turn_number)

  // Pass 1: exact command_id match.
  const unmatched: SessionCheckpoint[] = []
  for (const cp of sortedCandidates) {
    if (cp.command_id) {
      const anchor = anchors.find(
        (a) => a.userCommandId === cp.command_id && !result.has(a.assistantMessage.id),
      )
      if (anchor) {
        result.set(anchor.assistantMessage.id, cp)
        continue
      }
    }
    unmatched.push(cp)
  }

  // Pass 2: closest preceding assistant reply within timestamp tolerance.
  for (const cp of unmatched) {
    const cpTs = parseTimeMs(cp.created_at)
    if (cpTs === null) continue
    let best: TurnAnchor | null = null
    let bestDelta = Infinity
    for (const anchor of anchors) {
      if (result.has(anchor.assistantMessage.id) || anchor.assistantTs === null) continue
      // Checkpoint is normally captured right as or right after assistant reply finishes.
      // Allow up to 15s clock skew before reply and up to 10m after reply.
      const delta = cpTs - anchor.assistantTs
      if (delta >= -15_000 && delta <= 10 * 60_000 && Math.abs(delta) < bestDelta) {
        best = anchor
        bestDelta = Math.abs(delta)
      }
    }
    if (best) {
      result.set(best.assistantMessage.id, cp)
    }
  }

  return result
}
