import { Suspense, lazy, type ComponentType } from 'react'
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

function lazyNamed<T extends ComponentType<any>>(
  loader: () => Promise<Record<string, unknown>>,
  name: string,
) {
  return lazy(() => loader().then((mod) => ({ default: mod[name] as T })))
}

const MobileWorkspace = lazyNamed(() => import('../features/mobile/MobileWorkspace'), 'MobileWorkspace')
const MobileGroupsPage = lazyNamed(() => import('../features/mobile/MobileGroupsPage'), 'MobileGroupsPage')
const GroupsPage = lazyNamed(() => import('../features/chat/GroupsPage'), 'GroupsPage')
const MonitorPage = lazyNamed(() => import('../features/monitor/MonitorPage'), 'MonitorPage')
const SwarmPage = lazyNamed(() => import('../features/monitor/SwarmPage'), 'SwarmPage')
const PluginsPage = lazyNamed(() => import('../features/plugins/PluginsPage'), 'PluginsPage')
const AnalyticsPage = lazyNamed(() => import('../features/analytics/AnalyticsPage'), 'AnalyticsPage')
const BackupsPage = lazyNamed(() => import('../features/backups/BackupsPage'), 'BackupsPage')
const AgentAccountsLayout = lazyNamed(() => import('../features/agentAccounts/AgentAccountsLayout'), 'AgentAccountsLayout')
const UnavailablePlatformPage = lazyNamed(() => import('../features/agentAccounts/UnavailablePlatformPage'), 'UnavailablePlatformPage')
const QrAuthorizePage = lazyNamed(() => import('../features/auth/QrAuthorizePage'), 'QrAuthorizePage')
const QrScanPage = lazyNamed(() => import('../features/auth/QrScanPage'), 'QrScanPage')

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
  useShortcutHandlers({
    chat: () => navigate(chatReturnPath),
    monitor: () => navigate('/monitor'),
    swarm: () => navigate('/swarm'),
  })
  useEventStream(Boolean(bootstrap.data), queryClient, navigate)
  useServerOutboxSync(Boolean(bootstrap.data))

  if (bootstrap.isLoading) return <LoadingPage label="正在读取已认证工作台…" />
  if (bootstrap.error) return <BootstrapError error={bootstrap.error} retry={() => void bootstrap.refetch()} />

  const isMobilePath = location.pathname.startsWith('/mobile')
  const forceMobile = new URLSearchParams(location.search).get('view') === 'mobile'
  const isMobile = mobileViewport || forceMobile

  if (isMobile) {
    return (
      <Suspense fallback={<RouteSuspenseFallback />}>
        <Routes>
          <Route path="/mobile/groups" element={<MobileGroupsPage />} />
          <Route path="/mobile/groups/:groupId" element={<MobileGroupsPage />} />
          <Route path="/groups" element={<MobileGroupsPage />} />
          <Route path="/groups/:groupId" element={<MobileGroupsPage />} />
          <Route path="*" element={<MobileWorkspace />} />
        </Routes>
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
        <Route path="groups" element={<Suspense fallback={<RouteSuspenseFallback />}><GroupsPage /></Suspense>} />
        <Route path="groups/:groupId" element={<Suspense fallback={<RouteSuspenseFallback />}><GroupsPage /></Suspense>} />
        <Route path="monitor" element={<Suspense fallback={<RouteSuspenseFallback />}><MonitorPage /></Suspense>} />
        <Route path="swarm" element={<Suspense fallback={<RouteSuspenseFallback />}><SwarmPage /></Suspense>} />
        <Route path="plugins" element={<Suspense fallback={<RouteSuspenseFallback />}><PluginsPage /></Suspense>} />
        <Route path="analytics" element={<Suspense fallback={<RouteSuspenseFallback />}><AnalyticsPage /></Suspense>} />
        <Route path="backups" element={<Suspense fallback={<RouteSuspenseFallback />}><BackupsPage /></Suspense>} />
        <Route path="agent-accounts" element={<Suspense fallback={<RouteSuspenseFallback />}><AgentAccountsLayout /></Suspense>}>
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
