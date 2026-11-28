import { Suspense, lazy, useEffect, useMemo, type ComponentType } from 'react'
import { IconAlertTriangle, IconLoader2, IconPuzzle } from '@tabler/icons-react'
import { Alert, Button, Center, Group, Loader, Paper, Stack, Text, Title } from '@mantine/core'
import { Navigate, Route, Routes, useLocation, useNavigate, useParams } from 'react-router-dom'
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
import { AstrorderLoader } from '../components/AnimatedStatus'
import { ShinyText } from '../components/animations/ShinyText'
import { useShortcutHandlers } from '../features/shortcuts/shortcuts'
import { SIDEBAR_INTEGRATIONS_PLUGIN_ID } from '../features/plugins/builtinPlugins'
import { usePluginSettingsStore } from '../features/plugins/pluginSettingsStore'
import { useAppProfile } from '../features/office/appProfile'
import { registerBuiltinWorkspacePages } from '../features/extensions/builtinWorkspacePages'
import { extensionRegistry, useExtensionRegistryVersion } from '../features/extensions/extensionRegistry'
import { useExtensionStore } from '../features/extensions/extensionStore'
import { PluginErrorBoundary } from '../features/extensions/PluginErrorBoundary'
import { resolveExtensionPagePath, type ExtensionPageRegistration } from '../features/extensions/types'
import { clientPerf, PerfProfiler } from '../perf/clientPerf'

registerBuiltinWorkspacePages()

function lazyNamed<T extends ComponentType<any>>(
  loader: () => Promise<Record<string, unknown>>,
  name: string,
) {
  return lazy(() => loader().then((mod) => ({ default: mod[name] as T })))
}

const MobileWorkspace = lazyNamed(() => import('../features/mobile/MobileWorkspace'), 'MobileWorkspace')
const ActionInboxPage = lazyNamed(() => import('../features/inbox/ActionInboxPage'), 'ActionInboxPage')
const PluginsPage = lazyNamed(() => import('../features/plugins/PluginsPage'), 'PluginsPage')
const BotsPage = lazyNamed(() => import('../features/bots/BotsPage'), 'BotsPage')
const IntegrationPage = lazyNamed(() => import('../features/integrations/IntegrationPage'), 'IntegrationPage')
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

function renderRegisteredPage(
  page: ExtensionPageRegistration,
  registryVersion: number,
  surface: 'desktop' | 'mobile' = 'desktop',
) {
  const PageComponent = surface === 'mobile' && page.mobileComponent ? page.mobileComponent : page.component
  return (
    <PluginErrorBoundary
      pluginId={page.pluginId}
      pluginName={page.title}
      resetKey={`${page.pluginId}:${page.id}:${registryVersion}`}
    >
      <Suspense fallback={<RouteSuspenseFallback />}>
        <PageComponent pluginId={page.pluginId} pageId={page.id} surface={surface} />
      </Suspense>
    </PluginErrorBoundary>
  )
}

