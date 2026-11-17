import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '@mantine/core/styles.css'
import '@mantine/notifications/styles.css'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import App from './app/App.tsx'
import { installGlobalNativeHoldBlocker } from './features/mobile/mobileGestures'
import './index.css'
import './workspaceVisuals.css'
import './brightWorkbench.css'
import { theme } from './theme'
import { LiquidSurfaces } from './components/LiquidSurfaces'
import { registerSW } from 'virtual:pwa-register'

installGlobalNativeHoldBlocker()

if (typeof window !== 'undefined' && 'astrorderDesktop' in window) {
  document.documentElement.classList.add('is-desktop-app')
}

if (import.meta.env.PROD) {
  registerSW({
    immediate: true,
    onRegisteredSW(_url, registration) {
      document.addEventListener('visibilitychange', () => {
        if (document.visibilityState === 'visible') void registration?.update()
      })
    },
  })
}


const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      gcTime: 1000 * 60 * 60 * 24,
    },
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
