import { IconArrowUpRight, IconBolt, IconCircleCheck, IconClock, IconListCheck, IconPlus, IconSend, IconShieldCheck, IconX } from '@tabler/icons-react'
import { ActionIcon, Badge, Button, Group, Modal, Paper, SegmentedControl, SimpleGrid, Stack, Text, Textarea, TextInput, Title } from '@mantine/core'
import { useMemo, useState } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { useNavigate } from 'react-router-dom'
import { api } from '../../api/client'
import type { Agent, Command, Session } from '../../domain/types'
import { EmptyState } from '../../components/EmptyState'
import { AgentStatusBadge, SessionStatusLabel } from '../../components/Status'
import { useSessionResources } from '../../hooks/useAstrorderData'
import { selectApprovals, selectCommands, selectMessages, useAstrorderStore } from '../../state/store'
import { newCommandId, scopeKey } from '../../domain/semantics'

const STORAGE_KEY = 'astrorder:monitor-sessions'

function active(session: Session) {
  return session.status === 'running' || session.status === 'waiting_approval' || session.live === true
}

function savedSessions(): string[] {
  try {
    const value = JSON.parse(localStorage.getItem(STORAGE_KEY) || '[]')
    return Array.isArray(value) ? value.filter(item => typeof item === 'string') : []
  } catch { return [] }
}

function MonitorCard({ session, agent, onOpen, onRemove }: { session: Session; agent?: Agent; onOpen: () => void; onRemove?: () => void }) {
  useSessionResources(session, true)
  const commands = useAstrorderStore(useShallow((state) => selectCommands(state, session.agent_id, session.id)))
  const messages = useAstrorderStore(useShallow((state) => selectMessages(state, session.agent_id, session.id)))
  const approvals = useAstrorderStore(useShallow((state) => selectApprovals(state, session.agent_id, session.id)))
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const [error, setError] = useState('')
  const activities = messages.filter((message) => message.kind === 'thinking' || message.kind === 'tool').slice(-3).reverse()
  const activeCommand = commands.find((command) => command.state === 'running' || command.state === 'accepted')
  const latest = activities[0]
  const send = async () => {
    const text = draft.trim()
    if (!text || sending) return
    setSending(true); setError('')
    try {
      const command = await api.createCommand({ id: newCommandId(), agent_id: session.agent_id, session_id: session.id, action: 'send', text, attachment_ids: [], target_id: null })
      useAstrorderStore.getState().mergeCommands([command])
      if (command.state === 'failed' || command.state === 'unknown') throw new Error(command.error || '消息发送结果未确认。')
      setDraft('')
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '消息发送失败。')
    } finally { setSending(false) }
  }
  const status = session.status === 'idle' && session.live ? 'running' : session.status
  return (
    <Paper className={`monitor-card monitor-${status}`} data-testid={`monitor-card-${session.agent_id}-${session.id}`} withBorder radius="lg" p="md">
      <Stack gap="sm" h="100%">
        <Group justify="space-between" align="flex-start" wrap="nowrap">
          <div className="monitor-card-title">
            <Group gap="xs" wrap="nowrap"><span className="monitor-status-orb" aria-hidden="true" /><Title order={3} size="h4">{session.title || '未命名会话'}</Title></Group>
            <Text size="xs" c="dimmed" mt={4}>{agent?.name || session.agent_id} · {session.workspace || '未提供工作区'}</Text>
          </div>
          <Group gap={4} wrap="nowrap"><SessionStatusLabel status={status} />{onRemove && <ActionIcon variant="subtle" color="gray" aria-label="移出监控室" onClick={onRemove}><IconX size={15} /></ActionIcon>}</Group>
        </Group>
        <Group gap="xs" wrap="wrap">
          {agent && <AgentStatusBadge status={agent.status} />}
          {activeCommand && <Badge color="dark" variant="light" leftSection={<IconBolt size={13} />}>指令执行中</Badge>}
          {approvals.length > 0 && <Badge color="yellow" variant="light" leftSection={<IconShieldCheck size={13} />}>{approvals.length} 个待确认</Badge>}
        </Group>
        <div className="monitor-activity-list" aria-label="最近活动">
          {latest ? <Group gap="xs" wrap="nowrap"><IconBolt size={15} className="activity-icon" /><Text size="sm" lineClamp={2}>{latest.text || '工具活动'}</Text></Group>
            : activeCommand ? <Group gap="xs" wrap="nowrap"><IconClock size={15} className="activity-icon" /><Text size="sm">{activeCommand.text || '命令正在运行'}</Text></Group>
              : <Text size="sm" c="dimmed">暂无活动事件</Text>}
        </div>
        <div className="monitor-composer">
          <Textarea aria-label={`发送到 ${session.title || '未命名会话'}`} placeholder="输入消息…" value={draft} onChange={event => setDraft(event.currentTarget.value)} onKeyDown={event => { if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); void send() } }} autosize minRows={1} maxRows={3} disabled={sending} />
          <ActionIcon color="dark" radius="xl" aria-label="发送消息" disabled={!draft.trim() || sending} onClick={() => void send()}><IconSend size={16} /></ActionIcon>
        </div>
        {error && <Text size="xs" c="red">{error}</Text>}
        <Group justify="space-between" mt="auto">
          <Text size="xs" c="dimmed">更新于 {new Date(session.updated_at).toLocaleString('zh-CN')}</Text>
          <Button size="compact-sm" variant="subtle" rightSection={<IconArrowUpRight size={15} />} onClick={onOpen}>查看会话</Button>
        </Group>
      </Stack>
    </Paper>
  )
}

