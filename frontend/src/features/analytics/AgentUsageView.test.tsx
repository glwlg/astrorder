import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { api, type AgentUsageReport } from '../../api/client'
import { AgentUsageView } from './AgentUsageView'

vi.mock('../../api/client', () => ({ api: { getAgentUsage: vi.fn() } }))

const report: AgentUsageReport = {
  range: '7d',
  summary: { calls: 4, succeeded: 3, failed: 1, active_agents: 1 },
  agents: [
    { agent_id: 'codex-a', name: '工作 Codex', runtime: 'codex', calls: 3, succeeded: 2, failed: 1 },
    { agent_id: '', name: '', runtime: '', calls: 1, succeeded: 1, failed: 0 },
  ],
  capabilities: [{ capability: 'sessions.read', calls: 4, succeeded: 3, failed: 1 }],
  recent: [{ capability: 'sessions.read', agent_id: 'codex-a', name: '工作 Codex', runtime: 'codex', success: false, duration_ms: 1200, occurred_at: '2026-11-10T04:00:00Z' }],
}

beforeEach(() => { vi.mocked(api.getAgentUsage).mockResolvedValue(report) })
afterEach(() => { cleanup(); vi.resetAllMocks() })
const mount = () => render(<MantineProvider><AgentUsageView /></MantineProvider>)

it('shows real totals, agent attribution, capabilities, and recent calls', async () => {
  mount()
  expect(await screen.findByText('75.0%')).toBeInTheDocument()
  expect(api.getAgentUsage).toHaveBeenCalledWith('7d')
  expect(screen.getAllByText('工作 Codex')).toHaveLength(2)
  expect(screen.getByText('未识别')).toBeInTheDocument()
  expect(screen.getAllByText('sessions.read')).toHaveLength(2)
  expect(screen.getByText('1.2 s')).toBeInTheDocument()
  expect(screen.getAllByText('失败').length).toBeGreaterThan(0)
})

it('changes the requested time range', async () => {
  mount()
  await screen.findByText('75.0%')
  fireEvent.click(screen.getByText('近 24 小时'))
  await waitFor(() => expect(api.getAgentUsage).toHaveBeenLastCalledWith('24h'))
})

it('shows empty and error states and retries', async () => {
  vi.mocked(api.getAgentUsage).mockRejectedValueOnce(new Error('读取失败')).mockResolvedValueOnce({
    range: '7d', summary: { calls: 0, succeeded: 0, failed: 0, active_agents: 0 }, agents: [], capabilities: [], recent: [],
  })
  mount()
  expect(await screen.findByText('读取失败')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '刷新' }))
  expect(await screen.findByText('此时间范围内暂无调用记录')).toBeInTheDocument()
  expect(screen.queryByText('NaN%')).not.toBeInTheDocument()
})
