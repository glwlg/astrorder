import { AppShell, Drawer } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { useShallow } from 'zustand/react/shallow'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { useCallback, useEffect, useRef, useState, type CSSProperties } from 'react'
import { scopeKey } from '../domain/semantics'
import { groupHasActiveTask } from '../domain/groupActivity'
import type { Session } from '../domain/types'
import { selectProjects, selectSessions, useAstrorderStore } from '../state/store'
import { AppHeader } from '../components/AppHeader'
import { AttentionIsland } from '../components/AttentionIsland'
import { CommandPalette } from '../components/CommandPalette'
import { ErrorBoundary } from '../components/ErrorBoundary'
import { Sidebar } from '../components/Sidebar'
import { BackgroundTasks } from '../components/BackgroundTasks'
import { useShortcutHandlers } from '../features/shortcuts/shortcuts'
import { agySessionEnvironment, AgyQuotaRefreshProvider } from '../features/agentAccounts/AgyQuotaRefreshProvider'

const SIDEBAR_WIDTH_KEY = 'astrorder:sidebar_width'
const SIDEBAR_COLLAPSED_KEY = 'astrorder:sidebar_panel_collapsed'

function routePart(value: string): string {
  try {
    return decodeURIComponent(value)
  } catch {
    return value
  }
}

function isVisibleOverlayPopup(el: Element): boolean {
  if (!(el instanceof HTMLElement)) return false
  const style = getComputedStyle(el)
  if (style.display === 'none' || style.visibility === 'hidden' || style.opacity === '0') return false
  const rect = el.getBoundingClientRect()
  return rect.width > 0 && rect.height > 0
}

function hasVisibleSidebarOverlay(): boolean {
  for (const el of document.querySelectorAll('.mantine-Menu-dropdown, .mantine-Popover-dropdown, .mantine-Modal-content')) {
    if (isVisibleOverlayPopup(el)) return true
  }
  return false
}

