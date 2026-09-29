import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, type OcxUsageResponse } from '../api/client'
import type { Project } from '../domain/types'
import { Sidebar } from './Sidebar'

const zeroSessionProject: Project = {
  id: 'project-row-empty',
  source_id: 'source-remote',
  connection_id: 'ssh-wsl',
  project_id: 'empty-project-id',
  project_name: '无会话项目',
  workspace: '/empty',
  session_count: 0,
  updated_at: '2026-09-07T09:00:00Z',
}

describe('Sidebar project catalog wiring', () => {
  beforeEach(() => {
    const now = new Date()
    const today = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
    vi.spyOn(api, 'getAnalyticsUsage').mockResolvedValue({
      summary: { requests: 999, totalTokens: 99_000_000 },
      days: [
        { date: '2020-01-01', requests: 999, totalTokens: 99_000_000, models: [{ provider: 'openai', model: 'older-model', requests: 999, totalTokens: 99_000_000 }] },
        { date: today, requests: 12, totalTokens: 250_000, models: [
          { provider: 'openai', model: 'gpt-5.6-sol', requests: 8, totalTokens: 200_000 },
          { provider: 'xai', model: 'grok-4.7', requests: 4, totalTokens: 50_000 },
        ] },
      ],
    } as unknown as OcxUsageResponse)
  })
  afterEach(() => { cleanup(); vi.restoreAllMocks() })

  function renderSidebar(projects: Project[] = []) {
    return render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <MemoryRouter>
            <Sidebar sessions={[]} agents={{}} projects={projects} onSelectSession={vi.fn()} />
          </MemoryRouter>
        </QueryClientProvider>
      </MantineProvider>,
    )
  }

  it('forwards zero-session projects to the two-level session rail without a connection parent', () => {
    renderSidebar([zeroSessionProject])

    expect(screen.getByRole('button', { name: /无会话项目.*0 个会话/ })).toBeInTheDocument()
    expect(screen.getByText('ssh-wsl')).toBeInTheDocument()
    expect(screen.queryByText('连接 ssh-wsl')).not.toBeInTheDocument()
  })

  it('preloads today’s model usage and shows it when the statistics tab is hovered', async () => {
    renderSidebar()
    await waitFor(() => expect(api.getAnalyticsUsage).toHaveBeenCalledOnce())
    expect(api.getAnalyticsUsage).toHaveBeenCalledWith({ range: '7d', surface: 'all' })

    await userEvent.hover(screen.getByRole('link', { name: '统计' }))
    expect(await screen.findByText('今日模型用量')).toBeInTheDocument()
    expect(screen.getByText('12 次请求')).toBeInTheDocument()
    expect(screen.getByText('25 万 Token')).toBeInTheDocument()
    expect(screen.getByText('gpt-5.6-sol')).toBeInTheDocument()
    expect(screen.getByText('grok-4.7')).toBeInTheDocument()
    expect(screen.queryByText('older-model')).not.toBeInTheDocument()
    expect(api.getAnalyticsUsage).toHaveBeenCalledOnce()
  })

  it('keeps navigation usable if gateway usage is unavailable', async () => {
    vi.mocked(api.getAnalyticsUsage).mockRejectedValue(new Error('gateway offline'))
    renderSidebar()
    await userEvent.hover(screen.getByRole('link', { name: '统计' }))
    expect(await screen.findByText('今日用量暂不可用')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '会话' })).toBeInTheDocument()
  })
})
