import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, type OcxUsageResponse } from '../../api/client'
import type { Agent, Session } from '../../domain/types'
import { useAstrorderStore } from '../../state/store'
import { WelcomeView } from './WelcomeView'

function LocationProbe() {
  const location = useLocation()
  return <output data-testid="welcome-loc">{`${location.pathname}${location.search}`}</output>
}

const agents: Record<string, Agent> = {
  'codex-wsl': {
    id: 'codex-wsl',
    name: 'Codex',
    kind: 'codex',
    connection_id: 'ssh-wsl',
    status: 'ready',
    capabilities: ['chat', 'approvals'],
    limitation: null,
  },
}

const sessions: Session[] = [
  {
    id: 'sess-1',
    agent_id: 'codex-wsl',
    title: '历史重构会话',
    workspace: '/workspace/astrorder',
    project_name: 'astrorder',
    status: 'idle',
    updated_at: '2026-11-21T08:00:00Z',
  },
]

describe('WelcomeView Command Deck', () => {
  beforeEach(() => {
    vi.spyOn(api, 'getAnalyticsUsage').mockResolvedValue({
      range: 'today',
      summary: { requests: 18, totalTokens: 420_000 },
      models: [],
      days: [],
    } as unknown as OcxUsageResponse)
  })

  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
    useAstrorderStore.getState().resetRuntime()
  })

  function renderWelcome() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    return render(
      <MantineProvider>
        <QueryClientProvider client={client}>
          <MemoryRouter initialEntries={['/chat']}>
            <WelcomeView sessions={sessions} agents={agents} />
            <LocationProbe />
          </MemoryRouter>
        </QueryClientProvider>
      </MantineProvider>,
    )
  }

  it('creates a session, sends the prompt, and navigates on Omni-Composer submit', async () => {
    const createSession = vi.spyOn(api, 'createSession').mockResolvedValue({
      id: 'new-sess-9',
      agent_id: 'codex-wsl',
      title: '新任务',
      workspace: '/workspace/astrorder',
      project_name: 'astrorder',
      status: 'running',
      updated_at: '2026-11-21T09:00:00Z',
    })
    const createCommand = vi.spyOn(api, 'createCommand').mockImplementation(async (payload) => ({
      ...payload,
      state: 'accepted',
      attachments: [],
      created_at: '2026-11-21T09:00:01Z',
      error: null,
    }))

    renderWelcome()

    const textarea = screen.getByLabelText('输入新任务指令')
    await userEvent.type(textarea, '检查接口契约并补单测')
    await userEvent.click(screen.getByRole('button', { name: '启动任务' }))

    await waitFor(() =>
      expect(createSession).toHaveBeenCalledWith(
        expect.objectContaining({
          agent_id: 'codex-wsl',
          workspace: '/workspace/astrorder',
          project_name: 'astrorder',
        }),
      ),
    )
    expect(createCommand).toHaveBeenCalledWith(
      expect.objectContaining({
        agent_id: 'codex-wsl',
        session_id: 'new-sess-9',
        action: 'send',
        text: '检查接口契约并补单测',
      }),
    )
    expect(screen.getByTestId('welcome-loc')).toHaveTextContent('/chat/new-sess-9?agent_id=codex-wsl')
  })

  it('does not submit on Enter while IME composition is active (isComposing or keyCode 229)', async () => {
    const createSession = vi.spyOn(api, 'createSession')
    renderWelcome()
    const textarea = screen.getByLabelText('输入新任务指令')
    await userEvent.type(textarea, '拼音输入中')

    fireEvent.keyDown(textarea, { key: 'Enter', isComposing: true })
    fireEvent.keyDown(textarea, { key: 'Enter', keyCode: 229 })
    expect(createSession).not.toHaveBeenCalled()
  })

  it('shows today telemetry metrics and pending approval strip when present', async () => {
    useAstrorderStore.getState().hydrateBootstrap({
      protocol_version: 1,
      cursor: 1,
      agents: Object.values(agents),
      sessions,
      approvals: [
        {
          id: 'appr-deck',
          agent_id: 'codex-wsl',
          session_id: 'sess-1',
          title: '允许执行部署脚本',
          detail: 'deploy.ps1',
          state: 'pending',
          target_id: 't-1',
          data: {},
        },
      ],
    })

    renderWelcome()
    expect(await screen.findByText('42 万')).toBeInTheDocument()
    expect(screen.getByText('待你介入')).toBeInTheDocument()
    expect(screen.getByText('允许执行部署脚本')).toBeInTheDocument()
  })

  it('uses custom branded menus instead of native <select> elements for workspace and agent pickers', async () => {
    const { container } = renderWelcome()
    expect(container.querySelectorAll('select')).toHaveLength(0)

    const workspaceBtn = screen.getByRole('button', { name: '选择目标工作区' })
    await userEvent.click(workspaceBtn)
    await userEvent.click(await screen.findByRole('menuitem', { name: '不指定工作区' }))
    expect(workspaceBtn).toHaveTextContent('不指定工作区')
  })
})