function DynamicExtensionPageRoute() {
  const { pluginId = '', pageId = '' } = useParams<{ pluginId: string; pageId: string }>()
  const navigate = useNavigate()
  const registryVersion = useExtensionRegistryVersion()
  const enabledPlugins = usePluginSettingsStore((s) => s.enabledPlugins)
  const appProfile = useAppProfile()
  const extensionsHydrated = useExtensionStore((s) => s.hydrated)
  const extensionsError = useExtensionStore((s) => s.error)
  const extensionItem = useExtensionStore((s) => s.items.find((item) => item.id === pluginId))
  const esmState = useExtensionStore((s) => (pluginId ? s.esmStates[pluginId] : undefined))

  const page = useMemo(() => {
    void registryVersion
    if (!pluginId || !pageId) return undefined
    return extensionRegistry.getPageById(pluginId, pageId)
  }, [pageId, pluginId, registryVersion])

  if (page) {
    const disabled = enabledPlugins[page.pluginId] === false
    const hiddenByOffice = appProfile === 'office' && Boolean(page.hideInOfficeMode)
    if (disabled || hiddenByOffice) {
      return <Navigate to="/chat" replace />
    }
    return renderRegisteredPage(page, registryVersion, 'desktop')
  }

  const manifestPage = extensionItem?.contributes?.pages?.find((entry) => entry.id === pageId)
  const blockedByOfficeManifest = appProfile === 'office' && Boolean(manifestPage?.hide_in_office_mode)
  const isEligibleEsmPlugin = Boolean(
    extensionItem &&
    extensionItem.frontend?.mode === 'esm' &&
    extensionItem.enabled &&
    extensionItem.permissions_granted !== false &&
    !(extensionItem.pending_permissions && extensionItem.pending_permissions.length > 0) &&
    enabledPlugins[extensionItem.id] !== false &&
    !blockedByOfficeManifest,
  )
  const esmSettled = esmState?.status === 'loaded' || esmState?.status === 'error'
  const isExtPagePending =
    (!extensionsHydrated && !extensionsError) ||
    (isEligibleEsmPlugin && !esmSettled)

  if (isExtPagePending) {
    return <RouteSuspenseFallback />
  }

  return (
    <Center style={{ height: '100%', minHeight: 280, padding: 24 }}>
      <Paper withBorder radius="md" p="lg" maw={440} w="100%">
        <Stack align="center" gap="sm" ta="center">
          <IconPuzzle size={28} color="var(--astr-muted)" />
          <Title order={4} size="h5">
            扩展页面暂不可用
          </Title>
          <Text size="xs" c="dimmed">
            {esmState?.status === 'error' && esmState.error
              ? `插件「${pluginId}」加载失败：${esmState.error}`
              : `未找到插件「${pluginId}」注册的页面「${pageId}」，可能已停用或卸载。`}
          </Text>
          <Group gap="xs" mt={4}>
            <Button size="xs" variant="light" onClick={() => navigate('/chat')}>
              返回会话
            </Button>
            <Button
              size="xs"
              variant="subtle"
              onClick={() =>
                window.dispatchEvent(
                  new CustomEvent('astrorder:open-settings', {
                    detail: { tab: 'plugins', pluginId: pluginId || undefined },
                  }),
                )
              }
            >
              前往插件管理
            </Button>
          </Group>
        </Stack>
      </Paper>
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
  const detail = error instanceof ApiError ? error.detail : error instanceof Error ? error.message : '服务暂时未响应，请稍后重试。'
  return (
    <Center className="loading-page">
      <Paper withBorder radius="lg" p="xl" maw={520}>
        <Stack align="center" gap="sm">
          <IconAlertTriangle className="error-icon" size={34} />
          <Title order={2} size="h3">工作台暂不可用</Title>
          <Text ta="center" c="dimmed">{detail}</Text>
          <Text ta="center" size="sm" c="dimmed">请确认本机星序服务正在运行，然后点击下方按钮重新连接。</Text>
          <Button onClick={retry} variant="light">重新连接</Button>
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

function WorkspacePendingOrRedirect() {
  const location = useLocation()
  const extensionsHydrated = useExtensionStore((s) => s.hydrated)
  const extensionsError = useExtensionStore((s) => s.error)
  const extensionItems = useExtensionStore((s) => s.items)
  const esmStates = useExtensionStore((s) => s.esmStates)
  const enabledPlugins = usePluginSettingsStore((s) => s.enabledPlugins)
  const appProfile = useAppProfile()

  // Wait while official ESM workspace plugins hydrate on direct URL refresh
  // so refreshing `/digest`, `/backups`, `/groups/:id`, `/agent-accounts/*`, etc.
  // never bounces prematurely to `/chat`.
  if (!extensionsHydrated && !extensionsError) {
    return <RouteSuspenseFallback />
  }

  const pathname = location.pathname
  const pendingMatch = extensionItems.some((item) => {
    if (item.frontend?.mode !== 'esm' || !item.enabled || item.permissions_granted === false) {
      return false
    }
    if (enabledPlugins[item.id] === false) return false
    const pages = item.contributes?.pages || []
    const hitsPath = pages.some((p) => {
      if (appProfile === 'office' && p.hide_in_office_mode) return false
      if (!p.path) return false
      return pathname === p.path || pathname.startsWith(`${p.path}/`)
    })
    if (!hitsPath) return false
    const st = esmStates[item.id]
    return !st || st.status === 'idle' || st.status === 'loading'
  })

  if (pendingMatch) {
    return <RouteSuspenseFallback />
  }

  return <Navigate to="/chat" replace />
}

function toChildRoutePath(path: string): string {
  return path.replace(/^\/+/, '')
}

function AuthenticatedApp() {
  const mobileViewport = useMediaQuery(MOBILE_MEDIA_QUERY, checkIsMobileViewport())
  const location = useLocation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const bootstrap = useBootstrap(true)
  const chatReturnPath = useChatReturnPath()
  const registryVersion = useExtensionRegistryVersion()
  const enabledPlugins = usePluginSettingsStore((s) => s.enabledPlugins)
  const integrationsEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(SIDEBAR_INTEGRATIONS_PLUGIN_ID))
  const appProfile = useAppProfile()

  const activePages = useMemo(() => {
    void registryVersion
    return extensionRegistry.getActivePages({
      profile: appProfile,
      enabledPlugins,
    })
  }, [appProfile, enabledPlugins, registryVersion])

  const monitorPageActive = activePages.some((page) => page.id === 'monitor')
  const accountsPage = activePages.find((page) => page.id === 'agentAccounts')
  const standardPages = useMemo(
    () =>
      activePages.filter((page) => {
        if (page.id === 'agentAccounts') return false
        const resolved = resolveExtensionPagePath(page)
        return Boolean(page.path || (page.routePaths && page.routePaths.length > 0)) && !resolved.startsWith('/ext-page/')
      }),
    [activePages],
  )

  const isStandaloneSettings =
    location.pathname === '/settings' || location.pathname.startsWith('/settings/')
  useShortcutHandlers(
    isStandaloneSettings
      ? {}
      : {
          chat: () => navigate(chatReturnPath),
          ...(monitorPageActive ? { monitor: () => navigate('/monitor') } : {}),
        },
  )
  useEventStream(Boolean(bootstrap.data) && !isStandaloneSettings, queryClient, navigate)
  useServerOutboxSync(Boolean(bootstrap.data) && !isStandaloneSettings)
  useEffect(() => {
    if (!bootstrap.data) return
    void useExtensionStore.getState().hydrate()
  }, [bootstrap.data])
  const isBootstrapped = Boolean(bootstrap.data)
  useEffect(() => {
    if (!isBootstrapped) return
    window.dispatchEvent(new CustomEvent('astrorder:auth-ready'))
  }, [isBootstrapped])
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

  if (bootstrap.isLoading) return <LoadingPage label="正在加载工作台…" />
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
        <Route path="bots" element={<Suspense fallback={<RouteSuspenseFallback />}><BotsPage /></Suspense>} />
        <Route path="inbox" element={<Suspense fallback={<RouteSuspenseFallback />}><ActionInboxPage /></Suspense>} />
        <Route path="swarm" element={<Navigate to="/chat" replace />} />
        <Route path="plugins" element={<Suspense fallback={<RouteSuspenseFallback />}><PluginsPage /></Suspense>} />
        <Route
          path="integrations/:integrationId"
          element={
            integrationsEnabled ? (
              <Suspense fallback={<RouteSuspenseFallback />}><IntegrationPage /></Suspense>
            ) : (
              <Navigate to="/chat" replace />
            )
          }
        />
        {standardPages.flatMap((page) => {
          const paths = page.routePaths && page.routePaths.length > 0
            ? page.routePaths
            : [resolveExtensionPagePath(page)]
          return paths.map((routePath) => (
            <Route
              key={`${page.pluginId}:${page.id}:${routePath}`}
              path={toChildRoutePath(routePath)}
              element={renderRegisteredPage(page, registryVersion, 'desktop')}
            />
          ))
        })}
        {accountsPage && (
          <Route
            path="agent-accounts/*"
            element={renderRegisteredPage(accountsPage, registryVersion, 'desktop')}
          />
        )}
        <Route path="ext-page/:pluginId/:pageId" element={<DynamicExtensionPageRoute />} />
        <Route path="*" element={<WorkspacePendingOrRedirect />} />
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
          连接服务时出现异常，你仍可尝试重新登录。
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
