import { useEffect } from 'react'
import { Menu } from '@mantine/core'
import { IconChevronDown, IconSparkles } from '@tabler/icons-react'
import { AgentBrandIcon, agentKindLabel } from './AgentBrandIcon'
import type { Agent, AgentKind, Session } from '../domain/types'

function parseFilter(filter: string): { connection: string; kind: AgentKind | 'all' } {
  if (filter === 'all') return { connection: 'all', kind: 'all' }
  if (filter === 'hermes' || filter === 'codex') return { connection: 'all', kind: filter }
  const match = /^connection:(.*)\|kind:([^|]+)$/.exec(filter)
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

export function AgentSessionFilter({ agents, value, onChange }: { agents: Record<string, Agent>; sessions?: Session[]; value: string; onChange: (value: string) => void }) {
  const selected = parseFilter(value)
  const visibleAgents = Object.values(agents).filter(agent => agent.status !== 'disconnected')
  const connections = new Map<string, string>()
  visibleAgents.forEach(agent => {
    const id = agent.connection_id || 'local'
    if (!connections.has(id)) connections.set(id, connectionName(agent))
  })
  const requestedConnection = selected.connection.startsWith('agent:') ? 'all' : selected.connection
  const connection = requestedConnection === 'all' || connections.has(requestedConnection) ? requestedConnection : 'all'
  const kindsFor = (id: string) => [...new Set(visibleAgents.filter(agent => id === 'all' || (agent.connection_id || 'local') === id).map(agent => agent.kind))].sort()
  const kinds = kindsFor(connection)
  const kind = selected.kind === 'all' || kinds.includes(selected.kind) ? selected.kind : 'all'
  const normalizedValue = filterValue(connection, kind)
  const stale = Object.keys(agents).length > 0 && !selected.connection.startsWith('agent:') && (connection !== selected.connection || kind !== selected.kind)
  useEffect(() => {
    if (stale) onChange(normalizedValue)
  }, [stale, normalizedValue, onChange])
  const label = kind === 'all' ? '全部 Agent' : agentKindLabel(kind)
  return <div className="agent-session-filter">
    <select className="agent-connection-select" aria-label="按连接筛选" value={connection} onChange={event => {
      const next = event.currentTarget.value
      onChange(filterValue(next, kindsFor(next).includes(kind) ? kind : 'all'))
    }}>
      <option value="all">全部连接</option>
      {[...connections].map(([id, name]) => <option key={id} value={id}>{name}</option>)}
    </select>
    <Menu position="bottom-end" width={200} withinPortal>
      <Menu.Target>
        <button type="button" className="agent-kind-select" aria-label="按 Agent 类型筛选" title={label}>
          {kind === 'all' ? <IconSparkles size={18} /> : <AgentBrandIcon kind={kind} size={18} />}
          <IconChevronDown size={12} />
        </button>
      </Menu.Target>
      <Menu.Dropdown className="agent-kind-dropdown">
        <Menu.Item leftSection={<IconSparkles size={18} />} aria-current={kind === 'all' ? 'true' : undefined} onClick={() => onChange(filterValue(connection, 'all'))}>全部 Agent</Menu.Item>
        {kinds.map(item => <Menu.Item key={item} leftSection={<AgentBrandIcon kind={item} size={18} />} aria-current={kind === item ? 'true' : undefined} onClick={() => onChange(filterValue(connection, item))}>{agentKindLabel(item)}</Menu.Item>)}
      </Menu.Dropdown>
    </Menu>
  </div>
}
