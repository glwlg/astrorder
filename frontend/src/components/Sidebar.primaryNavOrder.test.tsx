import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { usePluginSettingsStore } from '../features/plugins/pluginSettingsStore'
import { clearStoredAppProfileForTest, setAppProfile } from '../features/office/appProfile'
import { extensionRegistry } from '../features/extensions/extensionRegistry'
import { Sidebar, normalizePrimaryNavOrder } from './Sidebar'
import { NotificationOptInPrompt, NOTIFICATION_PROMPTED_STORAGE_KEY } from './NotificationOptInPrompt'
import { GeneralSettingsPage } from '../features/settings/GeneralSettingsPage'

function createDataTransfer(initial: Record<string, string> = {}) {
  const store = new Map<string, string>(Object.entries(initial))
  return {
    effectAllowed: 'all',
    dropEffect: 'none',
    get types() {
      return Array.from(store.keys())
    },
    setData: (type: string, val: string) => store.set(type, val),
    getData: (type: string) => store.get(type) || '',
  }
}

describe('Sidebar primary nav core ordering & drag reorder', () => {
  beforeEach(() => {
    localStorage.clear()
    usePluginSettingsStore.getState().reset()
    vi.spyOn(usePluginSettingsStore.getState(), 'setPluginConfig').mockImplementation((pluginId, key, value) => {
      usePluginSettingsStore.setState((state) => ({
        pluginConfigs: {
          ...state.pluginConfigs,
          [pluginId]: { ...(state.pluginConfigs[pluginId] || {}), [key]: value },
        },
      }))
    })
  })

  afterEach(() => {
    cleanup()
    clearStoredAppProfileForTest()
    vi.restoreAllMocks()
  })

  it('orders default primary nav by core workflow with office tools at the end', () => {
    expect(normalizePrimaryNavOrder(undefined)).toEqual([
      'chat',
      'bots',
      'inbox',
      'monitor',
      'groups',
      'analytics',
      'backups',
      'agentAccounts',
      'digest',
      'meetings',
      'learning',
    ])
  })

  it('supports drag-and-drop reordering and persists order without breaking monitor session drop', () => {
    Object.defineProperty(window, 'innerHeight', { configurable: true, writable: true, value: 1080 })
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })

    render(
      <QueryClientProvider client={queryClient}>
        <MemoryRouter initialEntries={['/chat']}>
          <MantineProvider>
            <Sidebar sessions={[]} agents={{}} onSelectSession={() => {}} />
          </MantineProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    )

    const getPrimaryLabels = () =>
      Array.from(document.querySelectorAll('.sidebar-primary-nav .sidebar-nav-link')).map(
        (el) => el.getAttribute('aria-label'),
      )

    expect(getPrimaryLabels()).toEqual(['会话', 'Bot', '待办', '矩阵', '群聊', '统计', '备份', '账号', '复盘', '会议', '成长'])

    const digestLink = screen.getByRole('link', { name: '复盘' })
    const inboxLink = screen.getByRole('link', { name: '待办' })
    vi.spyOn(inboxLink, 'getBoundingClientRect').mockReturnValue({
      top: 100,
      height: 48,
      bottom: 148,
      left: 0,
      right: 48,
      width: 48,
      x: 0,
      y: 100,
      toJSON: () => {},
    })

    const dt = createDataTransfer()
    fireEvent.dragStart(digestLink, { dataTransfer: dt })
    fireEvent.dragOver(inboxLink, { dataTransfer: dt, clientY: 110 })
    fireEvent.drop(inboxLink, { dataTransfer: dt, clientY: 110 })
    fireEvent.dragEnd(digestLink)

    expect(getPrimaryLabels()).toEqual(['会话', 'Bot', '复盘', '待办', '矩阵', '群聊', '统计', '备份', '账号', '会议', '成长'])
    expect(JSON.parse(localStorage.getItem('astrorder:sidebar-primary-nav-order') || '[]')).toEqual([
      'chat',
      'bots',
      'digest',
      'inbox',
      'monitor',
      'groups',
      'analytics',
      'backups',
      'agentAccounts',
      'meetings',
      'learning',
    ])

    // Dragging a session onto 矩阵 still adds it to monitor without changing primary nav order
    const monitorLink = screen.getByRole('link', { name: '矩阵' })
    const sessionDt = createDataTransfer({
      'application/x-astrorder-session': JSON.stringify({ agent_id: 'codex', id: 's-1', title: '测试会话' }),
    })
    fireEvent.dragOver(monitorLink, { dataTransfer: sessionDt })
    fireEvent.drop(monitorLink, { dataTransfer: sessionDt })
    expect(JSON.parse(localStorage.getItem('astrorder:monitor-sessions') || '[]')).toEqual(['codex::s-1'])
    expect(getPrimaryLabels()).toEqual(['会话', 'Bot', '复盘', '待办', '矩阵', '群聊', '统计', '备份', '账号', '会议', '成长'])
  })

  it('auto-collapses primary nav on short screens, shares scroll with integrations, and removes persistent notification rail button', () => {
    Object.defineProperty(window, 'innerHeight', { configurable: true, writable: true, value: 760 })
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })

    const { container } = render(
      <QueryClientProvider client={queryClient}>
        <MemoryRouter initialEntries={['/meetings']}>
          <MantineProvider>
            <Sidebar sessions={[]} agents={{}} onSelectSession={() => {}} />
          </MantineProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    )

    // Auto-collapsed on short viewport: top 4 + active route ('会议') stay visible
    const collapsedLabels = Array.from(container.querySelectorAll('.sidebar-primary-nav .sidebar-nav-link')).map(
      (el) => el.getAttribute('aria-label'),
    )
    expect(collapsedLabels).toEqual(['会话', 'Bot', '待办', '矩阵', '会议'])

    // Primary nav and integrations share one scroll container
    const scrollBox = container.querySelector('.workspace-navigation-scroll')
    expect(scrollBox).not.toBeNull()
    expect(scrollBox?.querySelector('.sidebar-primary-nav')).not.toBeNull()
    expect(scrollBox?.querySelector('.sidebar-integrations-nav')).not.toBeNull()

    // Persistent notification button is removed from bottom rail tools
    const tools = container.querySelector('.workspace-navigation-tools')
    expect(tools?.textContent).not.toContain('通知')

    // Clicking expand reveals all 11 primary nav items
    fireEvent.click(screen.getByRole('button', { name: /展开更多导航/ }))
    const expandedLabels = Array.from(container.querySelectorAll('.sidebar-primary-nav .sidebar-nav-link')).map(
      (el) => el.getAttribute('aria-label'),
    )
    expect(expandedLabels).toHaveLength(11)
    expect(localStorage.getItem('astrorder:sidebar-primary-nav-fold')).toBe('expanded')

    // Clicking collapse folds them back
    fireEvent.click(screen.getByRole('button', { name: '收起导航按钮' }))
    expect(container.querySelectorAll('.sidebar-primary-nav .sidebar-nav-link')).toHaveLength(5)
    expect(localStorage.getItem('astrorder:sidebar-primary-nav-fold')).toBe('collapsed')
  })

  it('asks once for notifications via NotificationOptInPrompt and exposes notification control in GeneralSettingsPage', () => {
    render(
      <MantineProvider>
        <NotificationOptInPrompt ready forceOpen />
        <GeneralSettingsPage />
      </MantineProvider>,
    )

    expect(screen.getByRole('dialog', { name: '首次启动通知提醒设置' })).toBeInTheDocument()
    expect(screen.getByText('桌面与后台通知')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '暂不开启' }))
    expect(screen.queryByRole('dialog', { name: '首次启动通知提醒设置' })).not.toBeInTheDocument()
    expect(localStorage.getItem(NOTIFICATION_PROMPTED_STORAGE_KEY)).toBe('1')
  })

  it('hides Bot and Learning Radar (along with other dev-only modules) in global office profile', () => {
    Object.defineProperty(window, 'innerHeight', { configurable: true, writable: true, value: 1080 })
    setAppProfile('office', { syncPluginStore: false })
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })

    render(
      <QueryClientProvider client={queryClient}>
        <MemoryRouter initialEntries={['/chat']}>
          <MantineProvider>
            <Sidebar sessions={[]} agents={{}} onSelectSession={() => {}} />
          </MantineProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    )

    const primaryLabels = Array.from(document.querySelectorAll('.sidebar-primary-nav .sidebar-nav-link')).map(
      (el) => el.getAttribute('aria-label'),
    )
    expect(primaryLabels).toEqual(['会话', '待办', '复盘', '会议'])
    expect(primaryLabels).not.toContain('Bot')
    expect(primaryLabels).not.toContain('成长')
    expect(primaryLabels).not.toContain('矩阵')
    expect(primaryLabels).not.toContain('群聊')
  })

  it('renders dynamically registered ESM extension pages in primary nav and removes them on dispose', () => {
    Object.defineProperty(window, 'innerHeight', { configurable: true, writable: true, value: 1080 })
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })

    const disposable = extensionRegistry.registerPage({
      pluginId: 'acme-board',
      id: 'kanban',
      title: '敏捷看板',
      navLabel: '看板',
      order: 200,
      hideInOfficeMode: true,
      component: () => <div>kanban</div>,
      source: 'esm',
    })

    try {
      render(
        <QueryClientProvider client={queryClient}>
          <MemoryRouter initialEntries={['/chat']}>
            <MantineProvider>
              <Sidebar sessions={[]} agents={{}} onSelectSession={() => {}} />
            </MantineProvider>
          </MemoryRouter>
        </QueryClientProvider>,
      )

      const extLink = screen.getByRole('link', { name: '看板' })
      expect(extLink).toHaveAttribute('href', '/ext-page/acme-board/kanban')

      // Office profile hides dev-only extension page
      act(() => {
        setAppProfile('office', { syncPluginStore: false })
      })
      expect(screen.queryByRole('link', { name: '看板' })).toBeNull()

      // Switch back to developer mode and dispose removes link immediately
      act(() => {
        setAppProfile('developer', { syncPluginStore: false })
      })
      expect(screen.getByRole('link', { name: '看板' })).toBeInTheDocument()
      act(() => {
        disposable.dispose()
      })
      expect(screen.queryByRole('link', { name: '看板' })).toBeNull()
    } finally {
      disposable.dispose()
    }
  })
})
