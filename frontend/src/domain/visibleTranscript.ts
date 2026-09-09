import type { Message } from './types'

/** Native pages own their covered time range; live projections only extend it. */
export function visibleTranscript(stored: Message[], loaded: Message[], baseline: Set<string>): Message[] {
  const ids = new Set(loaded.map(m => m.id))
  const newest = Math.max(...loaded.map(m => Date.parse(m.created_at)).filter(Number.isFinite))
  return stored.filter(m => {
    if (ids.has(m.id)) return true
    // Legacy post_llm callbacks repeated the user input with a new random ID.
    // Uncorrelated echoes are not a second native user turn.
    if (m.role === 'user' && m.id.startsWith('hermes-user-') && !m.command_id) return false
    return !baseline.has(m.id) && (!Number.isFinite(newest) || Date.parse(m.created_at) >= newest)
  }).sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at))
}
