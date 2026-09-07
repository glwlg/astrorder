import { IconChevronDown, IconChevronRight, IconMessageCircle } from '@tabler/icons-react'
import { Collapse, Stack, Text, UnstyledButton } from '@mantine/core'
import { useMemo, useState } from 'react'
import type { Agent, Session } from '../domain/types'
import { scopeKey } from '../domain/semantics'
import { StatusDot } from './Status'

export function SessionRail({
  sessions,
  agents,
  activeSessionKey,
  onSelect,
}: {
  sessions: Session[]
  agents: Record<string, Agent>
  activeSessionKey?: string
  onSelect: (session: Session) => void
}) {
  const [filter, setFilter] = useState('')
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({})
  const groups = useMemo(() => {
    const query = filter.trim().toLocaleLowerCase()
    const visible = sessions.filter((session) => {
      if (!query) return true
      return `${session.title} ${session.workspace}`.toLocaleLowerCase().includes(query)
    })
    const grouped = new Map<string, Session[]>()
    for (const session of visible) {
      const current = grouped.get(session.agent_id) || []
      current.push(session)
      grouped.set(session.agent_id, current)
    }
    return [...grouped.entries()]
  }, [filter, sessions])

  return (
    <Stack className="session-rail" gap="sm">
      <input
        className="session-search"
        aria-label="搜索会话"
        placeholder="搜索会话"
        value={filter}
        onChange={(event) => setFilter(event.currentTarget.value)}
      />
      {groups.length === 0 ? (
        <Text className="rail-empty" size="sm" c="dimmed">尚无会话</Text>
      ) : groups.map(([agentId, agentSessions]) => {
        const agent = agents[agentId]
        const isCollapsed = collapsed[agentId] === true
        return (
          <section className="session-group" key={agentId} aria-label={`${agent?.name || 'Agent'} 会话`}>
            <UnstyledButton
              className="session-group-title"
              onClick={() => setCollapsed((current) => ({ ...current, [agentId]: !isCollapsed }))}
              aria-expanded={!isCollapsed}
            >
              {isCollapsed ? <IconChevronRight size={15} /> : <IconChevronDown size={15} />}
              <span className="agent-glyph" aria-hidden="true">{agent?.kind === 'codex' ? 'C' : 'H'}</span>
              <span>{agent?.name || agentId}</span>
              <span className="session-count">{agentSessions.length}</span>
            </UnstyledButton>
            <Collapse expanded={!isCollapsed}>
              <Stack gap={4} mt={4}>
                {agentSessions.map((session) => {
                  const key = scopeKey(session.agent_id, session.id)
                  return (
                    <UnstyledButton
                      className={`session-row ${key === activeSessionKey ? 'is-active' : ''}`}
                      key={key}
                      onClick={() => onSelect(session)}
                      aria-current={key === activeSessionKey ? 'page' : undefined}
                    >
                      <StatusDot status={session.status} />
                      <span className="session-row-copy">
                        <span className="session-row-title">{session.title || '未命名会话'}</span>
                        <span className="session-row-meta">{session.workspace || '未提供工作区'}</span>
                      </span>
                      <IconMessageCircle className="session-row-icon" size={15} aria-hidden="true" />
                    </UnstyledButton>
                  )
                })}
              </Stack>
            </Collapse>
          </section>
        )
      })}
    </Stack>
  )
}
