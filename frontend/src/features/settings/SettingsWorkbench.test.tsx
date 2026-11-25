import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { normalizeSettingsTab, StandaloneSettingsPage } from './SettingsWorkbench'

vi.mock('./GeneralSettingsPage', () => ({ GeneralSettingsPage: () => <div>GENERAL_TAB_PANEL</div> }))
vi.mock('../agents/AgentsPage', () => ({ AgentsPage: () => <div>AGENTS_TAB_PANEL</div> }))
vi.mock('../services/ModelGatewaySettingsPage', () => ({ ModelGatewaySettingsPage: () => <div>MODELS_TAB_PANEL</div> }))
vi.mock('../services/CapabilitiesSettingsCard', () => ({ CapabilitiesSettingsCard: () => <div>SKILLS_TAB_PANEL</div> }))
vi.mock('../rtk/RtkSettingsPage', () => ({ RtkSettingsPage: () => <div>RTK_TAB_PANEL</div> }))
vi.mock('../network/NetworkSettingsPage', () => ({ NetworkSettingsPage: () => <div>NETWORK_TAB_PANEL</div> }))
vi.mock('../shortcuts/ShortcutSettingsPage', () => ({ ShortcutSettingsPage: () => <div>SHORTCUTS_TAB_PANEL</div> }))
vi.mock('../integrations/IntegrationsSettingsPage', () => ({ IntegrationsSettingsPage: () => <div>INTEGRATIONS_TAB_PANEL</div> }))
vi.mock('../plugins/PluginsPage', () => ({ PluginsPage: () => <div>PLUGINS_TAB_PANEL</div> }))

vi.mock('../plugins/pluginSettingsStore', () => {
  const state = {
    hydrate: vi.fn().mockResolvedValue(undefined),
    isPluginEnabled: () => true,
  }
  const hook = (selector?: (s: typeof state) => unknown) => (selector ? selector(state) : state)
  hook.getState = () => state
  return {
    SIDEBAR_INTEGRATIONS_PLUGIN_ID: 'sidebar-integrations',
    COMMAND_COMPRESSION_PLUGIN_ID: 'command-compression',
    usePluginSettingsStore: hook,
  }
})

vi.mock('../integrations/integrationStore', () => {
  const state = {
    hydrate: vi.fn().mockResolvedValue(undefined),
  }
  const hook = (selector?: (s: typeof state) => unknown) => (selector ? selector(state) : state)
  hook.getState = () => state
  return { useIntegrationsStore: hook }
})

afterEach(() => {
  cleanup()
  localStorage.clear()
  delete (window as unknown as { astrorderDesktop?: unknown }).astrorderDesktop
})

beforeEach(() => {
  localStorage.clear()
  vi.clearAllMocks()
})

describe('SettingsWorkbench & StandaloneSettingsPage (basic vs advanced mode)', () => {
  it('normalizes valid, aliased, and invalid tab names safely', () => {
    expect(normalizeSettingsTab('skills')).toBe('skills')
    expect(normalizeSettingsTab(' appearance ')).toBe('general')
    expect(normalizeSettingsTab('desktop')).toBe('general')
    expect(normalizeSettingsTab('services')).toBe('models')
    expect(normalizeSettingsTab('unknown')).toBe('general')
    expect(normalizeSettingsTab(null)).toBe('general')
  })

  it('defaults to basic mode hiding developer tabs and lets users toggle advanced mode cleanly', async () => {
    render(
      <MantineProvider>
        <MemoryRouter initialEntries={['/settings']}>
          <StandaloneSettingsPage />
        </MemoryRouter>
      </MantineProvider>,
    )

    await waitFor(() => {
      expect(screen.getByText('GENERAL_TAB_PANEL')).toBeInTheDocument()
    })

    // In default basic mode, office-friendly tabs are visible while dev tabs are hidden
    expect(screen.getByRole('tab', { name: '常规与外观' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'AI 供应商' })).toBeInTheDocument()
    expect(screen.queryByRole('tab', { name: '环境与 Agent 连接' })).toBeNull()
    expect(screen.queryByRole('tab', { name: '命令输出压缩' })).toBeNull()
    expect(screen.queryByRole('tab', { name: '网络与远程访问' })).toBeNull()

    // Expand advanced configuration reveals developer tabs
    fireEvent.click(screen.getByRole('button', { name: /展开高级与开发配置/ }))
    expect(screen.getByRole('tab', { name: '环境与 Agent 连接' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: '能力中心' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: '命令输出压缩' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: '网络与远程访问' })).toBeInTheDocument()

    // Switching to an advanced tab and then back to basic mode safely returns to general
    fireEvent.click(screen.getByRole('tab', { name: '网络与远程访问' }))
    await waitFor(() => {
      expect(screen.getByText('NETWORK_TAB_PANEL')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByText('一般配置', { selector: 'span' }))
    await waitFor(() => {
      expect(screen.getByText('GENERAL_TAB_PANEL')).toBeInTheDocument()
      expect(screen.queryByRole('tab', { name: '网络与远程访问' })).toBeNull()
    })
  })

  it('auto-enables advanced mode on deep link to advanced tab and returns to main window via desktop bridge', async () => {
    const focusMain = vi.fn().mockResolvedValue(undefined)
    ;(window as unknown as { astrorderDesktop?: { focusMain: typeof focusMain } }).astrorderDesktop = {
      focusMain,
    }

    render(
      <MantineProvider>
        <MemoryRouter initialEntries={['/settings?tab=skills']}>
          <StandaloneSettingsPage />
        </MemoryRouter>
      </MantineProvider>,
    )

    await waitFor(() => {
      expect(screen.getByText('SKILLS_TAB_PANEL')).toBeInTheDocument()
    })
    expect(screen.getByRole('tab', { name: '能力中心' })).toBeInTheDocument()

    // External trigger switches active tab without remounting the window
    fireEvent(window, new CustomEvent('astrorder:settings-tab', { detail: { tab: 'appearance' } }))
    await waitFor(() => {
      expect(screen.getByText('GENERAL_TAB_PANEL')).toBeInTheDocument()
    })

    // Clicking "返回主窗口" focuses the main OS window
    fireEvent.click(screen.getByRole('button', { name: /返回主窗口/ }))
    expect(focusMain).toHaveBeenCalledWith('')
  })
})
