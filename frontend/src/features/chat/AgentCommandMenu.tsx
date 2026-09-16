import { Group, Paper, Stack, Text, UnstyledButton } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { api } from '../../api/client'
import { AgentBrandIcon } from '../../components/AgentBrandIcon'
import type { Agent, AgentCommand, AgentMention, Session } from '../../domain/types'

export function useAgentCommands(session: Session | null | undefined, enabled: boolean) {
  return useQuery({
    queryKey: ['astrorder', 'agent-commands', session?.agent_id, session?.id],
    queryFn: () => (session ? api.getAgentCommands(session.id, session.agent_id) : Promise.resolve({ items: [] })),
    enabled: Boolean(enabled && session),
    staleTime: 30_000,
    retry: false,
  })
}

export function useAgentMentions(session: Session | null | undefined, enabled: boolean) {
  return useQuery({
    queryKey: ['astrorder', 'agent-mentions', session?.agent_id, session?.id],
    queryFn: () => (session ? api.getAgentMentions(session.id, session.agent_id) : Promise.resolve({ items: [] })),
    enabled: Boolean(enabled && session),
    staleTime: 30_000,
    retry: false,
  })
}

export function useFileMentions(session: Session | null | undefined, query: string, enabled: boolean) {
  return useQuery({
    queryKey: ['astrorder', 'file-mentions', session?.agent_id, session?.id, query],
    queryFn: async () => {
      if (!session) return []
      const result = await api.searchFiles({ q: query, sessionId: session.id, limit: 100 })
      const root = result.root.replace(/[\\/]+$/, '')
      return result.items.map(item => ({
        name: item.path.startsWith(root) ? item.path.slice(root.length).replace(/^[\\/]+/, '') : item.name,
        description: item.path,
        kind: 'file' as const,
        path: item.path,
      }))
    },
    enabled: Boolean(enabled && session),
    staleTime: 30_000,
    retry: false,
  })
}

export function filterAgentCommands(items: AgentCommand[], text: string): AgentCommand[] {
  const query = text.slice(1).toLocaleLowerCase()
  return items.filter(item =>
    item.name.toLocaleLowerCase().includes(query)
    || item.description.toLocaleLowerCase().includes(query),
  )
}

export function filterAgentMentions(items: AgentMention[], text: string): AgentMention[] {
  const query = text.slice(text.lastIndexOf('@') + 1).toLocaleLowerCase()
  return items.filter(item =>
    item.name.toLocaleLowerCase().includes(query)
    || item.description.toLocaleLowerCase().includes(query),
  )
}

export function formatAgentMention(item: AgentMention): string {
  if (item.kind === 'skill') return item.name.includes(' ') ? `@"${item.name}" ` : `@${item.name} `
  const path = item.name.replaceAll('\\', '/')
  const label = path.split('/').at(-1) || path
  return `[${label}](${path}) `
}

export function AgentCommandMenu({
  agent,
  items,
  activeIndex,
  onSelect,
}: {
  agent?: Agent
  items: AgentCommand[]
  activeIndex: number
  onSelect: (item: AgentCommand) => void
}) {
  const listRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    listRef.current?.querySelector<HTMLElement>('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' })
  }, [activeIndex])
  if (!items.length) return null
  return (
    <Paper
      ref={listRef}
      withBorder
      shadow="md"
      radius="md"
      p={4}
      role="listbox"
      aria-label="Agent 命令"
      style={{ position: 'absolute', zIndex: 20, left: 0, right: 0, bottom: 'calc(100% + 6px)', maxHeight: 'min(280px, 45vh)', overflowY: 'auto' }}
    >
      <Stack gap={2}>
        {items.map((item, index) => (
          <UnstyledButton
            key={item.name}
            role="option"
            aria-selected={index === activeIndex}
            onMouseDown={event => event.preventDefault()}
            onClick={() => onSelect(item)}
            p="xs"
            style={{ borderRadius: 8, background: index === activeIndex ? 'var(--mantine-color-default-hover)' : undefined }}
          >
            <Group gap="sm" wrap="nowrap">
              {agent && <AgentBrandIcon kind={agent.kind} size={24} />}
              <div style={{ minWidth: 0 }}>
                <Text size="sm" fw={600}>/{item.name}</Text>
                <Text size="xs" c="dimmed" lineClamp={1}>{item.description}</Text>
              </div>
              {item.input_hint && <Text size="xs" c="dimmed" ml="auto">{item.input_hint}</Text>}
            </Group>
          </UnstyledButton>
        ))}
      </Stack>
    </Paper>
  )
}


export function AgentMentionMenu({
  agent,
  items,
  activeIndex,
  onSelect,
}: {
  agent?: Agent
  items: AgentMention[]
  activeIndex: number
  onSelect: (item: AgentMention) => void
}) {
  const listRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    listRef.current?.querySelector<HTMLElement>('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' })
  }, [activeIndex])
  if (!items.length) return null
  return (
    <Paper ref={listRef} withBorder shadow="md" radius="md" p={4} role="listbox" aria-label="可提及资源" style={{ position: 'absolute', zIndex: 20, left: 0, right: 0, bottom: 'calc(100% + 6px)', maxHeight: 'min(280px, 45vh)', overflowY: 'auto' }}>
      <Stack gap={2}>
        {items.map((item, index) => (
          <UnstyledButton key={`${item.kind}:${item.name}`} role="option" aria-selected={index === activeIndex} onMouseDown={event => event.preventDefault()} onClick={() => onSelect(item)} p="xs" style={{ borderRadius: 8, background: index === activeIndex ? 'var(--mantine-color-default-hover)' : undefined }}>
            <Group gap="sm" wrap="nowrap">
              {agent && <AgentBrandIcon kind={agent.kind} size={24} />}
              <div style={{ minWidth: 0 }}>
                <Text size="sm" fw={600}>@{item.name}</Text>
                <Text size="xs" c="dimmed" lineClamp={1}>{item.description}</Text>
              </div>
              <Text size="xs" c="dimmed" ml="auto">{item.kind === 'skill' ? 'Skill' : '文件'}</Text>
            </Group>
          </UnstyledButton>
        ))}
      </Stack>
    </Paper>
  )
}
