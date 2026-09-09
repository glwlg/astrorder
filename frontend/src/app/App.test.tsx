import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MantineProvider } from '@mantine/core'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import App from './App'

function renderApp(initialEntries = ['/']) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <MemoryRouter initialEntries={initialEntries}>
          <App />
        </MemoryRouter>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

afterEach(() => vi.restoreAllMocks())

describe('isolated browser API fixture scope — not production Agent data', () => {
  it('shows the authentication screen for an unauthenticated browser', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ authenticated: false }), { status: 200, headers: { 'Content-Type': 'application/json' } }),
    )

    renderApp()

    expect(await screen.findByRole('heading', { name: '登录本地工作台' })).toBeInTheDocument()
    expect(screen.getByLabelText('访问令牌')).toBeInTheDocument()
    expect(screen.queryByText('尚无会话')).not.toBeInTheDocument()
  })

  it('renders an authenticated empty workspace without inventing an Agent', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = String(input)
      if (url.endsWith('/auth/session')) {
        return new Response(JSON.stringify({ authenticated: true }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      }
      if (url.endsWith('/bootstrap')) {
        return new Response(JSON.stringify({ protocol_version: 1, agents: [], sessions: [], cursor: 0 }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      }
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    })
    vi.stubGlobal('WebSocket', class {
      close() {}
    })

    renderApp()

    expect(await screen.findByText('还没有可用会话')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('link', { name: /监控室/ })).toBeInTheDocument())
    expect(screen.queryByText('Hermes')).not.toBeInTheDocument()
  })

  it('keeps an authenticated transcript shared with monitor routing and scoped by agent', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = String(input)
      const json = (value: unknown) => new Response(JSON.stringify(value), { status: 200, headers: { 'Content-Type': 'application/json' } })
      if (url.endsWith('/auth/session')) return json({ authenticated: true })
      if (url.endsWith('/bootstrap')) {
        return json({
          protocol_version: 1,
          cursor: 4,
          agents: [
            { id: 'agent-a', kind: 'hermes', name: 'Agent A', status: 'ready', capabilities: ['chat', 'events'], limitation: null },
            { id: 'agent-b', kind: 'codex', name: 'Agent B', status: 'ready', capabilities: ['chat', 'events'], limitation: null },
          ],
          sessions: [
            { id: 'shared', agent_id: 'agent-a', title: '会话 A', workspace: '/safe/a', status: 'idle', updated_at: '2026-01-01T00:00:00Z' },
            { id: 'shared', agent_id: 'agent-b', title: '会话 B', workspace: '/safe/b', status: 'running', updated_at: '2026-01-02T00:00:00Z' },
          ],
        })
      }
      if (url.includes('/messages?') && url.includes('agent_id=agent-b')) {
        return json({ next_cursor: null, items: [{ id: 'message-b', session_id: 'shared', agent_id: 'agent-b', role: 'assistant', kind: 'message', text: '来自 B', attachments: [], created_at: '2026-01-02T00:00:00Z', command_id: null, tool: null }] })
      }
      if (url.includes('/messages?')) {
        return json({ next_cursor: null, items: [{ id: 'message-a', session_id: 'shared', agent_id: 'agent-a', role: 'assistant', kind: 'message', text: '来自 A', attachments: [], created_at: '2026-01-01T00:00:00Z', command_id: null, tool: null }] })
      }
      return json({ items: [] })
    })
    vi.stubGlobal('WebSocket', class {
      close() {}
    })

    renderApp(['/chat/shared?agent_id=agent-b'])

    expect(await screen.findByText('来自 B')).toBeInTheDocument()
    expect(screen.queryByText('来自 A')).not.toBeInTheDocument()
    expect(screen.getByRole('heading', { name: '会话 B', level: 2 })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: '会话 B', level: 2 }).closest('.chat-heading')).toHaveTextContent('Codex')
    await screen.getAllByRole('link', { name: /监控室/ })[0].click()
    expect(await screen.findByRole('heading', { name: '监控室' })).toBeInTheDocument()
    expect(screen.getByTestId('monitor-card-agent-b-shared')).toHaveTextContent('会话 B')
  })
})
