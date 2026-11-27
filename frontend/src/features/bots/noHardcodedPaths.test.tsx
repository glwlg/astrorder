import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { CapabilitiesSettingsCard } from '../services/CapabilitiesSettingsCard'
import { BotCreateModal } from './BotCreateModal'

describe('No hardcoded personal paths or external personal repos', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    vi.spyOn(api, 'getCapabilitiesTargets').mockResolvedValue([
      { id: 'local', kind: 'local', name: '本机环境' },
    ] as never)
    vi.spyOn(api, 'getCapabilitiesSummary').mockResolvedValue({
      target_id: 'local',
      target_name: '本机环境',
      kind: 'local',
      counts: { skills: 0, mcp: 0, plugins: 0, marketplaces: 0 },
      skill_agents: [{ id: 'codex', label: 'OpenAI Codex' }],
      skills: [],
      mcp_servers: [],
      plugins: [],
      marketplaces: [],
    } as never)
    vi.spyOn(api, 'listCapabilityDispatchJobs').mockResolvedValue({ jobs: [] } as never)
    vi.spyOn(api, 'searchMarketCapabilities').mockResolvedValue({ items: [] } as never)
  })

  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  it('BotCreateModal uses generic workspace path placeholder without personal username paths', () => {
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MantineProvider>
          <BotCreateModal opened onClose={vi.fn()} onSaved={vi.fn()} />
        </MantineProvider>
      </QueryClientProvider>,
    )

    const input = screen.getByLabelText(/专属工作区路径/) as HTMLInputElement
    expect(input.placeholder).not.toMatch(/\/home\/luwei/i)
    expect(input.placeholder).toContain('~/Documents/my-project')
  })

  it('CapabilitiesSettingsCard opens add-marketplace modal with empty source and generic Git placeholder', async () => {
    render(
      <MantineProvider>
        <CapabilitiesSettingsCard />
      </MantineProvider>,
    )

    fireEvent.click(await screen.findByRole('button', { name: /能力市场/ }))
    const addBtn = await screen.findByRole('button', { name: '添加订阅源' })
    fireEvent.click(addBtn)

    await waitFor(() => {
      expect(screen.getByText('订阅新能力市场源 (Marketplace)')).toBeInTheDocument()
    })

    const repoInput = screen.getByLabelText(/Git 仓库地址/) as HTMLInputElement
    expect(repoInput.value).toBe('')
    expect(repoInput.value).not.toMatch(/DietrichGebert|ponytail/i)
    expect(repoInput.placeholder).toBe('https://github.com/your-org/your-skills-repo.git')

    const nameInput = screen.getByLabelText(/市场标识名称/) as HTMLInputElement
    expect(nameInput.placeholder).not.toMatch(/ponytail/i)
  })
})
