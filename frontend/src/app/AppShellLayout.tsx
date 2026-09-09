import { AppShell, Drawer } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { useQueryClient } from '@tanstack/react-query'
import { useShallow } from 'zustand/react/shallow'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { useRef, useState } from 'react'
import { api } from '../api/client'
import { scopeKey } from '../domain/semantics'
import type { Session } from '../domain/types'
import { selectProjects, selectSessions, useAstrorderStore } from '../state/store'
import { AppHeader } from '../components/AppHeader'
import { ErrorBoundary } from '../components/ErrorBoundary'
import { Sidebar } from '../components/Sidebar'

function routePart(value: string): string {
  try {
    return decodeURIComponent(value)
  } catch {
    return value
  }
}

export function AppShellLayout() {
  const navigate = useNavigate()
  const location = useLocation()
  const queryClient = useQueryClient()
  const [mobileOpened, { open: openMobile, close: closeMobile }] = useDisclosure(false)
  const sessions = useAstrorderStore(useShallow(selectSessions))
  const projects = useAstrorderStore(useShallow(selectProjects))
  const agents = useAstrorderStore((state) => state.agents)
  const connection = useAstrorderStore((state) => state.connection)
  const routeSessionId = location.pathname.startsWith('/chat/') ? routePart(location.pathname.slice('/chat/'.length)) : null
  const routeAgentId = new URLSearchParams(location.search).get('agent_id')
  const activeSessionKey = routeSessionId && routeAgentId ? scopeKey(routeAgentId, routeSessionId) : undefined

  const SIDEBAR_WIDTH_KEY = 'astrorder:sidebar_width'
  const [sidebarWidth, setSidebarWidth] = useState<number>(() => {
    try {
      const stored = localStorage.getItem(SIDEBAR_WIDTH_KEY)
      if (stored) {
        const val = parseInt(stored, 10)
        if (val >= 220 && val <= 600) return val
      }
    } catch {}
    return 280
  })

  const isResizingRef = useRef(false)
  const startXRef = useRef(0)
  const startWidthRef = useRef(280)

  const handleResizerMouseDown = (e: React.MouseEvent) => {
    e.preventDefault()
    isResizingRef.current = true
    startXRef.current = e.clientX
    startWidthRef.current = sidebarWidth
    document.body.style.cursor = 'col-resize'
    document.body.style.userSelect = 'none'

    const handleMouseMove = (moveEvent: MouseEvent) => {
      if (!isResizingRef.current) return
      const delta = moveEvent.clientX - startXRef.current
      const newWidth = Math.min(Math.max(startWidthRef.current + delta, 220), 600)
      setSidebarWidth(newWidth)
    }

    const handleMouseUp = () => {
      if (!isResizingRef.current) return
      isResizingRef.current = false
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
      window.removeEventListener('mousemove', handleMouseMove)
      window.removeEventListener('mouseup', handleMouseUp)
      setSidebarWidth((latest) => {
        try {
          localStorage.setItem(SIDEBAR_WIDTH_KEY, String(latest))
        } catch {}
        return latest
      })
    }

    window.addEventListener('mousemove', handleMouseMove)
    window.addEventListener('mouseup', handleMouseUp)
  }

  const selectSession = (session: Session) => {
    navigate(`/chat/${encodeURIComponent(session.id)}?agent_id=${encodeURIComponent(session.agent_id)}`)
  }

  const logout = async () => {
    try {
      await api.logout()
    } finally {
      try {
        localStorage.removeItem('astrorder:token')
      } catch {}
      useAstrorderStore.getState().resetRuntime()
      queryClient.clear()
    }
  }

  return (
    <AppShell
      className="astrorder-shell"
      header={{ height: 64 }}
      navbar={{ width: sidebarWidth, breakpoint: 'md', collapsed: { mobile: true } }}
      padding={0}
    >
      <AppShell.Header>
        <AppHeader connection={connection} onMenu={openMobile} onLogout={logout} />
      </AppShell.Header>
      <AppShell.Navbar className="desktop-navbar" p="md" style={{ width: sidebarWidth }}>
        <Sidebar sessions={sessions} agents={agents} projects={projects} activeSessionKey={activeSessionKey} onSelectSession={selectSession} />
        <div
          className="sidebar-resizer"
          onMouseDown={handleResizerMouseDown}
          role="separator"
          aria-label="拖拽调整侧边栏宽度"
          title="按住拖拽调整宽度"
        />
      </AppShell.Navbar>
      <AppShell.Main className="shell-main">
        <ErrorBoundary fallbackTitle="会话主区域加载异常">
          <Outlet />
        </ErrorBoundary>
      </AppShell.Main>
      <Drawer
        className="mobile-navigation"
        opened={mobileOpened}
        onClose={closeMobile}
        title="星序 · Astrorder"
        padding="md"
        size="min(88vw, 360px)"
        hiddenFrom="md"
      >
        <Sidebar sessions={sessions} agents={agents} projects={projects} activeSessionKey={activeSessionKey} onSelectSession={selectSession} onNavigate={closeMobile} />
      </Drawer>
    </AppShell>
  )
}
