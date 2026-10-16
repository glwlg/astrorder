import type { Message } from './types'
import { confirmsOptimisticUser } from './semantics'

/** Native pages own their covered time range; live projections only extend it. */
export function visibleTranscript(stored: Message[], loaded: Message[], baseline: Set<string>): Message[] {
  const ids = new Set(loaded.map(m => m.id))
  const loadedUserMessages = loaded.filter(m => m.role === 'user')
  const newest = Math.max(...loaded.map(m => Date.parse(m.created_at)).filter(Number.isFinite))
  const turns = new Set(loaded.map(m => m.turn_id).filter(Boolean))
  const liveUserTurns = new Set(stored.filter(m => m.role === 'user' && !baseline.has(m.id) && Date.parse(m.created_at) >= newest).map(m => m.turn_id).filter(Boolean))
  const live = stored.filter(m => {
    if (ids.has(m.id)) return false
    // Replies and newer history timestamps cannot acknowledge a user's submitted content.
    if (m.id.startsWith('optimistic-') && m.role === 'user') {
      return !loadedUserMessages.some(native => confirmsOptimisticUser(m, native))
    }
    // Legacy post_llm callbacks repeated the user input with a new random ID.
    // Uncorrelated echoes are not a second native user turn.
    if (m.role === 'user' && m.id.startsWith('hermes-user-') && !m.command_id) return false
    if (baseline.has(m.id)) return false
    // Receive time cannot establish that an unknown Codex turn is newer.
    // Native history refresh supplies its position; a live user echo anchors a new turn.
    if (m.turn_id && turns.size) return turns.has(m.turn_id) || liveUserTurns.has(m.turn_id)
    return !Number.isFinite(newest) || Date.parse(m.created_at) >= newest
  })
  // Native item order is authoritative; tool completion times are not positions.
  const current = new Map(stored.map(m => [m.id, m]))
  const result = loaded.map(m => {
    const latest = current.get(m.id) || m
    return m.timestamp_source === 'native' ? { ...latest, created_at: m.created_at, timestamp_source: m.timestamp_source, completed_at: m.completed_at } : latest
  })
  for (const message of live) {
    if (message.turn_id) {
      const lastInTurn = result.findLastIndex(m => m.turn_id === message.turn_id)
      if (lastInTurn >= 0) {
        const nextInTurn = message.timestamp_source === 'native'
          ? result.findIndex(m => m.turn_id === message.turn_id && m.timestamp_source === 'native' && Date.parse(m.created_at) > Date.parse(message.created_at))
          : -1
        result.splice(nextInTurn >= 0 ? nextInTurn : lastInTurn + 1, 0, message)
        continue
      }
    }
    if (message.id.startsWith('optimistic-') && message.role === 'user') {
      const next = result.findIndex(m => Date.parse(m.created_at) > Date.parse(message.created_at))
      if (next >= 0) {
        result.splice(next, 0, message)
        continue
      }
    }
    result.push(message)
  }
  return result
}
