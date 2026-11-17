import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { api, type SystemOneConfig } from '../../api/client'
import { SystemOneSettingsCard } from './JevSettingsCard'

vi.mock('@mantine/notifications', () => ({ notifications: { show: vi.fn() } }))
const official: SystemOneConfig = {
  mode: 'official', model: '', configured: true, masked_key: 'official…key',
  gateway_type: 'magpie', gateway_url: 'https://gateway.test/v1', gateway_configured: true,
}
beforeEach(() => {
  vi.spyOn(api, 'getSystemOneConfig').mockResolvedValue(official)
  vi.spyOn(api, 'getSystemOneModels').mockResolvedValue({ items: [{ id: 'typesafe/jev-preview', label: 'typesafe/jev-preview' }], gateway_type: 'magpie', gateway_url: 'https://gateway.test/v1' })
  vi.spyOn(api, 'updateSystemOneConfig').mockImplementation(async patch => ({ ...official, ...patch, masked_key: patch.api_key === null ? '' : official.masked_key }))
  vi.spyOn(api, 'testSystemOneConnection').mockResolvedValue({ ok: true, details: { component: 'MetricGrid', confidence: 0.9 } })
})
afterEach(() => { cleanup(); vi.restoreAllMocks() })
const mount = () => render(<MantineProvider><SystemOneSettingsCard /></MantineProvider>)
async function chooseGateway() {
  await waitFor(() => expect(screen.getByLabelText('通过 LLM 网关')).not.toBeDisabled())
  fireEvent.click(screen.getByLabelText('通过 LLM 网关'))
  await waitFor(() => expect(api.getSystemOneModels).toHaveBeenCalled())
  const select = screen.getByRole('combobox', { name: 'System One 模型' })
  await waitFor(() => expect(select).not.toBeDisabled())
  await userEvent.click(select)
  fireEvent.click(await screen.findByText('typesafe/System One-preview'))
}

it('keeps official configuration and saves a gateway mode with only the selected model', async () => {
  mount()
  await screen.findByText('official…key')
  expect(api.getSystemOneModels).not.toHaveBeenCalled()
  await chooseGateway()
  expect(screen.queryByLabelText('System One 官方 API Key')).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '保存配置' }))
  await waitFor(() => expect(api.updateSystemOneConfig).toHaveBeenCalledWith({ mode: 'gateway', model: 'typesafe/jev-preview' }))
})

it('tests the unsaved mode and switches back without replacing the official key', async () => {
  mount()
  await chooseGateway()
  fireEvent.click(screen.getByRole('button', { name: '测试连通性' }))
  await waitFor(() => expect(api.testSystemOneConnection).toHaveBeenCalledWith({ mode: 'gateway', model: 'typesafe/jev-preview' }))
  expect(api.updateSystemOneConfig).not.toHaveBeenCalled()
  await waitFor(() => expect(screen.getByLabelText('System One 官方')).not.toBeDisabled())
  fireEvent.click(screen.getByLabelText('System One 官方'))
  fireEvent.click(screen.getByRole('button', { name: '保存配置' }))
  await waitFor(() => expect(api.updateSystemOneConfig).toHaveBeenCalledWith({ mode: 'official' }))
})

it('restores a saved gateway model and clears the official key only on explicit request', async () => {
  vi.mocked(api.getSystemOneConfig).mockResolvedValue({ ...official, mode: 'gateway', model: 'typesafe/jev-preview' })
  mount()
  await waitFor(() => expect(screen.getByRole('combobox', { name: 'System One 模型' })).toHaveValue('typesafe/System One-preview'))
  expect(screen.queryByRole('button', { name: '清除官方 Key' })).not.toBeInTheDocument()
  fireEvent.click(screen.getByLabelText('System One 官方'))
  fireEvent.click(screen.getByRole('button', { name: '清除官方 Key' }))
  await waitFor(() => expect(api.updateSystemOneConfig).toHaveBeenCalledWith({ mode: 'official', api_key: null }))
})

it('shows catalog failures and does not offer unrelated chat models or silently use official mode', async () => {
  vi.mocked(api.getSystemOneModels).mockRejectedValue(new Error('网关密钥无效'))
  mount()
  await waitFor(() => expect(screen.getByLabelText('通过 LLM 网关')).not.toBeDisabled())
  fireEvent.click(screen.getByLabelText('通过 LLM 网关'))
  await screen.findByText('网关密钥无效')
  expect(screen.getByRole('button', { name: '保存配置' })).toBeDisabled()
  expect(screen.getByRole('button', { name: '测试连通性' })).toBeDisabled()
  expect(api.testSystemOneConnection).not.toHaveBeenCalled()
})
