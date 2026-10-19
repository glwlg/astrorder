import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { MobileWorkspace } from './MobileWorkspace'
import { useAstrorderStore } from '../../state/store'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

beforeEach(() => {
  useAstrorderStore.setState({
    sessions: {},
    agents: {},
    connection: 'connected',
  })
})

describe('MobileWorkspace Kernel Management Menu Integration', () => {
  it('provides "内核管理" in settings menu and opens kernel management sheet', async () => {
    vi.spyOn(api, 'getOpenSessions').mockResolvedValue({ known_agent_ids: [], items: [] })
    vi.spyOn(api, 'getKernelStatus').mockResolvedValue({
      app: { state: 'running', healthy: true },
      daemon: { state: 'running' },
      update: { update_available: false },
    })

    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <MantineProvider>
        <QueryClientProvider client={queryClient}>
          <MemoryRouter initialEntries={['/mobile']}>
            <MobileWorkspace />
          </MemoryRouter>
        </QueryClientProvider>
      </MantineProvider>,
    )

    const settingsBtn = await screen.findByRole('button', { name: '应用设置' })
    fireEvent.click(settingsBtn)

    const kernelMenuItem = await screen.findByRole('menuitem', { name: '内核管理' })
    expect(kernelMenuItem).toBeInTheDocument()

    fireEvent.click(kernelMenuItem)

    expect(await screen.findByRole('heading', { name: '内核管理' })).toBeInTheDocument()
    expect(screen.getByText('内核运行时状态')).toBeInTheDocument()
  })
})
