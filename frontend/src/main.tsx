import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '@mantine/core/styles.css'
import '@mantine/notifications/styles.css'
import { MantineProvider } from '@mantine/core'
import { Notifications, notifications } from '@mantine/notifications'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import App from './app/App.tsx'
import { installGlobalNativeHoldBlocker } from './features/mobile/mobileGestures'
import './index.css'
import './workspaceVisuals.css'
import './brightWorkbench.css'
import './themeAccent.css'
import { applyThemePalette, readStoredThemePalette } from './themeAccent'
import { theme } from './theme'
import { LiquidSurfaces } from './components/LiquidSurfaces'
import { startClientPerf } from './perf/clientPerf'
import { registerSW } from 'virtual:pwa-register'

applyThemePalette(readStoredThemePalette(), { persistLocal: false })
installGlobalNativeHoldBlocker()

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
          <Notifications position="top-right" zIndex={3000} />
          <LiquidSurfaces />
          <App />
        </BrowserRouter>
      </QueryClientProvider>
    </MantineProvider>
  </StrictMode>,
)
