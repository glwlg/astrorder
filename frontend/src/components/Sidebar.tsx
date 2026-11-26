import { ChartLineUpIcon, ChatCircleDotsIcon, GearSixIcon, HardDrivesIcon, IdentificationCardIcon, MicrophoneIcon, NotebookIcon, RobotIcon, SquaresFourIcon, TrayIcon, UsersThreeIcon } from './workbenchIcons'
import { IconLayoutSidebarLeftCollapse, IconLayoutSidebarLeftExpand, IconPin, IconPlus } from '@tabler/icons-react'
import { ActionIcon, HoverCard, Modal, NavLink, Stack, Text, Tooltip } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useQuery } from '@tanstack/react-query'
import { useCallback, useEffect, useId, useState } from 'react'
import { NavLink as RouterNavLink, useLocation, useNavigate } from 'react-router-dom'
import { api, type IntegrationItem, type OcxUsageResponse } from '../api/client'
import type { Agent, Project, Session } from '../domain/types'
import './settings.css'
import './arcSidebar.css'
import '../features/integrations/integrations.css'
import { SettingsDrawerScope } from './SettingsDrawer'
import { SettingsWorkbench, normalizeSettingsTab } from '../features/settings/SettingsWorkbench'
import { useShortcutHandlers } from '../features/shortcuts/shortcuts'
import { formatTokens, getModelColor } from '../features/analytics/usageFormat'
import { SessionRail } from './SessionRail'
import { BotGroupRail } from './BotGroupRail'
import { useAstrorderStore } from '../state/store'
import { groupHasActiveTask } from '../domain/groupActivity'
import { GroupTaskIndicator } from './GroupTaskIndicator'
import { NotificationPermissionControl } from './NotificationPermissionControl'
import { WorkspaceThemeToggle } from './WorkspaceThemeToggle'
import { connectionName } from './AgentSessionFilter'
import { scopeKey } from '../domain/semantics'
import { SessionAccountsHoverCard } from '../features/agentAccounts/SessionAccountsHoverCard'
import { accountPlatformMeta } from '../features/agentAccounts/agyAccountsApi'
import { sessionAccountTarget } from '../features/agentAccounts/AgyQuotaRefreshProvider'
import {
  SIDEBAR_INTEGRATIONS_PLUGIN_ID,
  WORKSPACE_AGENT_ACCOUNTS_PLUGIN_ID,
  WORKSPACE_ANALYTICS_GATEWAY_PLUGIN_ID,
  WORKSPACE_BACKUPS_PLUGIN_ID,
  WORKSPACE_DIGEST_PLUGIN_ID,
  WORKSPACE_GROUPS_PLUGIN_ID,
  WORKSPACE_MEETINGS_PLUGIN_ID,
  WORKSPACE_MONITOR_PLUGIN_ID,
} from '../features/plugins/builtinPlugins'
import { usePluginSettingsStore } from '../features/plugins/pluginSettingsStore'
import { useIntegrationsStore } from '../features/integrations/integrationStore'
import { IntegrationIcon } from '../features/integrations/integrationIcons'
import { IntegrationFormModal } from '../features/integrations/IntegrationFormModal'
import { useActionInbox } from '../features/inbox/useActionInbox'
import { useMeetingSessionStore } from '../features/meetings/meetingSessionStore'
import { useUserIdle } from '../hooks/useUserIdle'
import {
  DEV_ONLY_PRIMARY_NAV_IDS,
  setAppProfile,
  useAppProfile,
} from '../features/office/appProfile'
import { IconBriefcase, IconCode } from '@tabler/icons-react'

const SIDEBAR_PRIMARY_NAV_CONFIG_KEY = 'sidebar-primary-nav'
const SIDEBAR_PRIMARY_NAV_STORAGE_KEY = 'astrorder:sidebar-primary-nav-order'
const PRIMARY_NAV_DRAG_MIME = 'application/x-astrorder-primary-nav'

const navItems = [
  { to: '/chat', label: '会话', icon: ChatCircleDotsIcon, id: 'chat' },
  { to: '/bots', label: 'Bot', icon: RobotIcon, id: 'bots' },
  { to: '/inbox', label: '待办', icon: TrayIcon, id: 'inbox' },
  { to: '/monitor', label: '矩阵', icon: SquaresFourIcon, id: 'monitor' },
  { to: '/groups', label: '群聊', icon: UsersThreeIcon, id: 'groups' },
  { to: '/analytics', label: '统计', icon: ChartLineUpIcon, id: 'analytics' },
  { to: '/backups', label: '备份', icon: HardDrivesIcon, id: 'backups' },
  { to: '/agent-accounts', label: '账号', icon: IdentificationCardIcon, id: 'agentAccounts' },
  { to: '/digest', label: '复盘', icon: NotebookIcon, id: 'digest' },
  { to: '/meetings', label: '会议', icon: MicrophoneIcon, id: 'meetings' },
]

