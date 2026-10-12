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
