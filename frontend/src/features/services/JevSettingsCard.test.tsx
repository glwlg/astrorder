import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { api, type JevConfig } from '../../api/client'
import { JevSettingsCard } from './JevSettingsCard'

vi.mock('@mantine/notifications', () => ({ notifications: { show: vi.fn() } }))
const official: JevConfig = {
  mode: 'official', model: '', configured: true, masked_key: 'official…key',
  gateway_type: 'magpie', gateway_url: 'https://gateway.test/v1', gateway_configured: true,
}
beforeEach(() => {
  vi.spyOn(api, 'getJevConfig').mockResolvedValue(official)
  vi.spyOn(api, 'getJevModels').mockResolvedValue({ items: [{ id: 'typesafe/jev-preview', label: 'typesafe/jev-preview' }], gateway_type: 'magpie', gateway_url: 'https://gateway.test/v1' })
  vi.spyOn(api, 'updateJevConfig').mockImplementation(async patch => ({ ...official, ...patch, masked_key: patch.api_key === null ? '' : official.masked_key }))
  vi.spyOn(api, 'testJevConnection').mockResolvedValue({ ok: true, details: { component: 'MetricGrid', confidence: 0.9 } })
})
afterEach(() => { cleanup(); vi.restoreAllMocks() })
const mount = () => render(<MantineProvider><JevSettingsCard /></MantineProvider>)
async function chooseGateway() {
  await waitFor(() => expect(screen.getByLabelText('通过 LLM 网关')).not.toBeDisabled())
  fireEvent.click(screen.getByLabelText('通过 LLM 网关'))
  await waitFor(() => expect(api.getJevModels).toHaveBeenCalled())
  const select = screen.getByRole('combobox', { name: 'System One 模型' })
  await waitFor(() => expect(select).not.toBeDisabled())
  await userEvent.click(select)
  fireEvent.click(await screen.findByText('typesafe/jev-preview'))
}

it('keeps official configuration and saves a gateway mode with only the selected model', async () => {
  mount()
  await screen.findByText('official…key')
  expect(api.getJevModels).not.toHaveBeenCalled()
  await chooseGateway()
  expect(screen.queryByLabelText('Jev 官方 API Key')).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '保存配置' }))
  await waitFor(() => expect(api.updateJevConfig).toHaveBeenCalledWith({ mode: 'gateway', model: 'typesafe/jev-preview' }))
})

it('tests the unsaved mode and switches back without replacing the official key', async () => {
  mount()
  await chooseGateway()
  fireEvent.click(screen.getByRole('button', { name: '测试连通性' }))
  await waitFor(() => expect(api.testJevConnection).toHaveBeenCalledWith({ mode: 'gateway', model: 'typesafe/jev-preview' }))
  expect(api.updateJevConfig).not.toHaveBeenCalled()
  await waitFor(() => expect(screen.getByLabelText('Jev 官方')).not.toBeDisabled())
  fireEvent.click(screen.getByLabelText('Jev 官方'))
  fireEvent.click(screen.getByRole('button', { name: '保存配置' }))
  await waitFor(() => expect(api.updateJevConfig).toHaveBeenCalledWith({ mode: 'official' }))
})

it('restores a saved gateway model and clears the official key only on explicit request', async () => {
  vi.mocked(api.getJevConfig).mockResolvedValue({ ...official, mode: 'gateway', model: 'typesafe/jev-preview' })
  mount()
  await waitFor(() => expect(screen.getByRole('combobox', { name: 'System One 模型' })).toHaveValue('typesafe/jev-preview'))
  expect(screen.queryByRole('button', { name: '清除官方 Key' })).not.toBeInTheDocument()
  fireEvent.click(screen.getByLabelText('Jev 官方'))
  fireEvent.click(screen.getByRole('button', { name: '清除官方 Key' }))
  await waitFor(() => expect(api.updateJevConfig).toHaveBeenCalledWith({ mode: 'official', api_key: null }))
})

it('shows catalog failures and does not offer unrelated chat models or silently use official mode', async () => {
  vi.mocked(api.getJevModels).mockRejectedValue(new Error('网关密钥无效'))
  mount()
  await waitFor(() => expect(screen.getByLabelText('通过 LLM 网关')).not.toBeDisabled())
  fireEvent.click(screen.getByLabelText('通过 LLM 网关'))
  await screen.findByText('网关密钥无效')
  expect(screen.getByRole('button', { name: '保存配置' })).toBeDisabled()
  expect(screen.getByRole('button', { name: '测试连通性' })).toBeDisabled()
  expect(api.testJevConnection).not.toHaveBeenCalled()
})