function readStoredPrimaryNavOrder(): string[] {
  if (typeof localStorage === 'undefined') return []
  try {
    const raw = localStorage.getItem(SIDEBAR_PRIMARY_NAV_STORAGE_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw)
    return Array.isArray(parsed) ? parsed.filter((x): x is string => typeof x === 'string') : []
  } catch {
    return []
  }
}

function writeStoredPrimaryNavOrder(order: string[]) {
  if (typeof localStorage === 'undefined') return
  try {
    localStorage.setItem(SIDEBAR_PRIMARY_NAV_STORAGE_KEY, JSON.stringify(order))
  } catch {}
}

export function normalizePrimaryNavOrder(savedOrder: string[] | undefined): string[] {
  const defaultIds = navItems.map((item) => item.id)
  if (!Array.isArray(savedOrder) || savedOrder.length === 0) return defaultIds
  const valid = new Set(defaultIds)
  const seen = new Set<string>()
  const result: string[] = []
  for (const id of savedOrder) {
    if (valid.has(id) && !seen.has(id)) {
      seen.add(id)
      result.push(id)
    }
  }
  for (const id of defaultIds) {
    if (!seen.has(id)) {
      result.push(id)
    }
  }
  return result
}

function TodayUsagePreview({ data, pending, error, updatedAt }: {
  data?: OcxUsageResponse
  pending: boolean
  error: boolean
  updatedAt: number
}) {
  const now = new Date()
  const today = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
  const day = data?.range === 'today' ? { requests: Number(data.summary.requests || 0), totalTokens: Number(data.summary.totalTokens || 0), models: data.models } : data?.days.find(row => row.date === today)
  const byModel = new Map<string, { model: string; totalTokens: number; providers: Set<string> }>()
  for (const row of day?.models || []) {
    if (!Number.isFinite(row.totalTokens)) continue
    const model = row.model || 'unknown'
    const entry = byModel.get(model) || { model, totalTokens: 0, providers: new Set<string>() }
    entry.totalTokens += row.totalTokens
    if (row.provider) entry.providers.add(row.provider)
    byModel.set(model, entry)
  }
  const models = [...byModel.values()].sort((a, b) => b.totalTokens - a.totalTokens)
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
              <div className="sidebar-usage-model" key={row.model}>
                <span className="sidebar-usage-model-name" title={`${row.model} · ${[...row.providers].join('、')}`}>
                  <i style={{ background: getModelColor(row.model) }} />
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
    const excludedValues: string[] = JSON.parse(localStorage.getItem('astrorder:monitor-excluded-sessions') || '[]')
    const wasExcluded = excludedValues.includes(sessionKey)
    if (!current.includes(sessionKey) || wasExcluded) {
      const next = current.includes(sessionKey) ? current : [...current, sessionKey]
      localStorage.setItem('astrorder:monitor-sessions', JSON.stringify(next))
      for (const storageKey of ['astrorder:monitor-auto-sessions', 'astrorder:monitor-excluded-sessions']) {
        const values: string[] = JSON.parse(localStorage.getItem(storageKey) || '[]')
        localStorage.setItem(storageKey, JSON.stringify(values.filter(key => key !== sessionKey)))
      }
      window.dispatchEvent(new CustomEvent('astrorder:monitor-sessions-changed', { detail: next }))
      notifications.show({
        color: 'teal',
        message: `已将“${title || '会话'}”加入矩阵`,
      })
    } else {
      notifications.show({
        color: 'blue',
        message: `“${title || '会话'}”已在矩阵中`,
      })
    }
  } catch {}
}

