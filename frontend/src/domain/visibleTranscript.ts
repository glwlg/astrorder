import type { Message } from './types'
import { confirmsOptimisticUser } from './semantics'

/** Native pages own their covered time range; live projections only extend it. */
export function visibleTranscript(stored: Message[], loaded: Message[], baseline: Set<string>): Message[] {
  const ids = new Set(loaded.map(m => m.id))
  const loadedUserMessages = loaded.filter(m => m.role === 'user')
  const newest = Math.max(...loaded.map(m => Date.parse(m.created_at)).filter(Number.isFinite))
  const live = stored.filter(m => {
    if (ids.has(m.id)) return false
    // Replies and newer history timestamps cannot acknowledge a user's submitted content.
    if (m.id.startsWith('optimistic-') && m.role === 'user') {
      return !loadedUserMessages.some(native => confirmsOptimisticUser(m, native))
    }
    // Legacy post_llm callbacks repeated the user input with a new random ID.
    // Uncorrelated echoes are not a second native user turn.
    if (m.role === 'user' && m.id.startsWith('hermes-user-') && !m.command_id) return false
    return !baseline.has(m.id) && (!Number.isFinite(newest) || Date.parse(m.created_at) >= newest)
  })
  // Native item order is authoritative; tool completion times are not positions.
  const current = new Map(stored.map(m => [m.id, m]))
  const result = loaded.map(m => current.get(m.id) || m)
  for (const message of live) {
    if (message.turn_id) {
      const lastInTurn = result.findLastIndex(m => m.turn_id === message.turn_id)
      if (lastInTurn >= 0) {
        result.splice(lastInTurn + 1, 0, message)
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
