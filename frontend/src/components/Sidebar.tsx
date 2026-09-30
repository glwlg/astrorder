import { IconAdjustments, IconBolt, IconBrain, IconChartHistogram, IconDeviceDesktop, IconLayoutDashboard, IconMessageCircle, IconPuzzle, IconSettings, IconTopologyStarRing, IconUsers, IconWorld } from '@tabler/icons-react'
import { ActionIcon, HoverCard, Modal, NavLink, Stack, Tabs, Text, Tooltip } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { NavLink as RouterNavLink, useLocation } from 'react-router-dom'
import { api, type OcxUsageResponse } from '../api/client'
import type { Agent, Project, Session } from '../domain/types'
import { RtkSettingsPage } from '../features/rtk/RtkSettingsPage'
import './settings.css'
import { AgentsPage } from '../features/agents/AgentsPage'
import { formatTokens, getModelColor } from '../features/analytics/AnalyticsPage'
import { PluginsPage } from '../features/plugins/PluginsPage'
import { NetworkSettingsPage } from '../features/network/NetworkSettingsPage'
import { DesktopSettingsPage } from '../features/services/DesktopSettingsPage'
import { ModelGatewaySettingsPage } from '../features/services/ModelGatewaySettingsPage'
import { CapabilitiesSettingsCard } from '../features/services/CapabilitiesSettingsCard'
import { SessionRail } from './SessionRail'
import { BotGroupRail } from './BotGroupRail'
import { SwarmRootRail } from './SwarmRootRail'

const navItems = [
  { to: '/chat', label: '会话', icon: IconMessageCircle, id: 'chat' },
  { to: '/monitor', label: '监控室', icon: IconLayoutDashboard, id: 'monitor' },
  { to: '/swarm', label: '星图', icon: IconTopologyStarRing, id: 'swarm' },
  { to: '/groups', label: '群聊', icon: IconUsers, id: 'groups' },
  { to: '/analytics', label: '统计', icon: IconChartHistogram, id: 'analytics' },
]

function TodayUsagePreview({ data, pending, error, updatedAt }: {
  data?: OcxUsageResponse
  pending: boolean
  error: boolean
  updatedAt: number
}) {
  const now = new Date()
  const today = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
  const day = data?.days.find(row => row.date === today)
  const models = day?.models?.filter(row => Number.isFinite(row.totalTokens))
    .sort((a, b) => b.totalTokens - a.totalTokens) || []
  return (
    <div className="sidebar-usage-preview">
      <div className="sidebar-usage-heading">
        <strong>今日模型用量</strong>
        {updatedAt > 0 && <span>更新于 {new Date(updatedAt).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })}</span>}
      </div>
      {day ? (
        <>
          <div className="sidebar-usage-summary">
            <span>{day.requests.toLocaleString('zh-CN')} 次请求</span>
            <strong>{formatTokens(day.totalTokens)} Token</strong>
          </div>
          <div className="sidebar-usage-models">
            {models.length ? models.map(row => (
              <div className="sidebar-usage-model" key={`${row.provider}:${row.model}`}>
                <span className="sidebar-usage-model-name" title={`${row.provider}/${row.model}`}>
                  <i style={{ background: getModelColor(row.model, row.provider) }} />
                  <span>{row.model}</span>
                </span>
                <strong>{formatTokens(row.totalTokens)}</strong>
              </div>
            )) : <span className="sidebar-usage-state">暂无模型用量</span>}
          </div>
          {error && <span className="sidebar-usage-state">更新失败，显示上次数据</span>}
        </>
      ) : <span className="sidebar-usage-state">{pending ? '正在读取今日用量…' : error ? '今日用量暂不可用' : '今天暂无用量'}</span>}
    </div>
  )
}