export function Sidebar({
  sessions,
  agents,
  projects = [],
  activeSessionKey,
  chatReturnPath = '/chat',
  onSelectSession,
  onNavigate,
  panelCollapsed = false,
  isPeeking = false,
  onTogglePanelCollapse,
  onPeekEnter,
  onPeekLeave,
  onDismissPeek,
}: {
  sessions: Session[]
  agents: Record<string, Agent>
  projects?: Project[]
  activeSessionKey?: string
  chatReturnPath?: string
  onSelectSession: (session: Session) => void
  onNavigate?: () => void
  panelCollapsed?: boolean
  isPeeking?: boolean
  onTogglePanelCollapse?: () => void
  onPeekEnter?: () => void
  onPeekLeave?: () => void
  onDismissPeek?: () => void
}) {
  const location = useLocation()
  const navigate = useNavigate()
  const skyId = useId()
  const [settingsOpened, setSettingsOpened] = useState(false)
  const [settingsTab, setSettingsTab] = useState<string>('general')
  const [addIntegrationOpened, setAddIntegrationOpened] = useState(false)
  const [draggingIntegrationId, setDraggingIntegrationId] = useState<string | null>(null)
  const [dragOverIntegration, setDragOverIntegration] = useState<{ id: string; pos: 'before' | 'after' } | null>(null)
  const [draggingNavId, setDraggingNavId] = useState<string | null>(null)
  const [dragOverNav, setDragOverNav] = useState<{ id: string; pos: 'before' | 'after' } | null>(null)
  const [localNavOrder, setLocalNavOrder] = useState<string[]>(() => readStoredPrimaryNavOrder())
  const configuredNavOrder = usePluginSettingsStore((s) => s.pluginConfigs[SIDEBAR_PRIMARY_NAV_CONFIG_KEY]?.order)
  const setPluginConfig = usePluginSettingsStore((s) => s.setPluginConfig)
  const integrationsPluginEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(SIDEBAR_INTEGRATIONS_PLUGIN_ID))
  const groupsPluginEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(WORKSPACE_GROUPS_PLUGIN_ID))
  const monitorPluginEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(WORKSPACE_MONITOR_PLUGIN_ID))
  const analyticsGatewayEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(WORKSPACE_ANALYTICS_GATEWAY_PLUGIN_ID))
  const backupsPluginEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(WORKSPACE_BACKUPS_PLUGIN_ID))
  const agentAccountsEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(WORKSPACE_AGENT_ACCOUNTS_PLUGIN_ID))
  const digestPluginEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(WORKSPACE_DIGEST_PLUGIN_ID))
  const meetingsPluginEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(WORKSPACE_MEETINGS_PLUGIN_ID))
  const appProfile = useAppProfile()
  const isOfficeProfile = appProfile === 'office'

  const orderedNavIds = normalizePrimaryNavOrder(
    Array.isArray(configuredNavOrder) && configuredNavOrder.length > 0
      ? (configuredNavOrder.filter((x): x is string => typeof x === 'string'))
      : localNavOrder,
  )
  const navById = new Map(navItems.map((item) => [item.id, item]))
  const visibleNavItems = orderedNavIds
    .map((id) => navById.get(id))
    .filter((item): item is (typeof navItems)[number] => Boolean(item))
    .filter((item) => {
      if (isOfficeProfile && DEV_ONLY_PRIMARY_NAV_IDS.has(item.id)) return false
      if (item.id === 'digest') return digestPluginEnabled
      if (item.id === 'meetings') return meetingsPluginEnabled
      if (item.id === 'groups') return groupsPluginEnabled
      if (item.id === 'monitor') return monitorPluginEnabled
      if (item.id === 'analytics') return analyticsGatewayEnabled
      if (item.id === 'backups') return backupsPluginEnabled
      if (item.id === 'agentAccounts') return agentAccountsEnabled
      return true
    })

  const handlePrimaryNavDrop = (sourceId: string, targetId: string, pos: 'before' | 'after') => {
    setDraggingNavId(null)
    setDragOverNav(null)
    if (!sourceId || !targetId || sourceId === targetId) return
    const nextIds = [...orderedNavIds]
    const fromIdx = nextIds.indexOf(sourceId)
    if (fromIdx < 0) return
    nextIds.splice(fromIdx, 1)
    const targetIdx = nextIds.indexOf(targetId)
    if (targetIdx < 0) return
    nextIds.splice(pos === 'after' ? targetIdx + 1 : targetIdx, 0, sourceId)
    setLocalNavOrder(nextIds)
    writeStoredPrimaryNavOrder(nextIds)
    setPluginConfig(SIDEBAR_PRIMARY_NAV_CONFIG_KEY, 'order', nextIds)
  }
  const integrationItems = useIntegrationsStore((s) => s.items)
  const reorderIntegrations = useIntegrationsStore((s) => s.reorder)
  const visibleIntegrations = integrationItems.filter((item) => item.visible)

  const handleIntegrationDrop = async (sourceId: string, targetId: string, pos: 'before' | 'after') => {
    setDraggingIntegrationId(null)
    setDragOverIntegration(null)
    if (!sourceId || !targetId || sourceId === targetId) return
    const nextIds = integrationItems.map((entry) => entry.id)
    const fromIdx = nextIds.indexOf(sourceId)
    if (fromIdx < 0) return
    nextIds.splice(fromIdx, 1)
    const targetIdx = nextIds.indexOf(targetId)
    if (targetIdx < 0) return
    nextIds.splice(pos === 'after' ? targetIdx + 1 : targetIdx, 0, sourceId)
    try {
      await reorderIntegrations(nextIds)
    } catch (cause) {
      notifications.show({ color: 'red', message: cause instanceof Error ? cause.message : '保存集成排序失败' })
    }
  }

  const openSettings = useCallback((tab?: string) => {
    const desktopOpenSettings = (
      window as unknown as {
        astrorderDesktop?: { openSettings?: (tab?: string) => Promise<void> }
      }
    ).astrorderDesktop?.openSettings
    if (desktopOpenSettings) {
      void desktopOpenSettings(tab || '')
      return
    }
    if (tab) {
      setSettingsTab(normalizeSettingsTab(tab))
    }
    setSettingsOpened(true)
  }, [])

  useShortcutHandlers(onNavigate ? {} : { settings: () => openSettings() }, true)
  useEffect(() => {
    if (onNavigate) return
    const onOpenSettings = (event: Event) => {
      const detail = (event as CustomEvent<{ tab?: string }>).detail
      openSettings(detail?.tab)
    }
    const onCloseSettings = () => setSettingsOpened(false)
    const onNavigateRoute = (event: Event) => {
      const detail = (event as CustomEvent<{ to?: string }>).detail
      if (detail?.to) {
        setSettingsOpened(false)
        navigate(detail.to)
      }
    }
    window.addEventListener('astrorder:open-settings', onOpenSettings)
    window.addEventListener('astrorder:close-settings', onCloseSettings)
    window.addEventListener('astrorder:navigate', onNavigateRoute)
    return () => {
      window.removeEventListener('astrorder:open-settings', onOpenSettings)
      window.removeEventListener('astrorder:close-settings', onCloseSettings)
      window.removeEventListener('astrorder:navigate', onNavigateRoute)
    }
  }, [navigate, onNavigate, openSettings])
  const [isMonitorDragOver, setIsMonitorDragOver] = useState(false)
  const userIdle = useUserIdle()
  const todayUsage = useQuery({
    queryKey: ['astrorder', 'analytics', 'today-models'],
    queryFn: () => api.getAnalyticsUsage({ range: 'today', surface: 'all' }),
    enabled: !onNavigate && analyticsGatewayEnabled && !isOfficeProfile,
    staleTime: 180_000,
    refetchInterval: userIdle ? false : 180_000,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: false,
    retry: false,
  })

  const isBots = location.pathname.startsWith('/bots')
  const isInbox = location.pathname.startsWith('/inbox')
  const isDigest = location.pathname.startsWith('/digest')
  const isMeetings = location.pathname.startsWith('/meetings')
  const isGroups = location.pathname.startsWith('/groups') || location.pathname.startsWith('/chat/group-')
  const isMonitorRoute = location.pathname.startsWith('/monitor')
  const isAnalytics = location.pathname.startsWith('/analytics')
  const isBackups = location.pathname.startsWith('/backups')
  const isAgentAccounts = location.pathname.startsWith('/agent-accounts')
  const isIntegrations = location.pathname.startsWith('/integrations/')
  const isChat = !isBots && !isInbox && !isDigest && !isMeetings && !isGroups && !isMonitorRoute && !isAnalytics && !isBackups && !isAgentAccounts && !isIntegrations
  const isInChatSession = isChat && (
    (location.pathname.startsWith('/chat/') && !location.pathname.startsWith('/chat/group-')) ||
    Boolean(activeSessionKey)
  )
  const { totalCount: inboxTotalCount, urgentCount: inboxUrgentCount } = useActionInbox(activeSessionKey)
  const meetingRecording = useMeetingSessionStore(
    (s) => s.recordState === 'recording' || s.recordState === 'paused' || s.recordState === 'starting',
  )
  const groupsBusy = useAstrorderStore(state => Object.values(state.botGroups).some(groupHasActiveTask))
  const connection = useAstrorderStore(state => state.connection)
  const activeSession = sessions.find(session => scopeKey(session.agent_id, session.id) === activeSessionKey)
  const activeAgent = activeSession ? agents[activeSession.agent_id] : undefined
  const accountTarget = sessionAccountTarget(activeSession, activeAgent)
  const connectionLabel = connection === 'connected' ? '已连接' : connection === 'connecting' ? '连接中' : connection === 'error' ? '连接异常' : '离线'

  const hasSecondaryPanel = !(isBots || isInbox || isDigest || isMeetings || isAgentAccounts || isBackups || isAnalytics || isIntegrations)
  const isCollapsedDesktop = Boolean(!onNavigate && hasSecondaryPanel && panelCollapsed)
  const isPeekingDesktop = Boolean(isCollapsedDesktop && isPeeking)

  const openExternalIntegration = async (item: IntegrationItem, event?: React.MouseEvent) => {
    const desktop = (window as unknown as { astrorderDesktop?: { openExternal?: (address: string) => Promise<void> } }).astrorderDesktop
    if (desktop?.openExternal) {
      event?.preventDefault()
      try {
        await desktop.openExternal(item.url)
      } catch (cause) {
        notifications.show({ color: 'red', message: cause instanceof Error ? cause.message : '打开外链失败' })
      }
      return
    }
    if (!event) {
      window.open(item.url, '_blank', 'noopener,noreferrer')
    }
  }

  return (
    <>
      <div
        className={`workspace-sidebar${isCollapsedDesktop ? ' is-panel-collapsed' : ''}${isPeekingDesktop ? ' is-panel-peeking' : ''}`}
      >
        <div id={skyId} className="workspace-sidebar-sky" aria-hidden="true" />
        <nav className="workspace-navigation" aria-label="主导航">
          <div className="sidebar-primary-nav">
            {visibleNavItems.map(({ to, label, icon: Icon, id }) => {
              const isThisMonitor = id === 'monitor'
              const isActive =
                (id === 'bots' && isBots) ||
                (id === 'inbox' && isInbox) ||
                (id === 'digest' && isDigest) ||
                (id === 'meetings' && isMeetings) ||
                (id === 'groups' && isGroups) ||
                (id === 'monitor' && isMonitorRoute) ||
                (id === 'analytics' && isAnalytics) ||
                (id === 'backups' && isBackups) ||
                (id === 'agentAccounts' && isAgentAccounts) ||
                (id === 'chat' && isChat)
              const canHoverPeekTab =
                isCollapsedDesktop &&
                ((id === 'chat' && (isChat || isMonitorRoute)) ||
                  (id === 'groups' && isGroups))
              const isNavDragging = draggingNavId === id
              const navDropPos = dragOverNav?.id === id ? dragOverNav.pos : undefined
              const link = (
                <NavLink
                    component={RouterNavLink}
                    to={id === 'agentAccounts' && accountTarget ? `${accountPlatformMeta(accountTarget.platform).routePath}?${new URLSearchParams({ environment: accountTarget.environment })}` :
                      id === 'chat' ? (isInChatSession ? '/chat' : chatReturnPath) : to}
                    state={id === 'chat' && isInChatSession ? { welcome: true } : undefined}
                    label={label}
                    leftSection={<Icon size={25} weight={isActive ? 'duotone' : 'regular'} aria-hidden="true" />}
                    onClick={onNavigate}
                    onMouseEnter={canHoverPeekTab ? onPeekEnter : undefined}
                    data-peek-trigger={canHoverPeekTab ? 'true' : undefined}
                    data-active={isActive ? 'true' : undefined}
                    data-dragging={isNavDragging ? 'true' : undefined}
                    data-drop-pos={navDropPos}
                    draggable
                    aria-current={isActive ? 'page' : undefined}
                    aria-label={id === 'groups' && groupsBusy && !isGroups ? '群聊，有任务进行中' : label}
                    className={`sidebar-nav-link ${isActive ? 'active' : ''} ${isThisMonitor && isMonitorDragOver ? 'is-drag-over' : ''}`}
                    onDragStart={(e) => {
                      setDraggingNavId(id)
                      e.dataTransfer.effectAllowed = 'move'
                      e.dataTransfer.setData(PRIMARY_NAV_DRAG_MIME, id)
                    }}
                    onDragOver={(e) => {
                      const isPrimaryNavDrag = Boolean(draggingNavId) || e.dataTransfer.types.includes(PRIMARY_NAV_DRAG_MIME)
                      if (isPrimaryNavDrag) {
                        if (draggingNavId === id) return
                        e.preventDefault()
                        e.dataTransfer.dropEffect = 'move'
                        const rect = e.currentTarget.getBoundingClientRect()
                        const pos: 'before' | 'after' = e.clientY > rect.top + rect.height / 2 ? 'after' : 'before'
                        setDragOverNav((prev) => (prev?.id === id && prev.pos === pos ? prev : { id, pos }))
                        return
                      }
                      if (isThisMonitor && !e.dataTransfer.types.includes('application/x-astrorder-integration')) {
                        e.preventDefault()
                        e.dataTransfer.dropEffect = 'copy'
                        setIsMonitorDragOver(true)
                      }
                    }}
                    onDragLeave={(e) => {
                      if (!e.currentTarget.contains(e.relatedTarget as Node | null)) {
                        setDragOverNav((prev) => (prev?.id === id ? null : prev))
                      }
                      if (isThisMonitor) {
                        setIsMonitorDragOver(false)
                      }
                    }}
                    onDrop={(e) => {
                      const sourceNavId = e.dataTransfer.getData(PRIMARY_NAV_DRAG_MIME) || draggingNavId
                      if (sourceNavId) {
                        e.preventDefault()
                        const rect = e.currentTarget.getBoundingClientRect()
                        const pos: 'before' | 'after' = e.clientY > rect.top + rect.height / 2 ? 'after' : 'before'
                        handlePrimaryNavDrop(sourceNavId, id, pos)
                        return
                      }
                      if (isThisMonitor) {
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
                      }
                    }}
                    onDragEnd={() => {
                      setDraggingNavId(null)
                      setDragOverNav(null)
                    }}
                />
              )
              return id === 'inbox' && inboxTotalCount > 0 ? (
                <div key={to} className="sidebar-inbox-nav-wrap">
                  <Tooltip label={`待办收件箱（${inboxTotalCount} 项待处理）`} position="right" withArrow openDelay={200}>{link}</Tooltip>
                  <span className={`sidebar-inbox-badge${inboxUrgentCount > 0 ? ' is-urgent' : ''}`} aria-hidden="true">
                    {inboxTotalCount > 99 ? '99+' : inboxTotalCount}
                  </span>
                </div>
              ) : id === 'meetings' && meetingRecording ? (
                <div key={to} className="sidebar-inbox-nav-wrap">
                  <Tooltip label="会议（实时录制中）" position="right" withArrow openDelay={200}>{link}</Tooltip>
                  <span className="sidebar-meeting-rec-dot" aria-hidden="true" />
                </div>
              ) : id === 'groups' && groupsBusy && !isGroups ? (
                <div key={to} className="group-task-nav-tab">
                  <Tooltip label="群聊有任务进行中" position="right" withArrow openDelay={200}>{link}</Tooltip>
                  <GroupTaskIndicator />
                </div>
              ) : id === 'analytics' ? (
                <HoverCard key={to} width={280} position="right-start" withArrow openDelay={150} closeDelay={180} transitionProps={{ duration: 0 }}>
                  <HoverCard.Target>{link}</HoverCard.Target>
                  <HoverCard.Dropdown>
                    <TodayUsagePreview data={todayUsage.data} pending={todayUsage.isPending} error={todayUsage.isError} updatedAt={todayUsage.dataUpdatedAt} />
                  </HoverCard.Dropdown>
                </HoverCard>
              ) : id === 'agentAccounts' ? (
                <SessionAccountsHoverCard key={to} link={link} session={activeSession} agent={activeAgent} />
              ) : (
                <Tooltip key={to} label={id === 'chat' && isInChatSession ? '回到欢迎页' : label} position="right" withArrow openDelay={200}>{link}</Tooltip>
              )
            })}
          </div>
          {integrationsPluginEnabled && (
            <>
              <div className="sidebar-integrations-divider" aria-hidden="true" />
              <div className="sidebar-integrations-nav" aria-label={isOfficeProfile ? '常用网站' : '外链集成'}>
                {visibleIntegrations.map((item) => {
                  const targetPath = `/integrations/${encodeURIComponent(item.id)}`
                  const isItemActive = location.pathname === targetPath
                  const trimmedName = item.name.trim()
                  const fallbackLabel = (() => {
                    try { return new URL(item.url).hostname || item.url } catch { return item.url }
                  })()
                  const tooltipLabel = trimmedName || fallbackLabel
                  const iconOnly = !trimmedName
                  const isDragging = draggingIntegrationId === item.id
                  const dropPos = dragOverIntegration?.id === item.id ? dragOverIntegration.pos : undefined
                  const iconNode = (
                    <IntegrationIcon
                      name={trimmedName}
                      url={item.url}
                      iconType={item.icon_type}
                      iconValue={item.icon_value}
                      size={iconOnly ? 24 : 21}
                    />
                  )
                  const dragProps = {
                    draggable: true,
                    'data-dragging': isDragging ? 'true' : undefined,
                    'data-drop-pos': dropPos,
                    onDragStart: (e: React.DragEvent) => {
                      setDraggingIntegrationId(item.id)
                      e.dataTransfer.effectAllowed = 'move'
                      e.dataTransfer.setData('application/x-astrorder-integration', item.id)
                    },
                    onDragOver: (e: React.DragEvent) => {
                      const activeSource = draggingIntegrationId || (e.dataTransfer.types.includes('application/x-astrorder-integration') ? '__pending__' : null)
                      if (!activeSource || activeSource === item.id) return
                      e.preventDefault()
                      e.dataTransfer.dropEffect = 'move'
                      const rect = e.currentTarget.getBoundingClientRect()
                      const pos: 'before' | 'after' = e.clientY > rect.top + rect.height / 2 ? 'after' : 'before'
                      setDragOverIntegration((prev) => (prev?.id === item.id && prev.pos === pos ? prev : { id: item.id, pos }))
                    },
                    onDragLeave: (e: React.DragEvent) => {
                      if (!e.currentTarget.contains(e.relatedTarget as Node | null)) {
                        setDragOverIntegration((prev) => (prev?.id === item.id ? null : prev))
                      }
                    },
                    onDrop: (e: React.DragEvent) => {
                      const sourceId = e.dataTransfer.getData('application/x-astrorder-integration') || draggingIntegrationId
                      if (!sourceId) return
                      e.preventDefault()
                      const rect = e.currentTarget.getBoundingClientRect()
                      const pos: 'before' | 'after' = e.clientY > rect.top + rect.height / 2 ? 'after' : 'before'
                      void handleIntegrationDrop(sourceId, item.id, pos)
                    },
                    onDragEnd: () => {
                      setDraggingIntegrationId(null)
                      setDragOverIntegration(null)
                    },
                  }
                  const navNode = item.open_mode === 'external' ? (
                    <NavLink
                      component="a"
                      href={item.url}
                      target="_blank"
                      rel="noopener noreferrer"
                      label={trimmedName || undefined}
                      leftSection={iconNode}
                      onClick={(event) => {
                        void openExternalIntegration(item, event)
                        onNavigate?.()
                      }}
                      aria-label={tooltipLabel}
                      className={`sidebar-nav-link sidebar-integration-link${iconOnly ? ' is-icon-only' : ''}`}
                      {...dragProps}
                    />
                  ) : (
                    <NavLink
                      component={RouterNavLink}
                      to={targetPath}
                      label={trimmedName || undefined}
                      leftSection={iconNode}
                      onClick={onNavigate}
                      data-active={isItemActive ? 'true' : undefined}
                      aria-current={isItemActive ? 'page' : undefined}
                      aria-label={tooltipLabel}
                      className={`sidebar-nav-link sidebar-integration-link${isItemActive ? ' active' : ''}${iconOnly ? ' is-icon-only' : ''}`}
                      {...dragProps}
                    />
                  )
                  return (
                    <Tooltip key={item.id} label={tooltipLabel} position="right" withArrow openDelay={200} disabled={Boolean(draggingIntegrationId)}>
                      {navNode}
                    </Tooltip>
                  )
                })}
                <Tooltip label={isOfficeProfile ? '添加常用网站' : '添加集成外链'} position="right" withArrow openDelay={150}>
                  <button
                    type="button"
                    className="sidebar-integration-add-btn"
                    aria-label={isOfficeProfile ? '添加常用网站' : '添加集成'}
                    onClick={() => setAddIntegrationOpened(true)}
                  >
                    <IconPlus size={18} stroke={2} aria-hidden="true" />
                  </button>
                </Tooltip>
              </div>
            </>
          )}
          <div className="workspace-navigation-tools">
            {!onNavigate && hasSecondaryPanel && onTogglePanelCollapse && (
              <div className="workspace-tool-item sidebar-collapse-tool">
                <Tooltip
                  label={panelCollapsed ? '展开并固定侧栏 (Ctrl+B)' : '收起侧栏 (Ctrl+B)'}
                  position="right"
                >
                  <ActionIcon
                    variant="subtle"
                    aria-label={panelCollapsed ? '展开侧边栏' : '收起侧边栏'}
                    aria-expanded={!panelCollapsed}
                    data-collapsed={panelCollapsed ? 'true' : undefined}
                    onClick={onTogglePanelCollapse}
                  >
                    {panelCollapsed ? (
                      <IconLayoutSidebarLeftExpand size={20} aria-hidden="true" />
                    ) : (
                      <IconLayoutSidebarLeftCollapse size={20} aria-hidden="true" />
                    )}
                  </ActionIcon>
                </Tooltip>
                <span aria-hidden="true">{panelCollapsed ? '展开' : '收起'}</span>
              </div>
            )}
            <div className="workspace-tool-item"><NotificationPermissionControl /><span aria-hidden="true">通知</span></div>
            <div className="workspace-tool-item"><WorkspaceThemeToggle /><span aria-hidden="true">主题</span></div>
            <div className="workspace-tool-item">
            <Tooltip label="设置" position="right">
              <ActionIcon variant="subtle" aria-label="打开设置" onClick={() => openSettings()}>
                <GearSixIcon size={24} aria-hidden="true" />
              </ActionIcon>
            </Tooltip>
            <span aria-hidden="true">设置</span>
            </div>
          </div>
        </nav>
        {hasSecondaryPanel && (
          <div
            className="workspace-sidebar-panel"
            aria-hidden={isCollapsedDesktop && !isPeekingDesktop ? true : undefined}
            inert={isCollapsedDesktop && !isPeekingDesktop ? true : undefined}
            onMouseEnter={isCollapsedDesktop ? onPeekEnter : undefined}
            onMouseLeave={isCollapsedDesktop ? onPeekLeave : undefined}
          >
            {isPeekingDesktop && onTogglePanelCollapse && (
              <div className="sidebar-peek-header">
                <Text size="xs" fw={650} c="dimmed">
                  {isGroups ? '群聊协作' : isOfficeProfile ? '工作空间' : '项目会话'}
                </Text>
                <Tooltip label="固定侧栏 (Ctrl+B)" position="right" withArrow>
                  <button
                    type="button"
                    className="sidebar-peek-pin-btn"
                    aria-label="固定侧栏"
                    onClick={onTogglePanelCollapse}
                  >
                    <IconPin size={14} aria-hidden="true" />
                  </button>
                </Tooltip>
              </div>
            )}
            <Stack className="sidebar-content" gap="md">
        {isGroups ? (
          <BotGroupRail
            agents={agents}
            onNavigate={() => {
              onNavigate?.()
              onDismissPeek?.()
            }}
          />
        ) : (
          <>
            <div className="rail-heading">
              <Text size="xs" fw={700} c="dimmed">{isOfficeProfile ? '工作空间' : '项目会话'}</Text>
              <div className="rail-heading-actions">
                <Text size="xs" c="dimmed">{sessions.length}</Text>
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
                onDismissPeek?.()
              }}
            />
          </>
        )}
      </Stack>
            <footer className="workspace-connection" data-status={connection} style={{ justifyContent: 'space-between' }}>
              <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                <span className="workspace-connection-dot" aria-hidden="true" />
                <span>{isOfficeProfile ? (connection === 'connected' ? '服务就绪' : connectionLabel) : `${activeAgent ? `${connectionName(activeAgent)} · ` : ''}${connectionLabel}`}</span>
              </span>
              <Tooltip label={isOfficeProfile ? '当前为办公模式，点击切换为研发模式' : '当前为研发模式，点击切换为办公模式'} position="top" withArrow>
                <button
                  type="button"
                  onClick={() => {
                    const next = isOfficeProfile ? 'developer' : 'office'
                    setAppProfile(next)
                    notifications.show({
                      color: 'teal',
                      message: next === 'office' ? '已切换至办公模式' : '已切换至研发模式',
                    })
                  }}
                  style={{
                    display: 'inline-flex',
                    alignItems: 'center',
                    gap: 4,
                    padding: '2px 7px',
                    borderRadius: 999,
                    border: '1px solid var(--astr-border)',
                    background: 'transparent',
                    color: 'var(--astr-muted)',
                    fontSize: 11,
                    fontWeight: 550,
                    cursor: 'pointer',
                    flexShrink: 0,
                  }}
                >
                  {isOfficeProfile ? <IconBriefcase size={11} /> : <IconCode size={11} />}
                  <span>{isOfficeProfile ? '办公' : '研发'}</span>
                </button>
              </Tooltip>
            </footer>
          </div>
        )}
      </div>
      <SettingsDrawerScope>{hasOpenDrawer => <Modal
        className="settings-modal"
        opened={settingsOpened}
        closeOnEscape={!hasOpenDrawer}
        onClose={() => setSettingsOpened(false)}
        title="设置"
        centered
        size="80%"
        styles={{
          content: { width: '80vw', maxWidth: '1600px', height: '80vh', maxHeight: '1000px', display: 'flex', flexDirection: 'column', overflow: 'hidden' },
          body: { flex: 1, overflow: 'hidden', display: 'flex', flexDirection: 'column', padding: 0 },
        }}
      >
        {settingsOpened && (
          <SettingsWorkbench tab={settingsTab} onTabChange={setSettingsTab} />
        )}
      </Modal>}</SettingsDrawerScope>
      <IntegrationFormModal
        opened={addIntegrationOpened}
        onClose={() => setAddIntegrationOpened(false)}
        onSaved={(saved) => {
          if (saved.visible && saved.open_mode === 'embed') {
            navigate(`/integrations/${encodeURIComponent(saved.id)}`)
            onNavigate?.()
          }
        }}
      />
    </>
  )
}