export function MonitorPage() {
  const navigate = useNavigate()
  const [layout, setLayout] = useState('grid')
  const [pickerOpen, setPickerOpen] = useState(false)
  const [search, setSearch] = useState('')
  const [manualKeys, setManualKeys] = useState(savedSessions)
  const sessions = useAstrorderStore(useShallow((state) => Object.values(state.sessions).sort((a, b) => b.updated_at.localeCompare(a.updated_at))))
  const agents = useAstrorderStore((state) => state.agents)
  const commands = useAstrorderStore(useShallow((state) => Object.values(state.commands)))
  const manual = useMemo(() => new Set(manualKeys), [manualKeys])
  const orderedSessions = useMemo(() => sessions.filter(session => active(session) || manual.has(scopeKey(session.agent_id, session.id))), [manual, sessions])
  const candidates = useMemo(() => {
    const shown = new Set(orderedSessions.map(session => scopeKey(session.agent_id, session.id)))
    const query = search.trim().toLowerCase()
    return sessions.filter(session => !shown.has(scopeKey(session.agent_id, session.id)) && (!query || `${session.title} ${session.workspace || ''} ${agents[session.agent_id]?.name || ''}`.toLowerCase().includes(query)))
  }, [agents, orderedSessions, search, sessions])
  const saveManual = (next: string[]) => { setManualKeys(next); try { localStorage.setItem(STORAGE_KEY, JSON.stringify(next)) } catch { /* UI state still works for this tab. */ } }
  const queuedCommands = useMemo(() => commands.filter((command) => command.state === 'queued').sort((a, b) => a.created_at.localeCompare(b.created_at)), [commands])
  const counts = useMemo(() => ({ running: orderedSessions.filter(active).length, waiting: orderedSessions.filter(session => session.status === 'waiting_approval').length }), [orderedSessions])

  return (
    <div className="route-page monitor-page">
      <Group className="route-heading" justify="space-between" align="flex-end" mb="lg" wrap="wrap">
        <div><Text className="eyebrow" size="xs" fw={700}>LIVE WORKSPACE</Text><Title order={2} size="h2" mt={4}>监控室</Title><Text c="dimmed" mt={5}>集中查看进行中的会话，也可接入指定会话并直接发送消息。</Text></div>
        <Group gap="sm">
          <Group className="monitor-summary" gap="xs"><Badge color="dark" variant="light" leftSection={<IconBolt size={13} />}>{counts.running} 进行中</Badge><Badge color="yellow" variant="light" leftSection={<IconClock size={13} />}>{counts.waiting} 待确认</Badge><Badge color={queuedCommands.length ? 'yellow' : 'gray'} variant="light" leftSection={<IconListCheck size={13} />}>{queuedCommands.length} 待机</Badge><Text size="sm" c="dimmed">{orderedSessions.length} 个会话</Text></Group>
          <Button variant="light" leftSection={<IconPlus size={15} />} onClick={() => setPickerOpen(true)}>添加会话</Button>
          <SegmentedControl value={layout} onChange={setLayout} data={[{ label: '网格', value: 'grid' }, { label: '列表', value: 'list' }]} aria-label="监控室布局" />
        </Group>
      </Group>
      {queuedCommands.length > 0 && <Paper className="monitor-queue" withBorder radius="lg" p="md" mb="md" aria-label="待机队列"><Group justify="space-between" mb="sm"><div><Title order={3} size="h4">待机队列</Title><Text size="sm" c="dimmed">等待发送的会话消息。</Text></div><Badge color="yellow" variant="light">{queuedCommands.length} 项</Badge></Group><Stack gap="xs">{queuedCommands.map((command: Command) => { const session = sessions.find(item => scopeKey(item.agent_id, item.id) === scopeKey(command.agent_id, command.session_id)); return <Button key={`${command.agent_id}::${command.id}`} className="monitor-queue-item" variant="subtle" justify="space-between" onClick={() => session && navigate(`/chat/${encodeURIComponent(session.id)}?agent_id=${encodeURIComponent(session.agent_id)}`)}><span>{command.text || '无文本命令'}</span><Text component="span" size="xs" c="dimmed">{session?.title || '会话未返回'}</Text></Button> })}</Stack></Paper>}
      {orderedSessions.length === 0 ? <EmptyState icon={<IconCircleCheck />} title="暂无进行中的会话" description="会话开始运行后会自动出现，也可以手动添加会话。" />
        : <SimpleGrid className={`monitor-grid monitor-layout-${layout}`} cols={{ base: 1, sm: layout === 'grid' ? 2 : 1, lg: layout === 'grid' ? 3 : 1 }} spacing="md">{orderedSessions.map(session => { const key = scopeKey(session.agent_id, session.id); return <MonitorCard key={key} session={session} agent={agents[session.agent_id]} onRemove={!active(session) && manual.has(key) ? () => saveManual(manualKeys.filter(item => item !== key)) : undefined} onOpen={() => navigate(`/chat/${encodeURIComponent(session.id)}?agent_id=${encodeURIComponent(session.agent_id)}`)} /> })}</SimpleGrid>}
      <Modal opened={pickerOpen} onClose={() => { setPickerOpen(false); setSearch('') }} title="添加会话" centered>
        <TextInput aria-label="搜索会话" placeholder="搜索会话、项目或 Agent" value={search} onChange={event => setSearch(event.currentTarget.value)} mb="sm" />
        <Stack gap="xs" className="monitor-session-picker">{candidates.length ? candidates.map(session => { const key = scopeKey(session.agent_id, session.id); return <Button key={key} variant="subtle" fullWidth justify="space-between" onClick={() => { saveManual([...manualKeys, key]); setPickerOpen(false); setSearch('') }}><span>{session.title || '未命名会话'}</span><Text component="span" size="xs" c="dimmed">{agents[session.agent_id]?.name || session.agent_id}</Text></Button> }) : <Text size="sm" c="dimmed" ta="center" py="lg">没有可添加的会话</Text>}</Stack>
      </Modal>
    </div>
  )
}
