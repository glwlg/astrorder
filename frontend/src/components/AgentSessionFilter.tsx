import type { Agent, Session } from '../domain/types'
import { agentLabel } from './NewSessionDialog'
export function matchesAgent(session: Session, agents: Record<string, Agent>, filter: string): boolean {
  if (filter === 'all') return true
  if (filter === 'hermes' || filter === 'codex') return agents[session.agent_id]?.kind === filter
  return session.agent_id === filter
}
export function AgentSessionFilter({ agents, value, onChange }: { agents: Record<string, Agent>; value: string; onChange: (value: string) => void }) {
  return <select className="session-search" style={{ minWidth: 0, flex: 1, maxWidth: '100%' }} aria-label="按 Agent 筛选" value={value} onChange={event => onChange(event.currentTarget.value)}>
    <option value="all">全部 Agent</option><option value="hermes">全部 Hermes</option><option value="codex">全部 Codex</option>
    {Object.values(agents).map(agent => <option key={agent.id} value={agent.id}>{agentLabel(agent)}</option>)}
  </select>
}
