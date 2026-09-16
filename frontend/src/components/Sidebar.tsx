import { IconAdjustments, IconLayoutDashboard, IconMessageCircle, IconPuzzle, IconSettings } from '@tabler/icons-react'
import { ActionIcon, Modal, NavLink, Stack, Tabs, Text } from '@mantine/core'
import { notifications } from '@mantine/notifications'
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

function addSessionToMonitor(sessionKey: string, title?: string) {
  try {
    const raw = localStorage.getItem('astrorder:monitor-sessions')
    const current: string[] = raw ? JSON.parse(raw) : []
    if (!current.includes(sessionKey)) {
      const next = [...current, sessionKey]
      localStorage.setItem('astrorder:monitor-sessions', JSON.stringify(next))
      window.dispatchEvent(new CustomEvent('astrorder:monitor-sessions-changed', { detail: next }))
      notifications.show({
        color: 'teal',
        message: `已将“${title || '会话'}”加入监控室`,
      })
    } else {
      notifications.show({
        color: 'blue',
        message: `“${title || '会话'}”已在监控室中`,
      })
    }
  } catch {}
}

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
  const [isMonitorDragOver, setIsMonitorDragOver] = useState(false)
  return (
    <>
      <Stack className="sidebar-content" gap="md">
        <nav aria-label="主导航">
          <div className="sidebar-primary-nav">
            {navItems.map(({ to, label, icon: Icon }) => {
              const isMonitor = to === '/monitor'
              return (
                <NavLink
                  component={RouterNavLink}
                  to={to}
                  key={to}
                  label={label}
                  leftSection={<Icon size={16} stroke={1.8} />}
                  onClick={onNavigate}
                  className={`sidebar-nav-link ${isMonitor && isMonitorDragOver ? 'is-drag-over' : ''}`}
                  onDragOver={isMonitor ? (e) => {
                    e.preventDefault()
                    e.dataTransfer.dropEffect = 'copy'
                    setIsMonitorDragOver(true)
                  } : undefined}
                  onDragLeave={isMonitor ? () => setIsMonitorDragOver(false) : undefined}
                  onDrop={isMonitor ? (e) => {
                    e.preventDefault()
                    setIsMonitorDragOver(false)
                    let key = ''
                    let title = '会话'
                    const raw = e.dataTransfer.getData('application/x-astrorder-session')
                    if (raw) {
                      try {
                        const item = JSON.parse(raw)
                        key = item.key || `${item.agent_id}::${item.id}`
                        title = item.title || '会话'
                      } catch {}
                    }
                    if (!key) {
                      key = e.dataTransfer.getData('text/plain')
                    }
                    if (key) {
                      addSessionToMonitor(key, title)
                    }
                  } : undefined}
                />
              )
            })}
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
      <Modal
        className="settings-modal"
        opened={settingsOpened}
        onClose={() => setSettingsOpened(false)}
        title="设置"
        centered
        size="80%"
        styles={{
          content: { width: '80%', maxWidth: '1440px', minWidth: 'min(92vw, 760px)' },
        }}
      >
        <Tabs defaultValue="connections" keepMounted={true} className="settings-tabs">
          <Tabs.List>
            <Tabs.Tab value="connections" leftSection={<IconAdjustments size={16} />}>连接</Tabs.Tab>
            <Tabs.Tab value="plugins" leftSection={<IconPuzzle size={16} />}>插件</Tabs.Tab>
          </Tabs.List>
          <Tabs.Panel value="connections" className="settings-tab-panel"><AgentsPage /></Tabs.Panel>
          <Tabs.Panel value="plugins" className="settings-tab-panel"><PluginsPage /></Tabs.Panel>
        </Tabs>
      </Modal>
    </>
  )
}
