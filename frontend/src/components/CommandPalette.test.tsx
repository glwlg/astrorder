import { MantineProvider } from '@mantine/core'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../api/client'
import { useAstrorderStore } from '../state/store'
import { CommandPalette } from './CommandPalette'

function LocProbe() {
  const loc = useLocation()
  return <output data-testid="palette-loc">{`${loc.pathname}${loc.search}`}</output>
}

describe('CommandPalette', () => {
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
    useAstrorderStore.getState().resetRuntime()
  })

  it('filters sessions, routes, and dispatches background commands via quick-send mode', async () => {
    const createCommand = vi.spyOn(api, 'createCommand').mockImplementation(async (payload) => ({
      ...payload,
      state: 'accepted',
      attachments: [],
      created_at: '2026-11-21T09:00:00Z',
      error: null,
    }))
    useAstrorderStore.getState().hydrateBootstrap({
      protocol_version: 1,
      cursor: 1,
      agents: [{ id: 'codex', name: 'Codex', kind: 'codex', status: 'ready', capabilities: ['chat'], limitation: null }],
      sessions: [
        { id: 's-ops', agent_id: 'codex', title: 'OpsCore 灰度检查', project_name: 'OpsCore', workspace: '/opscore', status: 'running', updated_at: '2026-11-21T09:00:00Z' },
      ],
    })
    const onClose = vi.fn()
    render(
      <MantineProvider>
        <MemoryRouter initialEntries={['/chat']}>
          <CommandPalette opened onClose={onClose} onToggleSidebar={vi.fn()} onOpenSettings={vi.fn()} />
          <LocProbe />
        </MemoryRouter>
      </MantineProvider>,
    )

    const input = screen.getByRole('combobox')
    await userEvent.type(input, '灰度')
    expect(screen.getByText('OpsCore 灰度检查')).toBeInTheDocument()

    // Enter quick-send mode via Tab and send background command
    await userEvent.keyboard('{Tab}')
    const dispatchInput = screen.getByLabelText('输入后台指令内容')
    await userEvent.type(dispatchInput, '补充单测并输出摘要{Enter}')

    await waitFor(() =>
      expect(createCommand).toHaveBeenCalledWith(
        expect.objectContaining({
          agent_id: 'codex',
          session_id: 's-ops',
          action: 'send',
          text: '补充单测并输出摘要',
        }),
      ),
    )
    expect(onClose).toHaveBeenCalledOnce()
    // Did not navigate away from /chat
    expect(screen.getByTestId('palette-loc')).toHaveTextContent('/chat')
  })

  it('scrolls the active option into view when navigating down with ArrowDown', async () => {
    const scrollSpy = vi.spyOn(HTMLElement.prototype, 'scrollIntoView').mockImplementation(() => {})
    useAstrorderStore.getState().hydrateBootstrap({
      protocol_version: 1,
      cursor: 1,
      agents: [{ id: 'codex', name: 'Codex', kind: 'codex', status: 'ready', capabilities: ['chat'], limitation: null }],
      sessions: Array.from({ length: 8 }, (_, idx) => ({
        id: `s-${idx}`,
        agent_id: 'codex',
        title: `任务会话 ${idx + 1}`,
        project_name: 'astrorder',
        workspace: '/astrorder',
        status: 'idle' as const,
        updated_at: `2026-11-21T09:0${idx}:00Z`,
      })),
    })

    render(
      <MantineProvider>
        <MemoryRouter initialEntries={['/chat']}>
          <CommandPalette opened onClose={vi.fn()} onToggleSidebar={vi.fn()} onOpenSettings={vi.fn()} />
        </MemoryRouter>
      </MantineProvider>,
    )

    const input = screen.getByRole('combobox')
    scrollSpy.mockClear()
    await userEvent.type(input, '{ArrowDown}{ArrowDown}{ArrowDown}')
    expect(scrollSpy).toHaveBeenCalledWith({ block: 'nearest' })
    expect(input).toHaveAttribute('aria-activedescendant', 'command-palette-opt-3')
  })
})
