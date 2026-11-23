import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, type IntegrationItem } from '../../api/client'
import { Sidebar } from '../../components/Sidebar'
import { SIDEBAR_INTEGRATIONS_PLUGIN_ID, usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { IntegrationPage } from './IntegrationPage'
import { IntegrationsSettingsPage } from './IntegrationsSettingsPage'
import { useIntegrationsStore } from './integrationStore'

const sampleItem: IntegrationItem = {
  id: 'intg-magpie',
  name: 'Magpie 网关',
  url: 'http://192.168.1.100:3430/#usage',
  icon_type: 'auto',
  icon_value: 'data:image/svg+xml;base64,PHN2Zy8+',
  auto_icon_value: 'data:image/svg+xml;base64,PHN2Zy8+',
  open_mode: 'embed',
  visible: true,
  order: 0,
  created_at: '2026-11-22T00:00:00Z',
  updated_at: '2026-11-22T00:00:00Z',
}

beforeEach(() => {
  vi.spyOn(api, 'getAnalyticsUsage').mockResolvedValue({ range: 'today', summary: { requests: 0, totalTokens: 0 }, models: [], days: [] } as never)
  vi.spyOn(api, 'listIntegrations').mockResolvedValue({ items: [sampleItem] })
  usePluginSettingsStore.getState().reset()
  useIntegrationsStore.getState().reset()
  useIntegrationsStore.setState({ items: [sampleItem], hydrated: true })
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  delete (window as unknown as { astrorderDesktop?: unknown }).astrorderDesktop
})

describe('Web Integrations plugin', () => {
  it('embeds via native top-level webview on desktop and hides when modal dialogs open', async () => {
    const showEmbeddedPage = vi.fn().mockResolvedValue(undefined)
    const hideEmbeddedPage = vi.fn().mockResolvedValue(undefined)
    ;(window as unknown as { astrorderDesktop: unknown }).astrorderDesktop = { showEmbeddedPage, hideEmbeddedPage }

    const view = render(
      <MantineProvider>
        <MemoryRouter initialEntries={['/integrations/intg-magpie']}>
          <Routes>
            <Route path="/integrations/:integrationId" element={<IntegrationPage />} />
          </Routes>
        </MemoryRouter>
      </MantineProvider>,
    )

    expect(await screen.findByTitle('Magpie 网关')).toBeInTheDocument()
    expect(document.querySelector('.route-page.integration-embedded-page')).toBeInTheDocument()
    expect(showEmbeddedPage).toHaveBeenCalledWith(
      sampleItem.url,
      expect.objectContaining({ width: expect.any(Number), height: expect.any(Number) }),
      false,
    )
    expect(screen.getByTitle('Magpie 网关').tagName).not.toBe('IFRAME')

    const dialog = document.createElement('div')
    dialog.setAttribute('role', 'dialog')
    document.body.append(dialog)
    await waitFor(() => expect(hideEmbeddedPage).toHaveBeenCalled())
    const shownBefore = showEmbeddedPage.mock.calls.length
    dialog.remove()
    await waitFor(() => expect(showEmbeddedPage.mock.calls.length).toBeGreaterThan(shownBefore))
    view.unmount()
    expect(hideEmbeddedPage).toHaveBeenCalled()
  })

  it('falls back to iframe in browser mode and renders sidebar links + add button when plugin enabled', () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <MantineProvider>
        <QueryClientProvider client={client}>
          <MemoryRouter initialEntries={['/integrations/intg-magpie']}>
            <Sidebar sessions={[]} agents={{}} onSelectSession={vi.fn()} />
            <Routes>
              <Route path="/integrations/:integrationId" element={<IntegrationPage />} />
            </Routes>
          </MemoryRouter>
        </QueryClientProvider>
      </MantineProvider>,
    )

    expect(screen.getByRole('link', { name: 'Magpie 网关' })).toHaveAttribute('href', '/integrations/intg-magpie')
    expect(screen.getByRole('button', { name: '添加集成' })).toBeInTheDocument()
    expect(screen.getByTitle('Magpie 网关')).toHaveAttribute('src', sampleItem.url)

    // Disabling the plugin hides sidebar links and + button immediately
    act(() => {
      usePluginSettingsStore.setState({ enabledPlugins: { [SIDEBAR_INTEGRATIONS_PLUGIN_ID]: false } })
    })
    expect(screen.queryByRole('link', { name: 'Magpie 网关' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '添加集成' })).not.toBeInTheDocument()
    client.clear()
  })

  it('toggles visibility in IntegrationsSettingsPage via backend API', async () => {
    const updateSpy = vi.spyOn(api, 'updateIntegration').mockResolvedValue({ ...sampleItem, visible: false })
    render(
      <MantineProvider>
        <IntegrationsSettingsPage />
      </MantineProvider>,
    )
    const toggle = await screen.findByRole('switch', { name: '在侧边栏显示 Magpie 网关' })
    fireEvent.click(toggle)
    await waitFor(() => expect(updateSpy).toHaveBeenCalledWith('intg-magpie', { visible: false }))
  })

  it('supports icon-only sidebar items (empty name) and drag-and-drop reordering in Sidebar', async () => {
    const iconOnlyItem: IntegrationItem = {
      ...sampleItem,
      id: 'intg-icon-only',
      name: '',
      url: 'https://grafana.example.com',
      order: 1,
    }
    useIntegrationsStore.setState({ items: [sampleItem, iconOnlyItem], hydrated: true })
    const reorderSpy = vi.spyOn(api, 'reorderIntegrations').mockResolvedValue({
      items: [{ ...iconOnlyItem, order: 0 }, { ...sampleItem, order: 1 }],
    })

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <MantineProvider>
        <QueryClientProvider client={client}>
          <MemoryRouter initialEntries={['/chat']}>
            <Sidebar sessions={[]} agents={{}} onSelectSession={vi.fn()} />
          </MemoryRouter>
        </QueryClientProvider>
      </MantineProvider>,
    )

    const secondLink = screen.getByRole('link', { name: 'grafana.example.com' })
    expect(secondLink).toHaveClass('is-icon-only')
    expect(secondLink.textContent?.trim()).toBe('')

    const firstLink = screen.getByRole('link', { name: 'Magpie 网关' })
    const transfer = new Map<string, string>()
    const dataTransfer = {
      effectAllowed: 'move',
      dropEffect: 'move',
      types: ['application/x-astrorder-integration'],
      setData: (k: string, v: string) => transfer.set(k, v),
      getData: (k: string) => transfer.get(k) || '',
    }
    fireEvent.dragStart(secondLink, { dataTransfer })
    fireEvent.dragOver(firstLink, { dataTransfer, clientY: 0 })
    fireEvent.drop(firstLink, { dataTransfer, clientY: 0 })

    await waitFor(() => expect(reorderSpy).toHaveBeenCalledWith(['intg-icon-only', 'intg-magpie']))
    client.clear()
  })
})
