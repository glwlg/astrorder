import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, createEvent, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { SwarmDagView } from './SwarmDagView'

vi.mock('./MonitorPage', () => ({
  MonitorCard: () => <div role="log" style={{ height: 160, overflowY: 'auto' }}><div style={{ height: 1000 }}>会话消息</div></div>,
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
})
