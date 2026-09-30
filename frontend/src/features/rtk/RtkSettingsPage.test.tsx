import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { RtkSettingsPage } from './RtkSettingsPage'

vi.mock('../../api/client', () => ({ api: { getEnvironments: vi.fn(), getRtk: vi.fn(), installRtk: vi.fn(), setRtkAgent: vi.fn() } }))
const status = { installed: true, version: '0.50.0', executable: '/bin/rtk', agents: [{ kind: 'codex', name: 'Codex', enabled: true, supported: true }], daily: [{ date: '2026-09-30', commands: 2, input_tokens: 100, output_tokens: 20, saved_tokens: 80 }], summary: { total_commands: 2, total_input: 100, total_output: 20, total_saved: 80, avg_savings_pct: 80 } }
afterEach(cleanup)
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getEnvironments).mockResolvedValue({ items: [{ id: 'local', name: '本机', method: 'local', agents: [] }, { id: 'ssh-test', name: '测试服务器', method: 'ssh', agents: [] }] } as never)
  vi.mocked(api.getRtk).mockResolvedValue(status)
  vi.mocked(api.setRtkAgent).mockResolvedValue({ ...status, agents: [{ ...status.agents[0], enabled: false }] })
})
function mount() { render(<MantineProvider><QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><RtkSettingsPage /></QueryClientProvider></MantineProvider>) }
it('reads native state, changes range and toggles the selected host agent', async () => {
  mount()
  expect(await screen.findByRole('switch', { name: 'Codex RTK' })).toBeChecked()
  fireEvent.click(screen.getByLabelText('90 天'))
  await waitFor(() => expect(api.getRtk).toHaveBeenCalledWith('local', '90'))
  fireEvent.click(await screen.findByRole('switch', { name: 'Codex RTK' }))
  await waitFor(() => expect(api.setRtkAgent).toHaveBeenCalledWith('local', 'codex', false))
  fireEvent.change(screen.getByLabelText('运行环境'), { target: { value: 'ssh-test' } })
  await waitFor(() => expect(api.getRtk).toHaveBeenCalledWith('ssh-test', '90'))
})
it('keeps failed reads visible instead of displaying zero savings', async () => {
  vi.mocked(api.getRtk).mockRejectedValue(new Error('SSH 探测失败'))
  mount()
  expect(await screen.findByText('SSH 探测失败')).toBeInTheDocument()
  expect(screen.queryByRole('switch')).not.toBeInTheDocument()
})
it('preserves the enabled switch when a mutation fails', async () => {
  vi.mocked(api.setRtkAgent).mockRejectedValue(new Error('配置回读失败'))
  mount()
  fireEvent.click(await screen.findByRole('switch', { name: 'Codex RTK' }))
  expect(await screen.findByText('配置回读失败')).toBeInTheDocument()
  expect(screen.getByRole('switch', { name: 'Codex RTK' })).toBeChecked()
})
it('installs on the selected SSH environment and refreshes native state', async () => {
  vi.mocked(api.getRtk).mockResolvedValue({ ...status, installed: false, version: null })
  vi.mocked(api.installRtk).mockResolvedValue(status)
  mount()
  await screen.findByRole('button', { name: '安装 RTK' })
  fireEvent.change(screen.getByLabelText('运行环境'), { target: { value: 'ssh-test' } })
  fireEvent.click(await screen.findByRole('button', { name: '安装 RTK' }))
  await waitFor(() => expect(api.installRtk).toHaveBeenCalledWith('ssh-test'))
  await waitFor(() => expect(api.getRtk).toHaveBeenCalledWith('ssh-test', '30'))
})
