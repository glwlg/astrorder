import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MantineProvider } from '@mantine/core'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import { userEvent } from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import App from './App'
import { extensionRegistry } from '../features/extensions/extensionRegistry'
import { useExtensionStore } from '../features/extensions/extensionStore'
import type { ExtensionPageComponentProps } from '../features/extensions/types'
import { useAstrorderStore } from '../state/store'

const emptyPreferences = { appearance: {}, session_pins: {}, pinned_projects: [], project_order: [] }

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

afterEach(() => { cleanup(); vi.restoreAllMocks(); useExtensionStore.getState().reset(); useAstrorderStore.getState().resetRuntime() })

describe('isolated browser API fixture scope — not production Agent data', () => {
  it('opens a mobile group notification directly in the group conversation', async () => {
    const group = { id: 'group-review', name: '授权复核讨论', members: [], max_hops: 10, coordination: { status: 'complete' }, created_at: '', updated_at: '' }
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = String(input)
      const json = (value: unknown) => new Response(JSON.stringify(value), { status: 200, headers: { 'Content-Type': 'application/json' } })
      if (url.endsWith('/auth/session')) return json({ authenticated: true })
      if (url.endsWith('/bootstrap')) return json({ protocol_version: 1, agents: [], sessions: [], cursor: 0 })
      if (url.endsWith('/preferences')) return json(emptyPreferences)
      if (url.endsWith('/bot-groups')) return json({ items: [group], count: 1 })
      if (url.endsWith('/bot-groups/group-review')) return json({ group })
      if (url.endsWith('/bot-groups/group-review/messages')) return json({ items: [{ id: 'group-reply', group_id: group.id, sender_type: 'user', sender_id: 'human', text: '群里的最终回复', mentions: [], hop_count: 0, created_at: '' }], count: 1 })
      return json({ items: [] })
    })
    vi.stubGlobal('WebSocket', class { close() {} })
    renderApp(['/mobile/groups/group-review?view=mobile'])
    expect(await screen.findByText('群里的最终回复', {}, { timeout: 3000 })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '对话' })).toBeInTheDocument()
    expect(screen.queryByText('还没有可用会话')).not.toBeInTheDocument()
  })
  it('shows the authentication screen for an unauthenticated browser', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ authenticated: false }), { status: 200, headers: { 'Content-Type': 'application/json' } }),
    )

    renderApp()

    expect(await screen.findByRole('heading', { name: '登录星序工作台' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('radio', { name: '口令登录' }))
    expect(screen.getByLabelText('登录口令', { selector: 'input[type="password"]' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '立即登录' })).toBeInTheDocument()
    expect(screen.queryByText(/模拟 Agent/)).not.toBeInTheDocument()
    expect(screen.queryByText('建立会话')).not.toBeInTheDocument()
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
      if (url.endsWith('/preferences')) {
        return new Response(JSON.stringify(emptyPreferences), { status: 200, headers: { 'Content-Type': 'application/json' } })
      }
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    })
    vi.stubGlobal('WebSocket', class {
      close() {}
    })

    renderApp()

    expect(await screen.findByRole('button', { name: '一键配置 AI 助手' }, { timeout: 3000 })).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('link', { name: /矩阵/ })).toBeInTheDocument())
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
      if (url.endsWith('/preferences')) return json(emptyPreferences)
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
    await userEvent.click(screen.getAllByRole('link', { name: /矩阵/ })[0])
    expect(await screen.findByRole('heading', { name: '矩阵' })).toBeInTheDocument()
    expect(screen.getByTestId('monitor-card-agent-b-shared')).toHaveTextContent('会话 B')
    expect(screen.getByRole('link', { name: '会话' })).toHaveAttribute('href', '/chat/shared?agent_id=agent-b')
    await userEvent.click(screen.getByRole('link', { name: '会话' }))
    expect(await screen.findByRole('heading', { name: '会话 B', level: 2 })).toBeInTheDocument()
    expect(await screen.findByText('来自 B')).toBeInTheDocument()
    expect(screen.queryByText('来自 A')).not.toBeInTheDocument()
  })

  it('redirects PC desktop viewport away from /mobile route to desktop workspace', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = String(input)
      if (url.endsWith('/auth/session')) {
        return new Response(JSON.stringify({ authenticated: true }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      }
      if (url.endsWith('/bootstrap')) {
        return new Response(JSON.stringify({ protocol_version: 1, agents: [], sessions: [], cursor: 0 }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      }
      if (url.endsWith('/preferences')) {
        return new Response(JSON.stringify(emptyPreferences), { status: 200, headers: { 'Content-Type': 'application/json' } })
      }
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    })
    vi.stubGlobal('WebSocket', class {
      close() {}
    })

    renderApp(['/mobile'])

    // On PC wide viewport (default jsdom is desktop), /mobile redirects to /chat
    expect(await screen.findByRole('button', { name: '一键配置 AI 助手' })).toBeInTheDocument()
    await waitFor(() => expect(screen.getAllByRole('link', { name: /矩阵/ })[0]).toBeInTheDocument())
    expect(screen.queryByRole('heading', { name: '移动工作台' })).not.toBeInTheDocument()
  })

  it('mounts dynamically registered ESM extension pages on /ext-page/:pluginId/:pageId with error isolation', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = String(input)
      const json = (value: unknown) => new Response(JSON.stringify(value), { status: 200, headers: { 'Content-Type': 'application/json' } })
      if (url.endsWith('/auth/session')) return json({ authenticated: true })
      if (url.endsWith('/bootstrap')) return json({ protocol_version: 1, agents: [], sessions: [], cursor: 0 })
      if (url.endsWith('/preferences')) return json(emptyPreferences)
      return json({ items: [] })
    })
    vi.stubGlobal('WebSocket', class { close() {} })

    const okPage = extensionRegistry.registerPage({
      pluginId: 'demo-esm',
      id: 'overview',
      title: '扩展总览页',
      navLabel: '总览',
      component: ({ pluginId, pageId, surface }: ExtensionPageComponentProps) => (
        <div data-testid="dynamic-ext-page">{`ESM:${pluginId}/${pageId}/${surface}`}</div>
      ),
      source: 'esm',
    })
    const crashPage = extensionRegistry.registerPage({
      pluginId: 'demo-esm',
      id: 'broken',
      title: '故障扩展页',
      component: () => {
        throw new Error('dynamic page exploded')
      },
      source: 'esm',
    })

    try {
      renderApp(['/ext-page/demo-esm/overview'])
      expect(await screen.findByTestId('dynamic-ext-page', {}, { timeout: 3000 })).toHaveTextContent(
        'ESM:demo-esm/overview/desktop',
      )

      cleanup()
      vi.spyOn(console, 'error').mockImplementation(() => {})
      renderApp(['/ext-page/demo-esm/broken'])
      expect(await screen.findByText('故障扩展页 渲染异常', {}, { timeout: 3000 })).toBeInTheDocument()
      expect(screen.getByText('dynamic page exploded')).toBeInTheDocument()
    } finally {
      okPage.dispose()
      crashPage.dispose()
    }
  })

  it('waits for enabled ESM plugin activation on /ext-page/:pluginId/:pageId without flashing unavailable', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = String(input)
      const json = (value: unknown) => new Response(JSON.stringify(value), { status: 200, headers: { 'Content-Type': 'application/json' } })
      if (url.endsWith('/auth/session')) return json({ authenticated: true })
      if (url.endsWith('/bootstrap')) return json({ protocol_version: 1, agents: [], sessions: [], cursor: 0 })
      if (url.endsWith('/preferences')) return json(emptyPreferences)
      if (url.endsWith('/extensions')) {
        return json({
          items: [
            {
              id: 'async-esm',
              name: '异步扩展',
              version: '1.0.0',
              category: 'workspace',
              source: 'official',
              enabled: true,
              permissions_granted: true,
              runtime_state: 'ready',
              backend_type: 'none',
              frontend: { mode: 'esm', entry: 'ui/entry.js' },
            },
          ],
        })
      }
      return json({ items: [] })
    })
    vi.stubGlobal('WebSocket', class { close() {} })

    useExtensionStore.setState({
      hydrated: true,
      items: [
        {
          id: 'async-esm',
          name: '异步扩展',
          version: '1.0.0',
          category: 'workspace',
          source: 'official',
          enabled: true,
          permissions_granted: true,
          runtime_state: 'ready',
          backend_type: 'none',
          frontend: { mode: 'esm', entry: 'ui/entry.js' },
        },
      ],
      esmStates: {
        'async-esm': { status: 'loading', updatedAt: Date.now() },
      },
    })

    const disposables: Array<{ dispose(): void }> = []
    try {
      renderApp(['/ext-page/async-esm/board'])
      await waitFor(() => expect(screen.queryByRole('status', { name: '正在加载工作台…' })).not.toBeInTheDocument())
      expect(screen.queryByText('扩展页面暂不可用')).not.toBeInTheDocument()

      act(() => {
        disposables.push(
          extensionRegistry.registerPage({
            pluginId: 'async-esm',
            id: 'board',
            title: '异步扩展看板',
            component: () => <div data-testid="async-esm-ready">Async ESM Ready</div>,
            source: 'esm',
          }),
        )
        useExtensionStore.getState().setEsmRuntimeState('async-esm', { status: 'loaded', updatedAt: Date.now() })
      })

      expect(await screen.findByTestId('async-esm-ready')).toHaveTextContent('Async ESM Ready')
    } finally {
      for (const d of disposables) d.dispose()
    }
  })
})
