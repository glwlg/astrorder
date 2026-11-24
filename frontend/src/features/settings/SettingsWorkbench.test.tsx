import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { normalizeSettingsTab, StandaloneSettingsPage } from './SettingsWorkbench'

vi.mock('../agents/AgentsPage', () => ({ AgentsPage: () => <div>AGENTS_TAB_PANEL</div> }))
vi.mock('../services/ModelGatewaySettingsPage', () => ({ ModelGatewaySettingsPage: () => <div>MODELS_TAB_PANEL</div> }))
vi.mock('../services/CapabilitiesSettingsCard', () => ({ CapabilitiesSettingsCard: () => <div>SKILLS_TAB_PANEL</div> }))
vi.mock('../rtk/RtkSettingsPage', () => ({ RtkSettingsPage: () => <div>RTK_TAB_PANEL</div> }))
vi.mock('../network/NetworkSettingsPage', () => ({ NetworkSettingsPage: () => <div>NETWORK_TAB_PANEL</div> }))
vi.mock('../shortcuts/ShortcutSettingsPage', () => ({ ShortcutSettingsPage: () => <div>SHORTCUTS_TAB_PANEL</div> }))
vi.mock('../appearance/AppearanceSettingsPage', () => ({ AppearanceSettingsPage: () => <div>APPEARANCE_TAB_PANEL</div> }))
vi.mock('../services/DesktopSettingsPage', () => ({ DesktopSettingsPage: () => <div>DESKTOP_TAB_PANEL</div> }))
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
  delete (window as unknown as { astrorderDesktop?: unknown }).astrorderDesktop
})

beforeEach(() => {
  vi.clearAllMocks()
})

describe('SettingsWorkbench & StandaloneSettingsPage', () => {
  it('normalizes valid and invalid tab names safely', () => {
    expect(normalizeSettingsTab('skills')).toBe('skills')
    expect(normalizeSettingsTab(' appearance ')).toBe('appearance')
    expect(normalizeSettingsTab('unknown')).toBe('connections')
    expect(normalizeSettingsTab(null)).toBe('connections')
  })

  it('opens requested tab from URL, responds to astrorder:settings-tab events, and returns to main chat window via desktop bridge', async () => {
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

    // External trigger switches active tab without remounting the window
    fireEvent(window, new CustomEvent('astrorder:settings-tab', { detail: { tab: 'appearance' } }))
    await waitFor(() => {
      expect(screen.getByText('APPEARANCE_TAB_PANEL')).toBeInTheDocument()
    })

    // Clicking "回到会话窗口" focuses the main OS window
    fireEvent.click(screen.getByRole('button', { name: /回到会话窗口/ }))
    expect(focusMain).toHaveBeenCalledWith('')
  })
})