function addSessionToMonitor(sessionKey: string, title?: string) {
  try {
    const raw = localStorage.getItem('astrorder:monitor-sessions')
    const current: string[] = raw ? JSON.parse(raw) : []
    if (!current.includes(sessionKey)) {
      const next = [...current, sessionKey]
      localStorage.setItem('astrorder:monitor-sessions', JSON.stringify(next))
      for (const storageKey of ['astrorder:monitor-auto-sessions', 'astrorder:monitor-excluded-sessions']) {
        const values: string[] = JSON.parse(localStorage.getItem(storageKey) || '[]')
        localStorage.setItem(storageKey, JSON.stringify(values.filter(key => key !== sessionKey)))
      }
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
  const location = useLocation()
  const [settingsOpened, setSettingsOpened] = useState(false)
  const [isMonitorDragOver, setIsMonitorDragOver] = useState(false)
  const todayUsage = useQuery({
    queryKey: ['astrorder', 'analytics', 'today-models'],
    queryFn: () => api.getAnalyticsUsage({ range: '7d', surface: 'all' }),
    enabled: !onNavigate,
    staleTime: 60_000,
    refetchInterval: 60_000,
    refetchOnWindowFocus: false,
    retry: false,
  })

  const isSwarm = location.pathname.startsWith('/swarm')
  const isGroups = location.pathname.startsWith('/groups') || location.pathname.startsWith('/chat/group-')
  const isMonitorRoute = location.pathname.startsWith('/monitor')
  const isAnalytics = location.pathname.startsWith('/analytics')
  const isChat = !isSwarm && !isGroups && !isMonitorRoute && !isAnalytics

  return (
    <>
      <Stack className="sidebar-content" gap="md">
        <nav aria-label="主导航">
          <div className="sidebar-primary-nav">
            {navItems.map(({ to, label, icon: Icon, id }) => {
              const isThisMonitor = id === 'monitor'
              const isActive =
                (id === 'swarm' && isSwarm) ||
                (id === 'groups' && isGroups) ||
                (id === 'monitor' && isMonitorRoute) ||
                (id === 'analytics' && isAnalytics) ||
                (id === 'chat' && isChat)
              const link = (
                <NavLink
                    component={RouterNavLink}
                    to={to}
                    leftSection={<Icon size={18} stroke={1.8} />}
                    onClick={onNavigate}
                    data-active={isActive ? 'true' : undefined}
                    aria-label={label}
                    className={`sidebar-nav-link ${isActive ? 'active' : ''} ${isThisMonitor && isMonitorDragOver ? 'is-drag-over' : ''}`}
                    onDragOver={isThisMonitor ? (e) => {
                      e.preventDefault()
                      e.dataTransfer.dropEffect = 'copy'
                      setIsMonitorDragOver(true)
                    } : undefined}
                    onDragLeave={isThisMonitor ? () => setIsMonitorDragOver(false) : undefined}
                    onDrop={isThisMonitor ? (e) => {
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
              return id === 'analytics' ? (
                <HoverCard key={to} width={280} position="right-start" withArrow openDelay={150} closeDelay={150}>
                  <HoverCard.Target>{link}</HoverCard.Target>
                  <HoverCard.Dropdown>
                    <TodayUsagePreview data={todayUsage.data} pending={todayUsage.isPending} error={todayUsage.isError} updatedAt={todayUsage.dataUpdatedAt} />
                  </HoverCard.Dropdown>
                </HoverCard>
              ) : (
                <Tooltip key={to} label={label} position="bottom" withArrow openDelay={200}>{link}</Tooltip>
              )
            })}
          </div>
        </nav>
        <div className="sidebar-divider" />
        {isSwarm ? (
          <SwarmRootRail
            sessions={sessions}
            agents={agents}
            onNavigate={onNavigate}
          />
        ) : isGroups ? (
          <BotGroupRail
            agents={agents}
            onNavigate={onNavigate}
          />
        ) : (
          <>
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
          </>
        )}
      </Stack>
      <Modal
        className="settings-modal"
        opened={settingsOpened}
        onClose={() => setSettingsOpened(false)}
        title="设置"
        centered
        size="80%"
        styles={{
          content: { width: '80vw', maxWidth: '1600px', height: '80vh', maxHeight: '1000px', display: 'flex', flexDirection: 'column' },
          body: { flex: 1, overflow: 'hidden', display: 'flex', flexDirection: 'column', padding: '0 16px 16px' },
        }}
      >
        <Tabs defaultValue="connections" orientation="vertical" keepMounted={true} className="settings-tabs">
          <Tabs.List>
            <Tabs.Tab value="connections" leftSection={<IconAdjustments size={16} />}>连接</Tabs.Tab>
            <Tabs.Tab value="models" leftSection={<IconBrain size={16} />}>模型与网关</Tabs.Tab>
            <Tabs.Tab value="skills" leftSection={<IconBolt size={16} />}>能力中心</Tabs.Tab>
            <Tabs.Tab value="rtk" leftSection={<IconChartHistogram size={16} />}>RTK</Tabs.Tab>
            <Tabs.Tab value="network" leftSection={<IconWorld size={16} />}>网络与移动端</Tabs.Tab>
            <Tabs.Tab value="desktop" leftSection={<IconDeviceDesktop size={16} />}>桌面客户端</Tabs.Tab>
            <Tabs.Tab value="plugins" leftSection={<IconPuzzle size={16} />}>插件</Tabs.Tab>
          </Tabs.List>
          <Tabs.Panel value="connections" className="settings-tab-panel"><AgentsPage /></Tabs.Panel>
          <Tabs.Panel value="models" className="settings-tab-panel"><ModelGatewaySettingsPage /></Tabs.Panel>
          <Tabs.Panel value="skills" className="settings-tab-panel" data-tab-panel-skills="true" style={{ overflow: 'hidden' }}>
            <CapabilitiesSettingsCard />
          </Tabs.Panel>
          <Tabs.Panel value="rtk" className="settings-tab-panel" keepMounted={false}><RtkSettingsPage /></Tabs.Panel>
          <Tabs.Panel value="network" className="settings-tab-panel"><NetworkSettingsPage /></Tabs.Panel>
          <Tabs.Panel value="desktop" className="settings-tab-panel"><DesktopSettingsPage /></Tabs.Panel>
          <Tabs.Panel value="plugins" className="settings-tab-panel"><PluginsPage /></Tabs.Panel>
        </Tabs>
      </Modal>
    </>
  )
}