export function AppShellLayout({ chatReturnPath }: { chatReturnPath: string }) {
  const navigate = useNavigate()
  const location = useLocation()
  const [mobileOpened, { open: openMobile, close: closeMobile }] = useDisclosure(false)
  const sessions = useAstrorderStore(useShallow(selectSessions))
  const projects = useAstrorderStore(useShallow(selectProjects))
  const agents = useAstrorderStore((state) => state.agents)
  const connection = useAstrorderStore((state) => state.connection)
  const approvals = useAstrorderStore((state) => state.approvals)
  const botGroups = useAstrorderStore((state) => state.botGroups)
  const [paletteOpened, setPaletteOpened] = useState(false)

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && !event.altKey && !event.shiftKey && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        setPaletteOpened((prev) => !prev)
      }
    }
    const onOpenPalette = () => setPaletteOpened(true)
    window.addEventListener('keydown', onKeyDown)
    window.addEventListener('astrorder:open-command-palette', onOpenPalette)
    return () => {
      window.removeEventListener('keydown', onKeyDown)
      window.removeEventListener('astrorder:open-command-palette', onOpenPalette)
    }
  }, [])

  const hasPendingAttention =
    Object.values(approvals).some((a) => a.state === 'pending') ||
    sessions.some((s) => s.status === 'waiting_approval')
  const hasRunningActivity =
    sessions.some((s) => s.status === 'running') ||
    Object.values(botGroups).some(groupHasActiveTask)
  const ambientState: 'attention' | 'running' | 'idle' = hasPendingAttention
    ? 'attention'
    : hasRunningActivity
      ? 'running'
      : 'idle'
  const routeSessionId = location.pathname.startsWith('/chat/') ? routePart(location.pathname.slice('/chat/'.length)) : null
  const routeAgentId = new URLSearchParams(location.search).get('agent_id')
  const forceWelcome = Boolean((location.state as { welcome?: boolean } | null)?.welcome)
  const routeCandidates = routeSessionId && !routeSessionId.startsWith('group-')
    ? sessions.filter(session => session.id === routeSessionId && (!routeAgentId || session.agent_id === routeAgentId))
    : location.pathname === '/chat' && !forceWelcome && sessions.length === 1 ? sessions : []
  const activeSessionKey = routeCandidates.length === 1 ? scopeKey(routeCandidates[0].agent_id, routeCandidates[0].id) : undefined
  const session = routeCandidates.length === 1 ? routeCandidates[0] : undefined
  const compactNavigation =
    location.pathname.startsWith('/agent-accounts') ||
    location.pathname.startsWith('/backups') ||
    location.pathname.startsWith('/analytics')

  const [sidebarWidth, setSidebarWidth] = useState<number>(() => {
    try {
      const stored = localStorage.getItem(SIDEBAR_WIDTH_KEY)
      if (stored) {
        const val = parseInt(stored, 10)
        if (val >= 320 && val <= 520) return val
      }
    } catch {}
    return 352
  })

  // Arc-style collapsible secondary sidebar panel (defaults to expanded so drag-and-drop & rail work out of the box)
  const [panelCollapsed, setPanelCollapsed] = useState<boolean>(() => {
    try {
      return localStorage.getItem(SIDEBAR_COLLAPSED_KEY) === '1'
    } catch {
      return false
    }
  })
  const [isPeeking, setIsPeeking] = useState(false)
  const peekEnterTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const peekLeaveTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const draggingFromSidebarRef = useRef(false)

  const clearPeekTimers = useCallback(() => {
    if (peekEnterTimer.current !== undefined) {
      clearTimeout(peekEnterTimer.current)
      peekEnterTimer.current = undefined
    }
    if (peekLeaveTimer.current !== undefined) {
      clearTimeout(peekLeaveTimer.current)
      peekLeaveTimer.current = undefined
    }
  }, [])

  const togglePanelCollapse = useCallback(() => {
    if (compactNavigation) return
    clearPeekTimers()
    setIsPeeking(false)
    setPanelCollapsed((prev) => {
      const next = !prev
      try {
        localStorage.setItem(SIDEBAR_COLLAPSED_KEY, next ? '1' : '0')
      } catch {}
      return next
    })
  }, [clearPeekTimers, compactNavigation])

  useShortcutHandlers({ sidebar: togglePanelCollapse })

  const handlePeekEnter = useCallback(() => {
    if (compactNavigation || !panelCollapsed) return
    if (peekLeaveTimer.current !== undefined) {
      clearTimeout(peekLeaveTimer.current)
      peekLeaveTimer.current = undefined
    }
    if (isPeeking) return
    if (peekEnterTimer.current !== undefined) return
    peekEnterTimer.current = setTimeout(() => {
      peekEnterTimer.current = undefined
      setIsPeeking(true)
    }, 60)
  }, [compactNavigation, isPeeking, panelCollapsed])

  const handlePeekLeave = useCallback(() => {
    if (peekEnterTimer.current !== undefined) {
      clearTimeout(peekEnterTimer.current)
      peekEnterTimer.current = undefined
    }
    if (!isPeeking || draggingFromSidebarRef.current) return
    if (peekLeaveTimer.current !== undefined) return
    peekLeaveTimer.current = setTimeout(() => {
      peekLeaveTimer.current = undefined
      const panel = document.querySelector('.workspace-sidebar-panel')
      const active = document.activeElement
      // Keep peek drawer open only while actively typing inside sidebar search or interacting with a visible menu/modal
      if (panel && active && panel.contains(active) && active.matches('input, textarea, select')) return
      if (hasVisibleSidebarOverlay()) return
      setIsPeeking(false)
    }, 180)
  }, [isPeeking])

  const dismissPeek = useCallback(() => {
    clearPeekTimers()
    const panel = document.querySelector('.workspace-sidebar-panel')
    const active = document.activeElement
    if (panel && active instanceof HTMLElement && panel.contains(active)) {
      active.blur()
    }
    setIsPeeking(false)
  }, [clearPeekTimers])

  useEffect(() => {
    return () => clearPeekTimers()
  }, [clearPeekTimers])

  useEffect(() => {
    if (!isPeeking) return
    const isTargetInsidePeek = (target: EventTarget | null) => {
      if (!(target instanceof Element)) return false
      if (target.closest('.workspace-sidebar-panel, .sidebar-edge-peek-zone')) return true
      const overlay = target.closest('.mantine-Menu-dropdown, .mantine-Popover-dropdown, .mantine-Modal-content')
      return Boolean(overlay && isVisibleOverlayPopup(overlay))
    }
    const onPointerDown = (event: PointerEvent) => {
      if (isTargetInsidePeek(event.target)) return
      dismissPeek()
    }
    const onPointerMove = (event: PointerEvent) => {
      if (draggingFromSidebarRef.current) return
      const panel = document.querySelector<HTMLElement>('.workspace-sidebar-panel')
      const rect = panel?.getBoundingClientRect()
      const withinPanelBridge = rect ? event.clientX >= rect.left - 18 && event.clientX <= rect.right + 12 : false
      const onPeekNavTab =
        event.target instanceof Element &&
        Boolean(event.target.closest('.sidebar-nav-link[data-peek-trigger="true"]'))
      if (withinPanelBridge || onPeekNavTab || isTargetInsidePeek(event.target)) {
        if (peekLeaveTimer.current !== undefined) {
          clearTimeout(peekLeaveTimer.current)
          peekLeaveTimer.current = undefined
        }
        return
      }
      handlePeekLeave()
    }
    const onDocumentLeave = () => {
      handlePeekLeave()
    }
    const onWindowBlur = () => {
      dismissPeek()
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') dismissPeek()
    }
    const onDragStart = () => {
      draggingFromSidebarRef.current = true
    }
    const onDragEnd = () => {
      draggingFromSidebarRef.current = false
      dismissPeek()
    }
    document.addEventListener('pointerdown', onPointerDown, true)
    document.addEventListener('pointermove', onPointerMove, { passive: true })
    document.documentElement.addEventListener('mouseleave', onDocumentLeave)
    window.addEventListener('blur', onWindowBlur)
    document.addEventListener('keydown', onKeyDown)
    window.addEventListener('dragstart', onDragStart, true)
    window.addEventListener('dragend', onDragEnd, true)
    return () => {
      document.removeEventListener('pointerdown', onPointerDown, true)
      document.removeEventListener('pointermove', onPointerMove)
      document.documentElement.removeEventListener('mouseleave', onDocumentLeave)
      window.removeEventListener('blur', onWindowBlur)
      document.removeEventListener('keydown', onKeyDown)
      window.removeEventListener('dragstart', onDragStart, true)
      window.removeEventListener('dragend', onDragEnd, true)
    }
  }, [dismissPeek, handlePeekLeave, isPeeking])

  const isResizingRef = useRef(false)
  const startXRef = useRef(0)
  const startWidthRef = useRef(352)
  const [isResizing, setIsResizing] = useState(false)

  const handleResizerMouseDown = (e: React.MouseEvent) => {
    e.preventDefault()
    isResizingRef.current = true
    setIsResizing(true)
    startXRef.current = e.clientX
    startWidthRef.current = sidebarWidth
    document.body.style.cursor = 'col-resize'
    document.body.style.userSelect = 'none'

    const handleMouseMove = (moveEvent: MouseEvent) => {
      if (!isResizingRef.current) return
      const delta = moveEvent.clientX - startXRef.current
      const newWidth = Math.min(Math.max(startWidthRef.current + delta, 320), 520)
      setSidebarWidth(newWidth)
    }

    const handleMouseUp = () => {
      if (!isResizingRef.current) return
      isResizingRef.current = false
      setIsResizing(false)
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

  const effectiveRailWidth = compactNavigation || panelCollapsed ? 64 : sidebarWidth
  const shellStyle = {
    '--astr-shell-rail-width': `${effectiveRailWidth}px`,
    '--astr-sidebar-expanded-width': `${sidebarWidth}px`,
  } as CSSProperties

  return (
    <AppShell
      className={`astrorder-shell${isResizing ? ' is-sidebar-resizing' : ''}`}
      style={shellStyle}
      header={{ height: 64 }}
      navbar={{ width: effectiveRailWidth, breakpoint: 'md', collapsed: { mobile: true } }}
      padding={0}
    >
      <div id="astr-shell-sky" className="astr-shell-sky" data-ambient-state={ambientState} aria-hidden="true" />
      <AppShell.Header className="sidebar-header">
        <AppHeader connection={connection} onMenu={openMobile} />
      </AppShell.Header>
      <AppShell.Navbar className="desktop-navbar" p={0} style={{ width: effectiveRailWidth }}>
        <Sidebar
          sessions={sessions}
          agents={agents}
          projects={projects}
          activeSessionKey={activeSessionKey}
          chatReturnPath={chatReturnPath}
          onSelectSession={selectSession}
          panelCollapsed={!compactNavigation && panelCollapsed}
          isPeeking={!compactNavigation && panelCollapsed && isPeeking}
          onTogglePanelCollapse={togglePanelCollapse}
          onPeekEnter={handlePeekEnter}
          onPeekLeave={handlePeekLeave}
          onDismissPeek={dismissPeek}
        />
        {!compactNavigation && panelCollapsed && !isPeeking && (
          <div
            className="sidebar-edge-peek-zone"
            onMouseEnter={handlePeekEnter}
            onPointerEnter={handlePeekEnter}
            onMouseLeave={handlePeekLeave}
            onClick={() => {
              clearPeekTimers()
              setIsPeeking(true)
            }}
            title="悬停或点击呼出侧栏（Ctrl+B 固定）"
            aria-hidden="true"
          />
        )}
        {!compactNavigation && !panelCollapsed && (
          <div
            className="sidebar-resizer"
            data-resizing={isResizing}
            onMouseDown={handleResizerMouseDown}
            role="separator"
            aria-label="拖拽调整侧边栏宽度"
            title="按住拖拽调整宽度"
          />
        )}
      </AppShell.Navbar>
      <AppShell.Main className="shell-main">
        <ErrorBoundary fallbackTitle="会话主区域加载异常">
          <AgyQuotaRefreshProvider sessionEnvironment={agySessionEnvironment(session, session && agents[session.agent_id])}>
            <Outlet />
          </AgyQuotaRefreshProvider>
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
        <Sidebar sessions={sessions} agents={agents} projects={projects} activeSessionKey={activeSessionKey} chatReturnPath={chatReturnPath} onSelectSession={selectSession} onNavigate={closeMobile} />
      </Drawer>
      <AttentionIsland activeSessionKey={activeSessionKey} />
      <CommandPalette
        opened={paletteOpened}
        onClose={() => setPaletteOpened(false)}
        onToggleSidebar={togglePanelCollapse}
        onOpenSettings={() => window.dispatchEvent(new CustomEvent('astrorder:open-settings'))}
      />
      <BackgroundTasks />
    </AppShell>
  )
}
