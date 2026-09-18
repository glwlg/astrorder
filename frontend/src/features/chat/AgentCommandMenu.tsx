import { Group, Paper, Stack, Text, UnstyledButton } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { api } from '../../api/client'
import { AgentBrandIcon } from '../../components/AgentBrandIcon'
import type { Agent, AgentCommand, AgentMention, Session } from '../../domain/types'

export function useGitBranches(session: Session | null | undefined, enabled: boolean) {
  return useQuery({
    queryKey: ['astrorder', 'git-branches', session?.id, session?.workspace, session?.connection_id],
    queryFn: async () => {
      if (!session) return []
      let localToken: string | null = null
      try {
        if (typeof localStorage !== 'undefined') {
          localToken = localStorage.getItem('astrorder:token')
        }
      } catch {}
      const headers: Record<string, string> = {}
      if (localToken) headers['Authorization'] = `Bearer ${localToken}`
      const params = new URLSearchParams()
      if (session.workspace) params.set('workspace', session.workspace)
      if (session.id) params.set('session_id', session.id)
      if (session.connection_id) params.set('connection_id', session.connection_id)
      const res = await fetch(`/api/v1/git/status?${params.toString()}`, { headers })
      if (!res.ok) return []
      const data = await res.json()
      return (data.branches || []) as string[]
    },
    enabled: Boolean(enabled && session),
    staleTime: 20_000,
  })
}

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

export type CommandMenuItem =
  | { kind: 'review_uncommitted'; label: string }
  | { kind: 'header'; label: string }
  | { kind: 'review_branch'; branch: string; label: string }
  | { kind: 'command'; command: AgentCommand }

export function buildCommandMenuItems(
  commands: AgentCommand[],
  branches: string[],
  text: string,
): CommandMenuItem[] {
  const trimmed = text.trim()
  const lower = trimmed.toLowerCase()

  // 1. 只有当用户明确输入 /review、/review ... 或 /审查 时，才切入二级分支审查选择菜单
  const isReviewMode = lower === '/review' || lower.startsWith('/review ') || lower === '/审查' || lower.startsWith('/审查 ')
  if (isReviewMode) {
    const branchQuery = lower.startsWith('/review ')
      ? lower.slice('/review '.length).trim()
      : lower.startsWith('/审查 ')
      ? lower.slice('/审查 '.length).trim()
      : ''
    const results: CommandMenuItem[] = []

    if (!branchQuery || '审查未提交的更改'.includes(branchQuery) || 'uncommitted'.includes(branchQuery)) {
      results.push({ kind: 'review_uncommitted', label: '审查未提交的更改' })
    }

    const matchedBranches = branches.filter(b => !branchQuery || b.toLowerCase().includes(branchQuery))
    if (matchedBranches.length > 0) {
      results.push({ kind: 'header', label: '对照基础分支审查' })
      for (const branch of matchedBranches) {
        results.push({ kind: 'review_branch', branch, label: branch })
      }
    }
    return results
  }

  // 2. 默认敲 "/" 时展示一级常规命令列表，确保包含 review 命令
  const normalCommands = commands.filter(c => c.name !== 'review')
  const allCommands = [
    { name: 'review', description: '代码审查 / 审查工作区变更', input_hint: '可选：分支或未提交更改' },
    ...normalCommands
  ]
  const filteredNormal = filterAgentCommands(allCommands, text)
  return filteredNormal.map(cmd => ({ kind: 'command', command: cmd }))
}

export function filterAgentMentions(items: AgentMention[], text: string): AgentMention[] {
  const query = text.slice(text.lastIndexOf('@') + 1).toLocaleLowerCase()

  // 内置星序系统命令：@群星 / @stars
  const swarmItem: AgentMention = {
    name: '群星',
    description: '星序多 Agent 协同：激活主星调度与伴星派生模式 (支持英文 @stars)',
    kind: 'skill',
    path: 'system:swarm',
  }
  const allItems = [swarmItem, ...items]

  return allItems.filter(item =>
    item.name.toLocaleLowerCase().includes(query)
    || item.description.toLocaleLowerCase().includes(query)
    || (item.name === '群星' && 'stars'.includes(query)),
  )
}

export function formatAgentMention(item: AgentMention): string {
  if (item.path === 'system:swarm') return '@群星 '
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
  items: (CommandMenuItem | AgentCommand)[]
  activeIndex: number
  onSelect: (item: any) => void
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
        {items.map((item, index) => {
          if (!('kind' in item)) {
            return (
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
            )
          }
          if (item.kind === 'header') {
            return (
              <div
                key={'header-' + item.label}
                style={{ padding: '6px 12px 2px 12px', fontSize: '11.5px', fontWeight: 600, color: 'var(--astr-muted)', userSelect: 'none' }}
              >
                {item.label}
              </div>
            )
          }
          if (item.kind === 'review_uncommitted') {
            return (
              <UnstyledButton
                key="review-uncommitted"
                role="option"
                aria-selected={index === activeIndex}
                onMouseDown={event => event.preventDefault()}
                onClick={() => onSelect(item)}
                p="xs"
                style={{
                  borderRadius: 8,
                  background: index === activeIndex ? 'var(--mantine-color-default-hover)' : undefined,
                }}
              >
                <Text size="sm" fw={index === activeIndex ? 600 : 500} style={{ color: 'var(--astr-text)' }}>
                  {item.label}
                </Text>
              </UnstyledButton>
            )
          }
          if (item.kind === 'review_branch') {
            return (
              <UnstyledButton
                key={'review-branch-' + item.branch}
                role="option"
                aria-selected={index === activeIndex}
                onMouseDown={event => event.preventDefault()}
                onClick={() => onSelect(item)}
                p="xs"
                style={{
                  borderRadius: 8,
                  background: index === activeIndex ? 'var(--mantine-color-default-hover)' : undefined,
                }}
              >
                <Text size="xs" fw={500} style={{ fontFamily: 'ui-monospace, monospace', color: 'var(--astr-text)' }}>
                  {item.branch}
                </Text>
              </UnstyledButton>
            )
          }
          return (
            <UnstyledButton
              key={item.command.name}
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
                  <Text size="sm" fw={600}>/{item.command.name}</Text>
                  <Text size="xs" c="dimmed" lineClamp={1}>{item.command.description}</Text>
                </div>
                {item.command.input_hint && <Text size="xs" c="dimmed" ml="auto">{item.command.input_hint}</Text>}
              </Group>
            </UnstyledButton>
          )
        })}
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
