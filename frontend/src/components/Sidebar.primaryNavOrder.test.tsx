import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { usePluginSettingsStore } from '../features/plugins/pluginSettingsStore'
import { Sidebar, normalizePrimaryNavOrder } from './Sidebar'

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
    vi.restoreAllMocks()
  })

  it('orders default primary nav by core workflow with office tools at the end', () => {
    expect(normalizePrimaryNavOrder(undefined)).toEqual([
      'chat',
      'inbox',
      'monitor',
      'groups',
      'analytics',
      'backups',
      'agentAccounts',
      'digest',
      'meetings',
    ])
  })

  it('supports drag-and-drop reordering and persists order without breaking monitor session drop', () => {
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

    expect(getPrimaryLabels()).toEqual(['会话', '待办', '矩阵', '群聊', '统计', '备份', '账号', '复盘', '会议'])

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

    expect(getPrimaryLabels()).toEqual(['会话', '复盘', '待办', '矩阵', '群聊', '统计', '备份', '账号', '会议'])
    expect(JSON.parse(localStorage.getItem('astrorder:sidebar-primary-nav-order') || '[]')).toEqual([
      'chat',
      'digest',
      'inbox',
      'monitor',
      'groups',
      'analytics',
      'backups',
      'agentAccounts',
      'meetings',
    ])

    // Dragging a session onto 矩阵 still adds it to monitor without changing primary nav order
    const monitorLink = screen.getByRole('link', { name: '矩阵' })
    const sessionDt = createDataTransfer({
      'application/x-astrorder-session': JSON.stringify({ agent_id: 'codex', id: 's-1', title: '测试会话' }),
    })
    fireEvent.dragOver(monitorLink, { dataTransfer: sessionDt })
    fireEvent.drop(monitorLink, { dataTransfer: sessionDt })
    expect(JSON.parse(localStorage.getItem('astrorder:monitor-sessions') || '[]')).toEqual(['codex::s-1'])
    expect(getPrimaryLabels()).toEqual(['会话', '复盘', '待办', '矩阵', '群聊', '统计', '备份', '账号', '会议'])
  })
})
