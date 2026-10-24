import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, createEvent, fireEvent, render, screen, within, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { SwarmDagView } from './SwarmDagView'
import type { Session } from '../../domain/types'
import { useSessionResources } from '../../hooks/useAstrorderData'
import { useAstrorderStore } from '../../state/store'

vi.mock('./MonitorPage', () => ({
  MonitorCard: ({ session }: { session: Session }) => {
    useSessionResources(session, true)
    return <div role="log" style={{ height: 160, overflowY: 'auto' }}><div style={{ height: 1000 }}>会话消息</div></div>
  },
}))

const session = { id: 'session-1', agent_id: 'agent-1', title: '星图测试会话', workspace: '/repo', status: 'running' as const, updated_at: '2026-10-14T09:00:00Z' }
const agent = { id: 'agent-1', kind: 'codex' as const, name: '本机', status: 'ready' as const, capabilities: ['chat'], limitation: null }

function renderDag() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <SwarmDagView allSessions={[session]} agents={{ [agent.id]: agent }} activeRootKey="agent-1::session-1" onOpenSession={vi.fn()} />
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('SwarmDagView canvas gestures', () => {
  beforeEach(() => {
    vi.spyOn(api, 'getTelemetry').mockResolvedValue({ items: {} })
    vi.spyOn(api, 'listSos').mockResolvedValue({ items: [], count: 0 })
    vi.spyOn(api, 'getBlackboard').mockResolvedValue({ namespace: 'swarm:agent-1::session-1', items: {}, count: 0 })
    vi.spyOn(api, 'syncSession').mockResolvedValue(session)
    vi.spyOn(api, 'getMessages').mockResolvedValue({ items: [], next_cursor: null })
    vi.spyOn(api, 'getCommands').mockResolvedValue({ items: [] })
    vi.spyOn(api, 'getTasks').mockResolvedValue({ items: [] })
  })
  afterEach(() => { cleanup(); vi.restoreAllMocks() })

  it('allows scrolling a portalled conversation without zooming the topology', async () => {
    const { container } = renderDag()
    const viewport = container.querySelector('.swarm-dag-viewport')!
    fireEvent.click(screen.getByRole('button', { name: '弹窗查看现场' }))
    const dialog = await screen.findByRole('dialog')
    expect(viewport.contains(dialog)).toBe(false)
    const messages = within(dialog).getByRole('log')
    // JSDOM has no layout; give Mantine's scroll lock a scrollable viewport.
    Object.defineProperties(messages, {
      scrollHeight: { configurable: true, value: 1000 },
      clientHeight: { configurable: true, value: 160 },
    })
    messages.scrollTop = 100

    for (const deltaY of [120, -120]) {
      const wheel = createEvent.wheel(messages, { deltaY, bubbles: true, cancelable: true })
      fireEvent(messages, wheel)
      expect(screen.getByText('100%')).toBeInTheDocument()
      expect(wheel.defaultPrevented).toBe(false)
    }

    fireEvent.wheel(viewport, { deltaY: -120 })
    expect(screen.getByText('108%')).toBeInTheDocument()
    fireEvent.wheel(viewport, { deltaY: 120 })
    expect(screen.getByText('100%')).toBeInTheDocument()
  })

  it('keeps A -> B -> C while double-clicking B and synchronizing its details', async () => {
    const a: Session = { ...session, id: 'a', title: '主星A' }
    const b: Session = { ...session, id: 'b', title: '伴星B', delegation_parent_agent_id: a.agent_id, delegation_parent_session_id: a.id }
    const c: Session = { ...session, id: 'c', title: '伴星C', delegation_parent_agent_id: b.agent_id, delegation_parent_session_id: b.id }
    useAstrorderStore.getState().resetRuntime()
    useAstrorderStore.setState({ sessions: { 'agent-1::a': a, 'agent-1::b': b, 'agent-1::c': c } })
    vi.mocked(api.syncSession).mockResolvedValue({ ...b, status: 'idle' })
    const openSession = vi.fn()
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    function ConnectedDag() {
      const sessions = useAstrorderStore(state => state.sessions)
      return <SwarmDagView allSessions={Object.values(sessions)} agents={{ [agent.id]: agent }} activeRootKey="agent-1::a" onOpenSession={openSession} />
    }
    const { container } = render(<MantineProvider><QueryClientProvider client={queryClient}><ConnectedDag /></QueryClientProvider></MantineProvider>)
    const nodeB = screen.getByText('伴星B').closest('.swarm-node-card')!
    fireEvent.click(nodeB)
    fireEvent.click(nodeB)
    fireEvent.doubleClick(nodeB)
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('🪐 伴星会话')).toBeInTheDocument()
    await waitFor(() => expect(useAstrorderStore.getState().sessions['agent-1::b'].status).toBe('idle'))
    expect(container.querySelectorAll('.swarm-node-card')).toHaveLength(3)
    expect(screen.getByText('1 个主星 · 2 个伴星')).toBeInTheDocument()
    expect(container.querySelector('.swarm-node-card')).toHaveTextContent('主星A')
    expect(openSession).not.toHaveBeenCalled()
    expect(api.getBlackboard).toHaveBeenCalledWith('swarm:agent-1::a')
    queryClient.clear()
  })

  it('renders multi-level hierarchy A -> B -> C and excludes undelegated sessions', async () => {
    const sessionA = {
      id: 'session-a',
      agent_id: 'agent-1',
      title: '普通标题主星A',
      workspace: '/repo',
      status: 'running' as const,
      updated_at: '2026-10-14T09:00:00Z',
    }
    const sessionB = {
      id: 'session-b',
      agent_id: 'agent-1',
      title: '委派伴星B',
      workspace: '/repo',
      status: 'running' as const,
      updated_at: '2026-10-14T09:05:00Z',
      delegation_parent_agent_id: 'agent-1',
      delegation_parent_session_id: 'session-a',
    }
    const sessionC = {
      id: 'session-c',
      agent_id: 'agent-1',
      title: '二级委派伴星C',
      workspace: '/repo',
      status: 'idle' as const,
      updated_at: '2026-10-14T09:10:00Z',
      delegation_parent_agent_id: 'agent-1',
      delegation_parent_session_id: 'session-b',
    }
    const forkedSession = {
      id: 'session-forked',
      agent_id: 'agent-1',
      title: '仅Fork来源会话',
      workspace: '/repo',
      status: 'idle' as const,
      updated_at: '2026-10-14T09:15:00Z',
      parent_session_id: 'session-a',
    }

    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <MantineProvider>
        <QueryClientProvider client={queryClient}>
          <SwarmDagView
            allSessions={[sessionA, sessionB, sessionC, forkedSession]}
            agents={{ [agent.id]: agent }}
            activeRootKey="agent-1::session-a"
            onOpenSession={vi.fn()}
          />
        </QueryClientProvider>
      </MantineProvider>,
    )

    // A、B、C 节点均呈现在画布中
    expect(screen.getAllByText('普通标题主星A').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText('委派伴星B')).toBeInTheDocument()
    expect(screen.getByText('二级委派伴星C')).toBeInTheDocument()
    // 仅具有 parent_session_id 的 fork 会话不入图
    expect(screen.queryByText('仅Fork来源会话')).not.toBeInTheDocument()
    // 顶部状态栏显示 1 个主星 · 2 个伴星
    expect(screen.getByText('1 个主星 · 2 个伴星')).toBeInTheDocument()
  })
})
