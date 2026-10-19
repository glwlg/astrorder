import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { UsageGatewaySettingsCard } from './UsageGatewaySettingsCard'

vi.mock('../../api/client', () => ({
  api: {
    getAnalyticsConfig: vi.fn(),
    updateAnalyticsConfig: vi.fn(),
    getModelSyncTargets: vi.fn(),
    getModelSyncJobs: vi.fn(),
    previewModelSync: vi.fn(),
    startModelSync: vi.fn(),
    cancelModelSync: vi.fn(),
  },
}))

const gatewayConfig = {
  gateway_type: 'magpie',
  management_url: 'http://192.168.1.11:3430',
  inference_url: 'http://192.168.1.11:3425/v1',
  target_overrides: {},
  masked_key: 'abc...wxyz',
  masked_management_key: '',
  gateway_profiles: {},
}

afterEach(cleanup)
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getAnalyticsConfig).mockResolvedValue(gatewayConfig as never)
  vi.mocked(api.updateAnalyticsConfig).mockResolvedValue(gatewayConfig as never)
  vi.mocked(api.getModelSyncTargets).mockResolvedValue({
    items: [{ id: 'local', kind: 'local', name: '本机', agents: ['codex', 'grok', 'hermes'] }],
  } as never)
  vi.mocked(api.getModelSyncJobs).mockResolvedValue({ items: [] } as never)
})

function mount() {
  render(<MantineProvider><UsageGatewaySettingsCard /></MantineProvider>)
}

async function openPreview() {
  mount()
  fireEvent.click(await screen.findByLabelText('Codex'))
  fireEvent.click(screen.getByRole('button', { name: '预览选中' }))
  return screen.findByText(/个网关模型/)
}

it('keeps statistics page addresses per gateway across switching and saving', async () => {
  vi.mocked(api.getAnalyticsConfig).mockResolvedValue({
    ...gatewayConfig,
    statistics_url: 'https://magpie.example/#usage',
    gateway_profiles: {
      opencodex: { ...gatewayConfig, gateway_type: 'opencodex', statistics_url: 'https://ocx.example/#usage' },
      magpie: { ...gatewayConfig, statistics_url: 'https://magpie.example/#usage' },
    },
  } as never)
  mount()
  await screen.findByLabelText('Codex')
  const input = screen.getByLabelText('统计页地址')
  expect(input).toHaveValue('https://magpie.example/#usage')
  const switchTo = (name: string) => {
    fireEvent.click(screen.getByRole('combobox', { name: '网关类型' }))
    fireEvent.click(screen.getByRole('option', { name, hidden: true }))
  }
  switchTo('OpenCodeX')
  expect(input).toHaveValue('https://ocx.example/#usage')
  fireEvent.change(input, { target: { value: 'https://ocx.example/new#usage' } })
  switchTo('Magpie')
  expect(input).toHaveValue('https://magpie.example/#usage')
  fireEvent.click(screen.getByRole('button', { name: '保存配置' }))
  expect(api.updateAnalyticsConfig).toHaveBeenCalledWith(expect.objectContaining({
    gateway_type: 'magpie', statistics_url: 'https://magpie.example/#usage',
    gateway_profiles: expect.objectContaining({
      magpie: expect.objectContaining({ statistics_url: 'https://magpie.example/#usage' }),
      opencodex: expect.objectContaining({ statistics_url: 'https://ocx.example/new#usage' }),
    }),
  }))
})

it('loads and saves the statistics page address', async () => {
  vi.mocked(api.getAnalyticsConfig).mockResolvedValue({ ...gatewayConfig, statistics_url: 'https://gateway.example/#usage' } as never)
  mount()
  const input = await screen.findByLabelText('统计页地址')
  await screen.findByLabelText('Codex')
  expect(input).toHaveValue('https://gateway.example/#usage')
  fireEvent.change(input, { target: { value: 'https://other.example/stats' } })
  fireEvent.click(screen.getByRole('button', { name: '保存配置' }))
  expect(api.updateAnalyticsConfig).toHaveBeenCalledWith(expect.objectContaining({ statistics_url: 'https://other.example/stats' }))
})

it('offers Antigravity only on supported targets and previews its selection', async () => {
  vi.mocked(api.getModelSyncTargets).mockResolvedValue({ items: [
    { id: 'local', kind: 'local', name: '本机', agents: ['codex', 'antigravity'] },
    { id: 'ssh:test', kind: 'ssh', name: '远程', agents: ['codex'] },
  ] } as never)
  vi.mocked(api.previewModelSync).mockResolvedValue({ catalog_fingerprint: 'fixture', model_count: 1, targets: [] } as never)
  mount()
  const agy = await screen.findByLabelText('Antigravity (agy)')
  expect(screen.getAllByLabelText('Antigravity (agy)')).toHaveLength(1)
  fireEvent.click(agy)
  fireEvent.click(screen.getByRole('button', { name: '预览选中' }))
  await screen.findByText(/个网关模型/)
  expect(api.previewModelSync).toHaveBeenCalledWith([{ target_id: 'local', agents: ['antigravity'] }])
})

it('renders a preview whose model_diff arrays are null without crashing (Go null-slice regression)', async () => {
  vi.mocked(api.previewModelSync).mockResolvedValue({
    catalog_fingerprint: 'fp',
    model_count: 12,
    targets: [{
      target_id: 'local',
      agents: ['codex'],
      reload_pending: false,
      hermes: { dynamic: true, status: '未选择' },
      // Old Go build serialized empty slices as JSON null — must not throw.
      model_diff: { added: ['gpt-5.6-sol'], removed: null, kept_count: null } as never,
      changes: null as never,
    }],
  } as never)

  await openPreview()
  expect(screen.getByText('模型有更新')).toBeInTheDocument()
  expect(screen.getByText(/新增模型/)).toBeInTheDocument()
  expect(screen.queryByText(/移除模型/)).not.toBeInTheDocument()
})

it('renders removed models and the config diff accordion when present', async () => {
  vi.mocked(api.previewModelSync).mockResolvedValue({
    catalog_fingerprint: 'fp',
    model_count: 3,
    targets: [{
      target_id: 'local',
      agents: ['codex'],
      reload_pending: true,
      hermes: { dynamic: true, status: '未选择' },
      model_diff: { added: [], removed: ['old-model'], kept_count: 2 },
      changes: [{ file: 'codex_catalog', changed: true, current_sha256: 'a', expected_sha256: 'b', diff: '@@ -1,2 +1,2 @@\n-旧行\n+新行' }],
    }],
  } as never)

  await openPreview()
  expect(screen.getByText('移除模型 (1):')).toBeInTheDocument()
  expect(screen.getByText('- old-model')).toBeInTheDocument()
  expect(screen.getByText(/查看底层配置文件 Diff 详情/)).toBeInTheDocument()
  expect(screen.getByText('活跃会话稍后重载')).toBeInTheDocument()
})
