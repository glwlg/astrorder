import { IconAdjustments, IconLayoutDashboard, IconMessageCircle, IconPuzzle, IconSettings } from '@tabler/icons-react'
import { ActionIcon, Modal, NavLink, Stack, Tabs, Text } from '@mantine/core'
import { useState } from 'react'
import { NavLink as RouterNavLink } from 'react-router-dom'
import type { Agent, Project, Session } from '../domain/types'
import { AgentsPage } from '../features/agents/AgentsPage'
import { PluginsPage } from '../features/plugins/PluginsPage'
import { SessionRail } from './SessionRail'

const navItems = [
  { to: '/chat', label: '会话', icon: IconMessageCircle },
  { to: '/monitor', label: '监控室', icon: IconLayoutDashboard },
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
  const [settingsOpened, setSettingsOpened] = useState(false)
  return (
    <>
      <Stack className="sidebar-content" gap="md">
        <nav aria-label="主导航">
          <div className="sidebar-primary-nav">
            {navItems.map(({ to, label, icon: Icon }) => (
              <NavLink
                component={RouterNavLink}
                to={to}
                key={to}
                label={label}
                leftSection={<Icon size={19} stroke={1.7} />}
                onClick={onNavigate}
                variant="light"
                color="gray"
                className="sidebar-nav-link"
              />
            ))}
          </div>
        </nav>
        <div className="sidebar-divider" />
        <div className="rail-heading">
          <Text size="xs" fw={700} c="dimmed">项目会话</Text>
          <div className="rail-heading-actions">
            <Text size="xs" c="dimmed">{sessions.length}</Text>
            <ActionIcon variant="subtle" color="gray" size="sm" aria-label="打开设置" title="设置" onClick={() => setSettingsOpened(true)}>
              <IconSettings size={16} />
            </ActionIcon>
          </div>
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
      </Stack>
      <Modal className="settings-modal" opened={settingsOpened} onClose={() => setSettingsOpened(false)} title="设置" centered size="xl">
        <Tabs defaultValue="connections" keepMounted={false}>
          <Tabs.List>
            <Tabs.Tab value="connections" leftSection={<IconAdjustments size={16} />}>连接</Tabs.Tab>
            <Tabs.Tab value="plugins" leftSection={<IconPuzzle size={16} />}>插件</Tabs.Tab>
          </Tabs.List>
          <Tabs.Panel value="connections" pt="md"><AgentsPage /></Tabs.Panel>
          <Tabs.Panel value="plugins" pt="md"><PluginsPage /></Tabs.Panel>
        </Tabs>
      </Modal>
    </>
  )
}
