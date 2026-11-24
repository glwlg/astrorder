import { Suspense, lazy, useEffect, type ComponentType } from 'react'
import { IconAlertTriangle, IconLoader2 } from '@tabler/icons-react'
import { Alert, Button, Center, Loader, Paper, Stack, Text, Title } from '@mantine/core'
import { Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { useMediaQuery } from '@mantine/hooks'
import { useQueryClient } from '@tanstack/react-query'
import { ApiError } from '../api/client'
import { AuthPage } from '../components/AuthPage'
import { AppShellLayout } from './AppShellLayout'
import { useAuthSession, useBootstrap } from '../hooks/useAstrorderData'
import { useServerOutboxSync } from '../hooks/useServerOutboxSync'
import { useChatReturnPath } from '../hooks/useChatReturnPath'
import { useEventStream } from '../hooks/useEventStream'
import { ChatPage } from '../features/chat/ChatPage'
import { accountPlatforms } from '../features/agentAccounts/accountPlatforms'
import { AstrorderLoader } from '../components/AnimatedStatus'
import { ShinyText } from '../components/animations/ShinyText'
import { useShortcutHandlers } from '../features/shortcuts/shortcuts'
import {
  SIDEBAR_INTEGRATIONS_PLUGIN_ID,
  WORKSPACE_AGENT_ACCOUNTS_PLUGIN_ID,
  WORKSPACE_ANALYTICS_GATEWAY_PLUGIN_ID,
  WORKSPACE_BACKUPS_PLUGIN_ID,
  WORKSPACE_GROUPS_PLUGIN_ID,
  WORKSPACE_MONITOR_PLUGIN_ID,
} from '../features/plugins/builtinPlugins'
import { usePluginSettingsStore } from '../features/plugins/pluginSettingsStore'
import { clientPerf, PerfProfiler } from '../perf/clientPerf'

function lazyNamed<T extends ComponentType<any>>(
  loader: () => Promise<Record<string, unknown>>,
  name: string,
) {
  return lazy(() => loader().then((mod) => ({ default: mod[name] as T })))
}

const MobileWorkspace = lazyNamed(() => import('../features/mobile/MobileWorkspace'), 'MobileWorkspace')
const GroupsPage = lazyNamed(() => import('../features/chat/GroupsPage'), 'GroupsPage')
const ActionInboxPage = lazyNamed(() => import('../features/inbox/ActionInboxPage'), 'ActionInboxPage')
const MonitorPage = lazyNamed(() => import('../features/monitor/MonitorPage'), 'MonitorPage')
const PluginsPage = lazyNamed(() => import('../features/plugins/PluginsPage'), 'PluginsPage')
const AnalyticsPage = lazyNamed(() => import('../features/analytics/AnalyticsPage'), 'AnalyticsPage')
const BackupsPage = lazyNamed(() => import('../features/backups/BackupsPage'), 'BackupsPage')
const IntegrationPage = lazyNamed(() => import('../features/integrations/IntegrationPage'), 'IntegrationPage')
const AgentAccountsLayout = lazyNamed(() => import('../features/agentAccounts/AgentAccountsLayout'), 'AgentAccountsLayout')
const UnavailablePlatformPage = lazyNamed(() => import('../features/agentAccounts/UnavailablePlatformPage'), 'UnavailablePlatformPage')
const QrAuthorizePage = lazyNamed(() => import('../features/auth/QrAuthorizePage'), 'QrAuthorizePage')
const QrScanPage = lazyNamed(() => import('../features/auth/QrScanPage'), 'QrScanPage')
const StandaloneSettingsPage = lazyNamed(() => import('../features/settings/SettingsWorkbench'), 'StandaloneSettingsPage')

function RouteSuspenseFallback() {
  return (
    <Center style={{ height: '100%', minHeight: 240 }}>
      <Loader size="sm" color="violet" />
    </Center>
  )
}

function LoadingPage({ label = '正在启动星序工作台…' }: { label?: string }) {
  return (
    <div className="astrorder-boot-splash" role="status" aria-live="polite" aria-label={label}>
      <div className="splash-orbit-container">
        <div className="splash-orbit-halo" />
        <div className="splash-loader-wrap">
          <AstrorderLoader size={54} />
        </div>
      </div>
      <h1 className="splash-title">星序 · Astrorder</h1>
      <p className="splash-subtitle">群星各有所长，协作自有秩序</p>
      <div className="splash-progress-track">
        <div className="splash-progress-bar" />
      </div>
      <div className="splash-status-text">
        <ShinyText text={label} speed={1.8} />
      </div>
    </div>
  )
}

function BootstrapError({ error, retry }: { error: unknown; retry: () => void }) {
  const detail = error instanceof ApiError ? error.detail : error instanceof Error ? error.message : '服务端未返回工作台数据。'
  return (
    <Center className="loading-page">
      <Paper withBorder radius="lg" p="xl" maw={520}>
        <Stack align="center" gap="sm">
          <IconAlertTriangle className="error-icon" size={34} />
          <Title order={2} size="h3">工作台暂不可用</Title>
          <Text ta="center" c="dimmed">{detail}</Text>
          <Text ta="center" size="sm" c="dimmed">星序不会用示例数据填充空白。请确认后端认证和 bootstrap 接口已配置。</Text>
          <Button onClick={retry} variant="light">重新读取</Button>
        </Stack>
      </Paper>
    </Center>
  )
}

const MOBILE_MEDIA_QUERY = '(max-width: 767px), (orientation: landscape) and (max-height: 500px)'

function checkIsMobileViewport(): boolean {
  if (typeof window === 'undefined') return false
  if (window.innerWidth < 768) return true
  if (window.innerHeight <= 500 && window.innerWidth > window.innerHeight) return true
  return window.matchMedia(MOBILE_MEDIA_QUERY).matches
}

function AuthenticatedApp() {
  const mobileViewport = useMediaQuery(MOBILE_MEDIA_QUERY, checkIsMobileViewport())
  const location = useLocation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const bootstrap = useBootstrap(true)
  const chatReturnPath = useChatReturnPath()
  const groupsEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(WORKSPACE_GROUPS_PLUGIN_ID))
  const monitorEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(WORKSPACE_MONITOR_PLUGIN_ID))
  const analyticsEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(WORKSPACE_ANALYTICS_GATEWAY_PLUGIN_ID))
  const backupsEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(WORKSPACE_BACKUPS_PLUGIN_ID))
  const accountsEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(WORKSPACE_AGENT_ACCOUNTS_PLUGIN_ID))
  const integrationsEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(SIDEBAR_INTEGRATIONS_PLUGIN_ID))
  const isStandaloneSettings =
    location.pathname === '/settings' || location.pathname.startsWith('/settings/')
  useShortcutHandlers(
    isStandaloneSettings
      ? {}
      : {
          chat: () => navigate(chatReturnPath),
          ...(monitorEnabled ? { monitor: () => navigate('/monitor') } : {}),
        },
  )
  useEventStream(Boolean(bootstrap.data) && !isStandaloneSettings, queryClient, navigate)
  useServerOutboxSync(Boolean(bootstrap.data) && !isStandaloneSettings)
  useEffect(() => {
    if (isStandaloneSettings) return
    const onNavigate = (event: Event) => {
      const to = (event as CustomEvent<{ to?: string }>).detail?.to
      if (typeof to === 'string' && to.startsWith('/') && !to.startsWith('//')) {
        navigate(to)
      }
    }
    window.addEventListener('astrorder:navigate', onNavigate)
    return () => window.removeEventListener('astrorder:navigate', onNavigate)
  }, [isStandaloneSettings, navigate])
  useEffect(() => {
    clientPerf.setAuthenticated(true)
    return () => clientPerf.setAuthenticated(false)
  }, [])
  useEffect(() => {
    clientPerf.setRoute(location.pathname)
  }, [location.pathname])

  if (bootstrap.isLoading) return <LoadingPage label="正在读取已认证工作台…" />
  if (bootstrap.error) return <BootstrapError error={bootstrap.error} retry={() => void bootstrap.refetch()} />

  if (isStandaloneSettings) {
    return (
      <Suspense fallback={<RouteSuspenseFallback />}>
        <StandaloneSettingsPage />
      </Suspense>
    )
  }

  const isMobilePath = location.pathname.startsWith('/mobile')
  const forceMobile = new URLSearchParams(location.search).get('view') === 'mobile'
  const isMobile = mobileViewport || forceMobile

  if (isMobile) {
    return (
      <Suspense fallback={<RouteSuspenseFallback />}>
        <PerfProfiler id="MobileWorkspace">
          <MobileWorkspace />
        </PerfProfiler>
      </Suspense>
    )
  }

  if (isMobilePath) {
    const desktopPath = location.pathname.replace(/^\/mobile/, '') || '/chat'
    return <Navigate to={`${desktopPath}${location.search}`} replace />
  }

  return (
    <Routes>
      <Route element={<AppShellLayout chatReturnPath={chatReturnPath} />}>
        <Route index element={<Navigate to="/chat" replace />} />
        <Route path="chat" element={<ChatPage />} />
        <Route path="chat/:sessionId" element={<ChatPage />} />
        <Route path="inbox" element={<Suspense fallback={<RouteSuspenseFallback />}><ActionInboxPage /></Suspense>} />
        <Route path="groups" element={groupsEnabled ? <Suspense fallback={<RouteSuspenseFallback />}><GroupsPage /></Suspense> : <Navigate to="/chat" replace />} />
        <Route path="groups/:groupId" element={groupsEnabled ? <Suspense fallback={<RouteSuspenseFallback />}><GroupsPage /></Suspense> : <Navigate to="/chat" replace />} />
        <Route path="monitor" element={monitorEnabled ? <Suspense fallback={<RouteSuspenseFallback />}><MonitorPage /></Suspense> : <Navigate to="/chat" replace />} />
        <Route path="swarm" element={<Navigate to="/chat" replace />} />
        <Route path="plugins" element={<Suspense fallback={<RouteSuspenseFallback />}><PluginsPage /></Suspense>} />
        <Route path="analytics" element={analyticsEnabled ? <Suspense fallback={<RouteSuspenseFallback />}><AnalyticsPage /></Suspense> : <Navigate to="/chat" replace />} />
        <Route path="backups" element={backupsEnabled ? <Suspense fallback={<RouteSuspenseFallback />}><BackupsPage /></Suspense> : <Navigate to="/chat" replace />} />
        <Route path="integrations/:integrationId" element={integrationsEnabled ? <Suspense fallback={<RouteSuspenseFallback />}><IntegrationPage /></Suspense> : <Navigate to="/chat" replace />} />
        <Route path="agent-accounts" element={accountsEnabled ? <Suspense fallback={<RouteSuspenseFallback />}><AgentAccountsLayout /></Suspense> : <Navigate to="/chat" replace />}>
          {accountPlatforms.map(({ kind, path, Page }) =>
            <Route key={kind} path={path} element={<Suspense fallback={<RouteSuspenseFallback />}>{Page ? <Page /> : <UnavailablePlatformPage kind={kind} />}</Suspense>} />)}
        </Route>
        <Route path="*" element={<Navigate to="/chat" replace />} />
      </Route>
    </Routes>
  )
}

