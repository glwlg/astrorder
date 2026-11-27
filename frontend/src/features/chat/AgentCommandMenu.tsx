import { Group, Paper, Stack, Text, UnstyledButton } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { api } from '../../api/client'
import { AgentBrandIcon } from '../../components/AgentBrandIcon'
import '../../components/animations/AnimatedList.css'
import type { Agent, AgentCommand, AgentMention, Session } from '../../domain/types'
import './AgentMentionMenu.css'

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
    queryKey: ['astrorder', 'file-mentions', session?.agent_id, session?.id, session?.connection_id, query],
    queryFn: async () => {
      if (!session) return []
      const result = await api.searchFiles({ q: query, sessionId: session.id, connectionId: session.connection_id || undefined, limit: 100 })
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
  | { kind: 'office_prompt'; name: string; description: string; template: string }
  | { kind: 'command'; command: AgentCommand }

const OFFICE_PROMPT_COMMANDS: Array<{ name: string; description: string; template: string }> = [
  { name: '总结', description: '提炼核心结论、关键数据与后续待办', template: '请帮我总结以下内容的核心结论、关键数据与待办事项：\n' },
  { name: '润色', description: '优化公文、邮件或汇报方案的语气与表达', template: '请帮我润色以下文稿，使语气更专业、条理更清晰：\n' },
  { name: '翻译', description: '中英商务文档精准互译，保持专业术语一致', template: '请将以下内容翻译为流畅自然的商务表达（保留原有格式）：\n' },
  { name: '大纲', description: '为汇报、方案或 PPT 快速生成结构化大纲', template: '请围绕以下主题生成一份清晰的结构化方案/PPT 大纲：\n' },
  { name: '表格', description: '将零散信息整理成规范的 Markdown 对比表格', template: '请将以下内容整理成结构化表格，并补充简要分析结论：\n' },
  { name: '脱敏', description: '隐去姓名、账号、地址与内部敏感数据以便对外发送', template: '请对以下文稿进行对外脱敏处理，替换内部敏感信息并保持原意完整：\n' },
]

const DEV_ONLY_COMMAND_NAMES = new Set(['review', 'diff', 'compact', 'mcp', 'init', 'status', 'memory', 'bug'])

const OFFICE_DOC_EXT_RE = /\.(?:docx?|xlsx?|pptx?|pdf|md|markdown|csv|txt|wps|et|dps)$/i

export function buildCommandMenuItems(
  commands: AgentCommand[],
  branches: string[],
  text: string,
  options?: { officeMode?: boolean },
): CommandMenuItem[] {
  const trimmed = text.trim()
  const lower = trimmed.toLowerCase()

  if (options?.officeMode) {
    const query = lower.startsWith('/') ? lower.slice(1) : lower
    const matchedOffice: CommandMenuItem[] = OFFICE_PROMPT_COMMANDS
      .filter((cmd) => !query || cmd.name.toLowerCase().includes(query) || cmd.description.toLowerCase().includes(query))
      .map((cmd) => ({ kind: 'office_prompt', ...cmd }))
    const safeAgentCommands = filterAgentCommands(
      commands.filter((c) => !DEV_ONLY_COMMAND_NAMES.has(c.name.toLowerCase())),
      text,
    ).map((cmd): CommandMenuItem => ({ kind: 'command', command: cmd }))
    return [...matchedOffice, ...safeAgentCommands]
  }

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

export function filterAgentMentions(
  items: AgentMention[],
  text: string,
  options?: { officeMode?: boolean },
): AgentMention[] {
  const query = text.slice(text.lastIndexOf('@') + 1).toLocaleLowerCase()

  // 内置星序系统命令
  const swarmItem: AgentMention = {
    name: '群星',
    description: '星序多 Agent 协同：激活主星调度与伴星派生模式 (支持英文 @stars)',
    kind: 'skill',
    path: 'system:swarm',
  }
  const browserItem: AgentMention = {
    name: '浏览器',
    description: options?.officeMode ? '在内置浏览器中打开并辅助浏览网页' : '星序浏览器自动执行（支持英文 @browser）',
    kind: 'skill',
    path: 'system:browser',
  }
  const blackboardItem: AgentMention = {
    name: '黑板',
    description: '星序作战黑板：记录共享状态、事实与工件 (支持英文 @blackboard)',
    kind: 'skill',
    path: 'system:blackboard',
  }
  const bbArchItem: AgentMention = {
    name: '黑板:架构',
    description: '快捷模板：将方案决策写入黑板 (ArchitectureFlow / ApiEndpointsCard)',
    kind: 'skill',
    path: 'system:blackboard:arch',
  }
  const bbChecklistItem: AgentMention = {
    name: '黑板:清单',
    description: '快捷模板：将开发/任务分解清单写入黑板 (Checklist / StepTimeline)',
    kind: 'skill',
    path: 'system:blackboard:checklist',
  }
  const bbExportItem: AgentMention = {
    name: '黑板:沉淀',
    description: '快捷模板：将当前黑板关键事实固化导出为本地 Markdown 文档',
    kind: 'skill',
    path: 'system:blackboard:export',
  }
  const botItems = items.filter((item) => item.kind === 'bot')
  const nonBotItems = items.filter((item) => item.kind !== 'bot')
  const sortedItems = options?.officeMode
    ? [...nonBotItems].sort((a, b) => {
        const aDoc = a.kind === 'file' && OFFICE_DOC_EXT_RE.test(a.name) ? 1 : 0
        const bDoc = b.kind === 'file' && OFFICE_DOC_EXT_RE.test(b.name) ? 1 : 0
        return bDoc - aDoc
      })
    : nonBotItems
  const allItems = options?.officeMode
    ? [browserItem, ...sortedItems]
    : [
        ...botItems,
        swarmItem,
        browserItem,
        blackboardItem,
        bbArchItem,
        bbChecklistItem,
        bbExportItem,
        ...sortedItems,
      ]

  return allItems.filter(item =>
    item.name.toLocaleLowerCase().includes(query)
    || item.description.toLocaleLowerCase().includes(query)
    || (item.kind === 'bot' && ('bot'.includes(query) || (item.botSlug || '').toLocaleLowerCase().includes(query)))
    || (item.name.startsWith('黑板') && ('blackboard'.includes(query) || 'bb'.includes(query)))
    || (item.name === '群星' && 'stars'.includes(query))
    || (item.name === '浏览器' && 'browser'.includes(query))
  )
}

export function formatAgentMention(item: AgentMention): string {
  if (item.kind === 'bot') return `@Bot:${item.botSlug || item.name.replace(/^Bot:/i, '')} `
  if (item.path === 'system:swarm') return '@群星 '
  if (item.path === 'system:browser') return '@浏览器 '
  if (item.path === 'system:blackboard') return '@黑板 '
  if (item.path === 'system:blackboard:arch') return '@黑板 [架构规范] '
  if (item.path === 'system:blackboard:checklist') return '@黑板 [任务清单] '
  if (item.path === 'system:blackboard:export') return '@黑板 [固化沉淀] '
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
      className="astr-animated-list"
      style={{ position: 'absolute', zIndex: 20, left: 0, right: 0, bottom: 'calc(100% + 6px)', maxHeight: 'min(280px, 45vh)', overflowY: 'auto' }}
    >
      <Stack gap={2}>
        {items.map((item, index) => {
          if (!('kind' in item)) {
            return (
              <UnstyledButton
                key={item.name}
                className="astr-animated-list-item"
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
                className="astr-animated-list-item"
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
                className="astr-animated-list-item"
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
          if (item.kind === 'office_prompt') {
            return (
              <UnstyledButton
                key={`office-prompt-${item.name}`}
                className="astr-animated-list-item"
                role="option"
                aria-selected={index === activeIndex}
                onMouseDown={event => event.preventDefault()}
                onClick={() => onSelect(item)}
                p="xs"
                style={{ borderRadius: 8, background: index === activeIndex ? 'var(--mantine-color-default-hover)' : undefined }}
              >
                <Group gap="sm" wrap="nowrap">
                  <div style={{ minWidth: 0 }}>
                    <Text size="sm" fw={600}>/{item.name}</Text>
                    <Text size="xs" c="dimmed" lineClamp={1}>{item.description}</Text>
                  </div>
                  <Text size="xs" c="dimmed" ml="auto">快捷写作</Text>
                </Group>
              </UnstyledButton>
            )
          }
          return (
            <UnstyledButton
              key={item.command.name}
              className="astr-animated-list-item"
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
    <Paper
      ref={listRef}
      withBorder
      shadow="md"
      radius="md"
      p={4}
      role="listbox"
      aria-label="可提及资源"
      className="agent-mention-menu"
    >
      <Stack gap={2}>
        {items.map((item, index) => (
          <UnstyledButton
            key={`${item.kind}:${item.name}`}
            className="agent-mention-menu__option"
            role="option"
            aria-selected={index === activeIndex}
            onMouseDown={event => event.preventDefault()}
            onClick={() => onSelect(item)}
            p="xs"
          >
            <Group gap="sm" wrap="nowrap">
              {agent && <AgentBrandIcon kind={agent.kind} size={24} />}
              <div style={{ minWidth: 0 }}>
                <Text size="sm" fw={600} truncate>@{item.name}</Text>
                <Text size="xs" className="agent-mention-menu__meta" lineClamp={1}>{item.description}</Text>
              </div>
              <Text size="xs" className="agent-mention-menu__meta" ml="auto">
                {item.kind === 'bot' ? 'Bot' : item.kind === 'skill' ? 'Skill' : '文件'}
              </Text>
            </Group>
          </UnstyledButton>
        ))}
      </Stack>
    </Paper>
  )
}
