import { MantineProvider } from '@mantine/core'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../api/client'
import { clearStoredAppProfileForTest, setAppProfile } from '../features/office/appProfile'
import { extensionRegistry } from '../features/extensions/extensionRegistry'
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
    clearStoredAppProfileForTest()
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

  it('hides Bot and Learning Radar routes and uses human-friendly copy in global office profile', async () => {
    setAppProfile('office', { syncPluginStore: false })

    render(
      <MantineProvider>
        <MemoryRouter initialEntries={['/chat']}>
          <CommandPalette opened onClose={vi.fn()} onToggleSidebar={vi.fn()} onOpenSettings={vi.fn()} />
        </MemoryRouter>
      </MantineProvider>,
    )

    expect(screen.getByRole('dialog', { name: '全局搜索' })).toBeInTheDocument()
    expect(screen.queryByRole('dialog', { name: '星序全能星令面板' })).toBeNull()

    const input = screen.getByRole('combobox')
    await userEvent.type(input, 'Bot')
    expect(screen.queryByText('Bot 工作台')).toBeNull()

    await userEvent.clear(input)
    await userEvent.type(input, '成长')
    expect(screen.queryByText(/成长雷达/)).toBeNull()

    await userEvent.clear(input)
    await userEvent.type(input, '会议')
    expect(screen.getByText('会议纪要与录音')).toBeInTheDocument()
  })

  it('includes dynamically registered extension pages and commands and executes them', async () => {
    const onRunCommand = vi.fn()
    const onClose = vi.fn()
    const pageDisposable = extensionRegistry.registerPage({
      pluginId: 'acme-tools',
      id: 'studio',
      title: '星序插件工坊',
      order: 210,
      component: () => <div>studio</div>,
      source: 'esm',
    })
    const cmdDisposable = extensionRegistry.registerCommand({
      pluginId: 'acme-tools',
      id: 'export-bundle',
      title: '导出插件诊断包',
      keywords: 'diagnostics export bundle',
      run: onRunCommand,
      source: 'esm',
    })

    try {
      render(
        <MantineProvider>
          <MemoryRouter initialEntries={['/chat']}>
            <CommandPalette opened onClose={onClose} onToggleSidebar={vi.fn()} onOpenSettings={vi.fn()} />
            <LocProbe />
          </MemoryRouter>
        </MantineProvider>,
      )

      const input = screen.getByRole('combobox')
      await userEvent.type(input, '插件工坊')
      expect(screen.getByText('星序插件工坊')).toBeInTheDocument()
      await userEvent.click(screen.getByText('星序插件工坊'))
      expect(screen.getByTestId('palette-loc')).toHaveTextContent('/ext-page/acme-tools/studio')

      await userEvent.clear(input)
      await userEvent.type(input, '诊断包')
      expect(screen.getByText('导出插件诊断包')).toBeInTheDocument()
      await userEvent.click(screen.getByText('导出插件诊断包'))
      expect(onRunCommand).toHaveBeenCalledOnce()
    } finally {
      pageDisposable.dispose()
      cmdDisposable.dispose()
    }
  })
})
