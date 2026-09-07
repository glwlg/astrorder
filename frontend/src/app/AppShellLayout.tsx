import { AppShell, Drawer } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { useQueryClient } from '@tanstack/react-query'
import { useShallow } from 'zustand/react/shallow'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { api } from '../api/client'
import { scopeKey } from '../domain/semantics'
import type { Session } from '../domain/types'
import { selectSessions, useAstrorderStore } from '../state/store'
import { AppHeader } from '../components/AppHeader'
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
  const agents = useAstrorderStore((state) => state.agents)
  const connection = useAstrorderStore((state) => state.connection)
  const routeSessionId = location.pathname.startsWith('/chat/') ? routePart(location.pathname.slice('/chat/'.length)) : null
  const routeAgentId = new URLSearchParams(location.search).get('agent_id')
  const activeSessionKey = routeSessionId && routeAgentId ? scopeKey(routeAgentId, routeSessionId) : undefined

  const selectSession = (session: Session) => {
    navigate(`/chat/${encodeURIComponent(session.id)}?agent_id=${encodeURIComponent(session.agent_id)}`)
  }

  const logout = async () => {
    try {
      await api.logout()
    } finally {
      useAstrorderStore.getState().resetRuntime()
      queryClient.clear()
    }
  }

  return (
    <AppShell
      className="astrorder-shell"
      header={{ height: 64 }}
      navbar={{ width: 280, breakpoint: 'md', collapsed: { mobile: true } }}
      padding={0}
    >
      <AppShell.Header>
        <AppHeader connection={connection} onMenu={openMobile} onLogout={logout} />
      </AppShell.Header>
      <AppShell.Navbar className="desktop-navbar" p="md">
        <Sidebar sessions={sessions} agents={agents} activeSessionKey={activeSessionKey} onSelectSession={selectSession} />
      </AppShell.Navbar>
      <AppShell.Main className="shell-main">
        <Outlet />
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
        <Sidebar sessions={sessions} agents={agents} activeSessionKey={activeSessionKey} onSelectSession={selectSession} onNavigate={closeMobile} />
      </Drawer>
    </AppShell>
  )
}
