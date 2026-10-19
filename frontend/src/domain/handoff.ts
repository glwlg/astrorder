import type { Agent, Session } from './types'

export function sessionHandoffTargets(session: Session, agents: Record<string, Agent>): Agent[] {
  const source = agents[session.agent_id]
  if (!source || !['codex', 'hermes'].includes(source.kind)) return []
  const environment = source.connection_id || null
  if ((session.connection_id || null) !== environment) return []
  return Object.values(agents).filter(agent =>
    ['codex', 'hermes', 'grok'].includes(agent.kind) &&
    agent.kind !== source.kind &&
    (agent.connection_id || null) === environment &&
    agent.status === 'ready',
  )
}
