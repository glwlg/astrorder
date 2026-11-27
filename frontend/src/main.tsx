import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '@mantine/core/styles.css'
import '@mantine/notifications/styles.css'
import { MantineProvider } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { DynamicIslandToastHost } from './components/DynamicIslandToastHost'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import App from './app/App.tsx'
import { bootstrapBuiltinExtensions } from './features/extensions/bootstrapBuiltins'
import { startEsmPluginLoader } from './features/extensions/esmPluginLoader'
import { extensionRegistry } from './features/extensions/extensionRegistry'
import { useExtensionStore } from './features/extensions/extensionStore'
import { installPluginRuntimeBridge } from './features/extensions/runtimeBridge'
import {
  EXTERNAL_PLUGIN_PANEL_VIEWER_ID,
  PENDING_PLUGIN_CONFIG_SESSION_KEY,
  type OpenPluginConfigEventDetail,
  type OpenPluginPanelEventDetail,
} from './features/extensions/types'
import { installGlobalNativeHoldBlocker } from './features/mobile/mobileGestures'
import { useSidecarStore } from './features/sidecar/sidecarStore'
import './index.css'
import './workspaceVisuals.css'
import './brightWorkbench.css'
import './themeAccent.css'
import { applyThemePalette, readStoredThemePalette } from './themeAccent'
import { theme } from './theme'
import { startClientPerf } from './perf/clientPerf'
import { registerSW } from 'virtual:pwa-register'

installPluginRuntimeBridge()
applyThemePalette(readStoredThemePalette(), { persistLocal: false })
installGlobalNativeHoldBlocker()
bootstrapBuiltinExtensions()
startEsmPluginLoader()

if (typeof window !== 'undefined') {
  window.addEventListener('storage', (event) => {
    if (
      event.key === 'astrorder:theme-palette' ||
      event.key === 'astrorder:theme-seed'
    ) {
      applyThemePalette(readStoredThemePalette(), { persistLocal: false })
    }
  })

  window.addEventListener('astrorder:open-settings', (event) => {
    const detail = (event as CustomEvent<{ tab?: string; pluginId?: string }>).detail
    const pluginId = detail?.pluginId?.trim()
    if (!pluginId) return
    try {
      sessionStorage.setItem(PENDING_PLUGIN_CONFIG_SESSION_KEY, pluginId)
    } catch {
      // ignore storage errors in private contexts
    }
    window.dispatchEvent(
      new CustomEvent<OpenPluginConfigEventDetail>('astrorder:open-plugin-config', {
        detail: { pluginId },
      }),
    )
  })

  window.addEventListener('astrorder:open-plugin-panel', (event) => {
    const detail = (event as CustomEvent<OpenPluginPanelEventDetail>).detail
    if (!detail?.pluginId || !detail?.panelId || !detail.session) return
    const builtinPanel = extensionRegistry.findBuiltinPanel(detail.pluginId, detail.panelId, detail.mediaType)
    const extPlugin = useExtensionStore.getState().getById(detail.pluginId)
    const extPanel = extPlugin?.contributes?.sidecar_panels?.find(
      (panel) => panel.id === detail.panelId || (detail.mediaType && panel.media_type === detail.mediaType),
    )
    useSidecarStore.getState().openPluginPanel(detail.pluginId, detail.panelId, {
      title: detail.title || builtinPanel?.panel.title || extPanel?.title || extPlugin?.name || detail.panelId,
      mediaType: detail.mediaType || builtinPanel?.panel.mediaType || extPanel?.media_type,
      viewerId: builtinPanel ? detail.pluginId : EXTERNAL_PLUGIN_PANEL_VIEWER_ID,
      sessionId: detail.session.id,
      agentId: detail.session.agent_id,
      workspacePath: detail.session.workspace || undefined,
      connectionId: detail.session.connection_id || undefined,
      params: detail.params,
    })
  })
}

if (typeof window !== 'undefined' && 'astrorderDesktop' in window) {
  document.documentElement.classList.add('is-desktop-app')
}

if (import.meta.env.PROD) {
  const hadController = typeof navigator !== 'undefined' && Boolean(navigator.serviceWorker?.controller)
  let updateNotified = false
  const showUpdateNotification = (reloadAction: () => void) => {
    if (updateNotified) return
    updateNotified = true
    notifications.show({
      id: 'astrorder-pwa-update',
      color: 'violet',
      autoClose: false,
      title: '发现新版本',
      message: (
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 10, marginTop: 4 }}>
          <span>新版本已就绪，刷新即可生效</span>
          <button
            type="button"
            onClick={reloadAction}
            style={{
              flex: 'none',
              border: 0,
              borderRadius: 8,
              padding: '5px 11px',
              fontSize: 12,
              fontWeight: 600,
              cursor: 'pointer',
              background: 'var(--astr-accent, #7540bd)',
              color: 'var(--astr-accent-contrast, #ffffff)',
            }}
          >
            立即刷新
          </button>
        </div>
      ),
    })
  }
  if (hadController && navigator.serviceWorker) {
    navigator.serviceWorker.addEventListener('controllerchange', () => {
      showUpdateNotification(() => window.location.reload())
    })
  }
  const updateSW = registerSW({
    immediate: true,
    onNeedRefresh() {
      showUpdateNotification(() => {
        void updateSW(true)
      })
    },
    onRegisteredSW(_url, registration) {
      const checkUpdate = () => {
        void registration?.update()
      }
      document.addEventListener('visibilitychange', () => {
        if (document.visibilityState === 'visible') checkUpdate()
      })
      window.addEventListener('focus', checkUpdate)
      window.addEventListener('online', checkUpdate)
    },
  })
}


const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      gcTime: 1000 * 60 * 60 * 24,
      refetchOnWindowFocus: false,
      refetchOnReconnect: false,
    },
  },
})

startClientPerf({
  getQueryStats: () => {
    const all = queryClient.getQueryCache().getAll()
    let active = 0
    let fetching = 0
    for (const q of all) {
      if (q.getObserversCount() > 0) active++
      if (q.state.fetchStatus === 'fetching') fetching++
    }
    return { total: all.length, active, fetching }
  },
})


const rootEl = document.getElementById('root')!
const staticSplash = document.getElementById('app-splash-root')
if (staticSplash) {
  staticSplash.remove()
}

createRoot(rootEl).render(
  <StrictMode>
    <MantineProvider theme={theme} defaultColorScheme="light">
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>
          <DynamicIslandToastHost />
          <App />
        </BrowserRouter>
      </QueryClientProvider>
    </MantineProvider>
  </StrictMode>,
)
