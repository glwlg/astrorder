import type { Agent, Session } from './types'

const canonicalKind = (kind: string) => (kind === 'antigravity-ssh' ? 'antigravity' : kind)

export type MessageForkMode = 'native' | 'context_branch' | 'none'

export function messageForkModeForAgent(kind?: string | null): MessageForkMode {
  if (!kind) return 'none'
  const normalized = canonicalKind(kind)
  if (normalized === 'hermes') return 'native'
  if (normalized === 'antigravity') return 'context_branch'
  return 'none'
}

export function sessionHandoffTargets(session: Session, agents: Record<string, Agent>): Agent[] {
  const source = agents[session.agent_id]
  const sourceKind = source ? canonicalKind(source.kind) : ''
  if (!source || !['codex', 'hermes', 'antigravity'].includes(sourceKind)) return []
  const environment = source.connection_id || null
  if ((session.connection_id || null) !== environment) return []
  return Object.values(agents).filter(agent =>
    ['codex', 'hermes', 'grok', 'antigravity'].includes(canonicalKind(agent.kind)) &&
    canonicalKind(agent.kind) !== sourceKind &&
    (agent.connection_id || null) === environment &&
    agent.status === 'ready',
  )
}
