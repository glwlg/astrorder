import { IconAlertCircle, IconCircleCheck, IconLoader2, IconPlugConnected, IconPlug } from '@tabler/icons-react'
import { ActionIcon, Badge, Group, Text, Tooltip } from '@mantine/core'
import type { AgentStatus, ConnectionStatus, SessionStatus } from '../domain/types'

const agentLabels: Record<AgentStatus, string> = {
  disconnected: '未连接',
  connecting: '连接中',
  ready: '就绪',
  error: '错误',
}

const sessionLabels: Record<SessionStatus, string> = {
  idle: '空闲',
  running: '运行中',
  waiting_approval: '待确认',
  error: '错误',
}

export function StatusDot({ status }: { status: AgentStatus | SessionStatus | ConnectionStatus }) {
  return <span className={`status-dot status-${status}`} aria-hidden="true" />
}

export function AgentStatusBadge({ status }: { status: AgentStatus }) {
  const color = status === 'ready' ? 'teal' : status === 'error' ? 'red' : status === 'connecting' ? 'yellow' : 'gray'
  const Icon = status === 'ready' ? IconCircleCheck : status === 'error' ? IconAlertCircle : status === 'connecting' ? IconLoader2 : IconPlug
  return (
    <Badge className="status-badge" color={color} variant="light" leftSection={<Icon size={13} />}>
      {agentLabels[status]}
    </Badge>
  )
}

export function SessionStatusLabel({ status }: { status: SessionStatus }) {
  return (
    <Group className="status-label" gap={6} wrap="nowrap">
      <StatusDot status={status} />
      <Text size="xs" c="dimmed">{sessionLabels[status]}</Text>
    </Group>
  )
}

export function ConnectionBadge({ status }: { status: ConnectionStatus }) {
  const label = status === 'connected' ? '实时连接' : status === 'connecting' ? '连接中' : status === 'error' ? '连接异常' : '离线'
  const color = status === 'connected' ? 'teal' : status === 'error' ? 'red' : status === 'connecting' ? 'yellow' : 'gray'
  return (
    <Tooltip label={`连接状态：${label}`}>
      <ActionIcon
        className="connection-badge"
        color={color}
        variant="light"
        size="md"
        radius="md"
        aria-label={`连接状态：${label}`}
      >
        <IconPlugConnected size={16} />
      </ActionIcon>
    </Tooltip>
  )
}
