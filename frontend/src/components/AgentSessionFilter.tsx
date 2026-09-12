import { IconSparkles } from '@tabler/icons-react'
import { AgentBrandIcon } from './AgentBrandIcon'
import type { Agent, AgentKind, Session } from '../domain/types'

function parseFilter(filter: string): { connection: string; kind: AgentKind | 'all' } {
  if (filter === 'all') return { connection: 'all', kind: 'all' }
  if (filter === 'hermes' || filter === 'codex') return { connection: 'all', kind: filter }
  const match = /^connection:(.*)\|kind:(all|hermes|codex)$/.exec(filter)
  return match ? { connection: match[1], kind: match[2] as AgentKind | 'all' } : { connection: 'agent:' + filter, kind: 'all' }
}

export function connectionName(agent: Agent): string {
  if (!agent.connection_id) return '本机'
  return agent.name.trim().replace(/\s*·\s*(?:Codex|Hermes)$/i, '') || agent.connection_id
}

function filterValue(connection: string, kind: AgentKind | 'all'): string {
  return connection === 'all' && kind === 'all' ? 'all' : 'connection:' + connection + '|kind:' + kind
}

export function matchesAgent(session: Session, agents: Record<string, Agent>, filter: string): boolean {
  const selected = parseFilter(filter)
  if (selected.connection.startsWith('agent:')) return session.agent_id === selected.connection.slice(6)
  const agent = agents[session.agent_id]
  const connection = session.connection_id || agent?.connection_id || 'local'
  return (selected.connection === 'all' || selected.connection === connection)
    && (selected.kind === 'all' || agent?.kind === selected.kind)
}

export function AgentSessionFilter({ agents, value, onChange }: { agents: Record<string, Agent>; value: string; onChange: (value: string) => void }) {
  const selected = parseFilter(value)
  const connections = new Map<string, string>()
  Object.values(agents).forEach(agent => {
    const id = agent.connection_id || 'local'
    if (!connections.has(id)) connections.set(id, connectionName(agent))
  })
  const kinds = [
    ['all', '全部 Agent'],
    ['hermes', 'Hermes'],
    ['codex', 'Codex'],
  ] as const
  return <div className="agent-session-filter">
    <select aria-label="按连接筛选" value={selected.connection.startsWith('agent:') ? 'all' : selected.connection} onChange={event => onChange(filterValue(event.currentTarget.value, selected.kind))}>
      <option value="all">全部连接</option>
      {[...connections].map(([id, name]) => <option key={id} value={id}>{name}</option>)}
    </select>
    <div className="agent-kind-filters" role="group" aria-label="按 Agent 类型筛选">
      {kinds.map(([kind, label]) => <button key={kind} type="button" className={selected.kind === kind ? 'is-active' : ''} aria-label={label} aria-pressed={selected.kind === kind} title={label} onClick={() => onChange(filterValue(selected.connection.startsWith('agent:') ? 'all' : selected.connection, kind))}>{kind === 'all' ? <IconSparkles size={16} /> : <AgentBrandIcon kind={kind} size={16} />}</button>)}
    </div>
  </div>
}
