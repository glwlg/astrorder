import { IconArrowUpRight, IconBolt, IconCircleCheck, IconClock, IconListCheck, IconShieldCheck } from '@tabler/icons-react'
import { Badge, Button, Group, Paper, SegmentedControl, SimpleGrid, Stack, Text, Title } from '@mantine/core'
import { useMemo, useState } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { useNavigate } from 'react-router-dom'
import type { Agent, Command, Session } from '../../domain/types'
import { EmptyState } from '../../components/EmptyState'
import { AgentStatusBadge, SessionStatusLabel } from '../../components/Status'
import { useSessionResources } from '../../hooks/useAstrorderData'
import { selectApprovals, selectCommands, selectMessages, useAstrorderStore } from '../../state/store'
import { scopeKey } from '../../domain/semantics'

function MonitorCard({ session, agent, onOpen }: { session: Session; agent?: Agent; onOpen: () => void }) {
  useSessionResources(session, true)
  const commands = useAstrorderStore(useShallow((state) => selectCommands(state, session.agent_id, session.id)))
  const messages = useAstrorderStore(useShallow((state) => selectMessages(state, session.agent_id, session.id)))
  const approvals = useAstrorderStore(useShallow((state) => selectApprovals(state, session.agent_id, session.id)))
  const activities = messages.filter((message) => message.kind === 'thinking' || message.kind === 'tool').slice(-3).reverse()
  const activeCommand = commands.find((command) => command.state === 'running' || command.state === 'accepted')
  const latest = activities[0]
  return (
    <Paper className={`monitor-card monitor-${session.status}`} data-testid={`monitor-card-${session.agent_id}-${session.id}`} withBorder radius="lg" p="md">
      <Stack gap="sm">
        <Group justify="space-between" align="flex-start" wrap="nowrap">
          <div className="monitor-card-title">
            <Group gap="xs" wrap="nowrap"><span className="monitor-status-orb" aria-hidden="true" /><Title order={3} size="h4">{session.title || '未命名会话'}</Title></Group>
            <Text size="xs" c="dimmed" mt={4}>{agent?.name || session.agent_id} · {session.workspace || '未提供工作区'}</Text>
          </div>
          <SessionStatusLabel status={session.status} />
        </Group>
        <Group gap="xs" wrap="wrap">
          {agent && <AgentStatusBadge status={agent.status} />}
          {activeCommand && <Badge color="indigo" variant="light" leftSection={<IconBolt size={13} />}>指令执行中</Badge>}
          {approvals.length > 0 && <Badge color="yellow" variant="light" leftSection={<IconShieldCheck size={13} />}>{approvals.length} 个待确认</Badge>}
        </Group>
        <div className="monitor-activity-list" aria-label="最近活动">
          {latest ? (
            <Group gap="xs" wrap="nowrap"><IconBolt size={15} className="activity-icon" /><Text size="sm" lineClamp={2}>{latest.text || '工具活动'}</Text></Group>
          ) : activeCommand ? (
            <Group gap="xs" wrap="nowrap"><IconClock size={15} className="activity-icon" /><Text size="sm">{activeCommand.text || '命令正在运行'}</Text></Group>
          ) : (
            <Text size="sm" c="dimmed">暂无活动事件</Text>
          )}
        </div>
        <Group justify="space-between" mt="xs">
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
  const sessions = useAstrorderStore(useShallow((state) => Object.values(state.sessions).sort((a, b) => b.updated_at.localeCompare(a.updated_at))))
  const agents = useAstrorderStore((state) => state.agents)
  const commands = useAstrorderStore(useShallow((state) => Object.values(state.commands)))
  const orderedSessions = useMemo(() => {
    return [...sessions].sort((a, b) => scopeKey(a.agent_id, a.id).localeCompare(scopeKey(b.agent_id, b.id)))
  }, [sessions])
  const queuedCommands = useMemo(() => commands.filter((command) => command.state === 'queued').sort((a, b) => a.created_at.localeCompare(b.created_at)), [commands])
  const counts = useMemo(() => ({
    running: orderedSessions.filter((session) => session.status === 'running').length,
    waiting: orderedSessions.filter((session) => session.status === 'waiting_approval').length,
  }), [orderedSessions])

  return (
    <div className="route-page monitor-page">
      <Group className="route-heading" justify="space-between" align="flex-end" mb="lg" wrap="wrap">
        <div>
          <Text className="eyebrow" size="xs" fw={700}>LIVE WORKSPACE</Text>
          <Title order={2} size="h2" mt={4}>监控室</Title>
          <Text c="dimmed" mt={5}>按事件观察多个会话的运行状态、工具活动和待确认操作。</Text>
        </div>
        <Group gap="sm">
          <Group className="monitor-summary" gap="xs"><Badge color="indigo" variant="light" leftSection={<IconBolt size={13} />}>{counts.running} 运行中</Badge><Badge color="yellow" variant="light" leftSection={<IconClock size={13} />}>{counts.waiting} 待确认</Badge><Badge color={queuedCommands.length ? 'yellow' : 'gray'} variant="light" leftSection={<IconListCheck size={13} />}>{queuedCommands.length} 待机</Badge><Text size="sm" c="dimmed">{orderedSessions.length} 个会话</Text></Group>
          <SegmentedControl value={layout} onChange={setLayout} data={[{ label: '网格', value: 'grid' }, { label: '列表', value: 'list' }]} aria-label="监控室布局" />
        </Group>
      </Group>
      {queuedCommands.length > 0 && <Paper className="monitor-queue" withBorder radius="lg" p="md" mb="md" aria-label="待机队列">
        <Group justify="space-between" mb="sm"><div><Title order={3} size="h4">待机队列</Title><Text size="sm" c="dimmed">仅显示服务端报告为 queued 的命令。</Text></div><Badge color="yellow" variant="light">{queuedCommands.length} 项</Badge></Group>
        <Stack gap="xs">
          {queuedCommands.map((command: Command) => {
            const session = orderedSessions.find((item) => scopeKey(item.agent_id, item.id) === scopeKey(command.agent_id, command.session_id))
            return <Button key={`${command.agent_id}::${command.id}`} className="monitor-queue-item" variant="subtle" justify="space-between" onClick={() => session && navigate(`/chat/${encodeURIComponent(session.id)}?agent_id=${encodeURIComponent(session.agent_id)}`)}>
              <span>{command.text || '无文本命令'}</span><Text component="span" size="xs" c="dimmed">{session?.title || '会话未返回'}</Text>
            </Button>
          })}
        </Stack>
      </Paper>}
      {orderedSessions.length === 0 ? (
        <EmptyState icon={<IconCircleCheck />} title="监控室暂无会话" description="没有模拟卡片。认证后的 Agent 会话和事件进入服务端后，监控室会自动呈现。" />
      ) : (
        <SimpleGrid className={`monitor-grid monitor-layout-${layout}`} cols={{ base: 1, sm: layout === 'grid' ? 2 : 1, lg: layout === 'grid' ? 3 : 1 }} spacing="md">
          {orderedSessions.map((session) => (
            <MonitorCard
              key={`${session.agent_id}::${session.id}`}
              session={session}
              agent={agents[session.agent_id]}
              onOpen={() => navigate(`/chat/${encodeURIComponent(session.id)}?agent_id=${encodeURIComponent(session.agent_id)}`)}
            />
          ))}
        </SimpleGrid>
      )}
    </div>
  )
}
