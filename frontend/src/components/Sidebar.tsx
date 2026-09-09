import { IconAdjustments, IconLayoutDashboard, IconMessageCircle, IconSparkles } from '@tabler/icons-react'
import { Group, NavLink, Stack, Text, ThemeIcon, Title } from '@mantine/core'
import { NavLink as RouterNavLink } from 'react-router-dom'
import type { Agent, Project, Session } from '../domain/types'
import { SessionRail } from './SessionRail'

const navItems = [
  { to: '/chat', label: '会话', icon: IconMessageCircle },
  { to: '/monitor', label: '监控室', icon: IconLayoutDashboard },
  { to: '/agents', label: '连接管理', icon: IconAdjustments },
]

export function Sidebar({
  sessions,
  agents,
  projects = [],
  activeSessionKey,
  onSelectSession,
  onNavigate,
}: {
  sessions: Session[]
  agents: Record<string, Agent>
  projects?: Project[]
  activeSessionKey?: string
  onSelectSession: (session: Session) => void
  onNavigate?: () => void
}) {
  return (
    <Stack className="sidebar-content" gap="lg">
      <Group className="sidebar-brand" gap="sm" wrap="nowrap">
        <ThemeIcon className="brand-mark" size={38} radius="xl" variant="light" color="indigo">
          <IconSparkles size={21} stroke={1.5} />
        </ThemeIcon>
        <div>
          <Title order={1} size="h4">星序 · Astrorder</Title>
          <Text size="xs" c="dimmed">独立 Agent 控制台</Text>
        </div>
      </Group>
      <nav aria-label="主导航">
        <Stack gap={4}>
          {navItems.map(({ to, label, icon: Icon }) => (
            <NavLink
              component={RouterNavLink}
              to={to}
              key={to}
              label={label}
              leftSection={<Icon size={19} stroke={1.7} />}
              onClick={onNavigate}
            />
          ))}
        </Stack>
      </nav>
      <div className="sidebar-divider" />
      <div className="rail-heading">
        <Text size="xs" fw={700} c="dimmed">项目会话</Text>
        <Text size="xs" c="dimmed">{sessions.length}</Text>
      </div>
      <SessionRail
        sessions={sessions}
        agents={agents}
        projects={projects}
        activeSessionKey={activeSessionKey}
        onSelect={(session) => {
          onSelectSession(session)
          onNavigate?.()
        }}
      />
      <Text className="sidebar-note" size="xs" c="dimmed">
        数据来自已认证的 HTTP / WebSocket 通道；空列表代表当前没有已连接 Agent。
      </Text>
    </Stack>
  )
}
