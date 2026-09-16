import { IconPlugConnected } from '@tabler/icons-react'
import { ActionIcon, Group, Text, Tooltip } from '@mantine/core'
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
  return (
    <span className={`agent-status-tag is-${status}`}>
      <span className="status-indicator-dot" />
      <span>{agentLabels[status]}</span>
    </span>
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
        variant="subtle"
        size="md"
        radius="md"
        aria-label={`连接状态：${label}`}
      >
        <IconPlugConnected size={16} />
      </ActionIcon>
    </Tooltip>
  )
}