export default function App() {
  const queryClient = useQueryClient()
  const auth = useAuthSession()
  const location = useLocation()
  const navigate = useNavigate()
  useShortcutHandlers({ refresh: () => window.location.reload() }, true)

  if (auth.isLoading) return <LoadingPage label="正在检查登录状态…" />

  // Handle /auth/qr and /auth/qr/scan routes after auth check, before bootstrap
  if (location.pathname === '/auth/qr') {
    return (
      <Suspense fallback={<RouteSuspenseFallback />}>
        <QrAuthorizePage
          key={location.key}
          authenticated={Boolean(auth.data?.authenticated)}
          onAuthenticated={() => {
            void queryClient.invalidateQueries({ queryKey: ['astrorder', 'auth-session'] })
          }}
          onDone={() => navigate('/chat')}
        />
      </Suspense>
    )
  }

  if (location.pathname === '/auth/qr/scan') {
    if (!auth.data?.authenticated) {
      return (
        <AuthPage
          initialError={auth.error}
          onAuthenticated={() => {
            void queryClient.invalidateQueries({ queryKey: ['astrorder', 'auth-session'] })
          }}
        />
      )
    }
    return (
      <Suspense fallback={<RouteSuspenseFallback />}>
        <QrScanPage onDone={() => navigate('/chat')} />
      </Suspense>
    )
  }

  if (auth.data?.authenticated) return <AuthenticatedApp />

  return (
    <>
      {auth.error && !auth.data && (
        <Alert className="auth-service-alert" color="yellow" variant="light" icon={<IconLoader2 size={17} />}>
          登录接口当前返回错误；仍可尝试建立会话。
        </Alert>
      )}
      <AuthPage
        initialError={auth.error}
        onAuthenticated={() => {
          void queryClient.invalidateQueries({ queryKey: ['astrorder', 'auth-session'] })
        }}
      />
    </>
  )
}
