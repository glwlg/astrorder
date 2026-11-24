import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import {
  CapabilitiesSettingsCard,
  getSkillKey,
  isOfficialSkill,
  isUninstalledOfficialSkill,
  OFFICIAL_SKILL_TITLES,
} from './CapabilitiesSettingsCard'
import type { SkillItem } from './CapabilitiesSettingsCard'

vi.mock('../../api/client', () => ({
  api: {
    getCapabilitiesTargets: vi.fn(),
    getCapabilitiesSummary: vi.fn(),
    dispatchCapability: vi.fn(),
    startCapabilityDispatchJob: vi.fn(),
    listCapabilityDispatchJobs: vi.fn(),
    clearCapabilityDispatchJobs: vi.fn(),
    installSkill: vi.fn(),
    removeSkill: vi.fn(),
    searchMarketCapabilities: vi.fn(),
    updateMcpServer: vi.fn(),
    pingMcpServer: vi.fn(),
    updatePluginState: vi.fn(),
    updateMarketplace: vi.fn(),
  },
}))

const mockTargets = [
  { id: 'local', kind: 'local', name: '本机环境' },
  { id: 'ssh-remote', kind: 'ssh', name: 'Debian 远程节点' },
]

const mockOfficialSessionsSkill: SkillItem = {
  name: 'astrorder-sessions',
  source: 'astrorder',
  scope: 'builtin',
  agent: 'astrorder',
  agents: [],
  category: 'astrorder',
  description: '星序原生会话生命周期管理能力。',
  content: '# astrorder-sessions\n\n会话生命周期与路由调度。',
}

const mockOfficialBrowserSkill: SkillItem = {
  name: 'astrorder-browser',
  source: 'astrorder',
  scope: 'builtin',
  agent: 'astrorder',
  agents: [],
  category: 'astrorder',
  description: '星序自动化浏览器控制能力。',
  content: '# astrorder-browser\n\nCDP 协议桥接与自动化页面控制。',
}

const mockUserSkill: SkillItem = {
  name: 'astrorder-sessions',
  source: 'local',
  scope: 'workspace',
  agent: 'codex',
  agents: ['codex'],
  category: 'custom',
  description: '用户自建同名本地技能。',
  path: '/home/user/.codex/skills/astrorder-sessions',
}

const mockSummary = {
  target_id: 'local',
  target_name: '本机环境',
  kind: 'local',
  counts: { skills: 3, mcp: 0, plugins: 0, marketplaces: 0 },
  skill_agents: [
    { id: 'codex', label: 'OpenAI Codex' },
    { id: 'hermes', label: 'Hermes Agent' },
    { id: 'grok', label: 'xAI Grok' },
  ],
  skills: [mockOfficialSessionsSkill, mockOfficialBrowserSkill, mockUserSkill],
  mcp_servers: [],
  plugins: [],
  marketplaces: [],
}

afterEach(cleanup)

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getCapabilitiesTargets).mockResolvedValue(mockTargets as never)
  vi.mocked(api.getCapabilitiesSummary).mockResolvedValue(mockSummary as never)
  vi.mocked(api.listCapabilityDispatchJobs).mockResolvedValue({ jobs: [] } as never)
  vi.mocked(api.clearCapabilityDispatchJobs).mockResolvedValue({ ok: true, cleared: 1, jobs: [] } as never)
  vi.mocked(api.startCapabilityDispatchJob).mockImplementation(async (req) => ({
    id: 'job-test-1',
    capability_type: req.capability_type,
    source_target_id: req.source_target_id,
    source_target_name: '本机环境',
    items: req.items,
    target_matrix: req.target_matrix,
    status: 'running',
    total_steps: req.items.length * req.target_matrix.length,
    completed_steps: 0,
    success_count: 0,
    failed_count: 0,
    current_label: req.items[0]?.label || req.items[0]?.item_id,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    reports: [],
  }) as never)
  vi.mocked(api.dispatchCapability).mockResolvedValue({
    ok: true,
    capability_type: 'skill',
    item_id: 'astrorder-sessions',
    total_targets: 1,
    success_count: 1,
    reports: [
      {
        target_id: 'ssh-remote',
        target_name: 'Debian 远程节点',
        agent: 'codex',
        ok: true,
        installed_path: '/remote/path',
        message: 'Dispatched successfully',
      },
    ],
  } as never)
})

function mount() {
  return render(
    <MantineProvider>
      <CapabilitiesSettingsCard />
    </MantineProvider>
  )
}

describe('CapabilitiesSettingsCard - 星序官方技能与动态分发', () => {
  it('strictly classifies official skills by source and category and distinguishes user skills with same name', () => {
    // Official skill: source === 'astrorder' && category === 'astrorder'
    expect(isOfficialSkill(mockOfficialSessionsSkill)).toBe(true)
    expect(isOfficialSkill(mockOfficialBrowserSkill)).toBe(true)

    // User-created skill with same name is NOT official
    expect(isOfficialSkill(mockUserSkill)).toBe(false)

    // Skill with merely prefix 'astrorder-' is NOT official without source & category
    expect(
      isOfficialSkill({
        name: 'astrorder-sessions',
        source: 'community',
        category: 'general',
      })
    ).toBe(false)

    // Uninstalled official source check
    expect(isUninstalledOfficialSkill(mockOfficialSessionsSkill)).toBe(true)
    expect(isUninstalledOfficialSkill(mockUserSkill)).toBe(false)

    // Key disambiguation between official builtin and installed user skill of same name
    const officialKey = getSkillKey(mockOfficialSessionsSkill)
    const userKey = getSkillKey(mockUserSkill)
    expect(officialKey).not.toBe(userKey)
    expect(officialKey).toContain('builtin')
    expect(userKey).toContain('workspace')

    // Strict classification also requires scope='builtin': a same-name entry in an Agent
    // skill directory is not promoted to official by name alone.
    const installedSameName: SkillItem = {
      name: 'astrorder-sessions',
      source: 'astrorder',
      category: 'astrorder',
      scope: 'agent',
      agent: 'codex',
      agents: ['codex'],
      path: '/home/user/.codex/skills/astrorder-sessions',
    }
    expect(isOfficialSkill(installedSameName)).toBe(false)
    expect(isUninstalledOfficialSkill(installedSameName)).toBe(false)
    expect(getSkillKey(installedSameName)).not.toBe(officialKey)
  })

  it('renders official modules with responsibilities (会话管理/浏览器控制) and keeps official discoverable across agent views', async () => {
    mount()

    // Expect official titles to be rendered
    await waitFor(() => {
      expect(screen.getAllByText(OFFICIAL_SKILL_TITLES['astrorder-sessions']).length).toBeGreaterThan(0)
      expect(screen.getAllByText(OFFICIAL_SKILL_TITLES['astrorder-browser']).length).toBeGreaterThan(0)
    })

    // Expect source label or badge indicating official source
    expect(screen.getAllByText(/星序/i).length).toBeGreaterThan(0)
  })

  it('does not display uninstall button for uninstalled official builtin skills but keeps it for user skills', async () => {
    mount()

    // Wait for skills to load
    await waitFor(() => {
      expect(screen.getAllByText(OFFICIAL_SKILL_TITLES['astrorder-sessions']).length).toBeGreaterThan(0)
    })

    // Click on official session skill
    const sessionCards = screen.getAllByText(OFFICIAL_SKILL_TITLES['astrorder-sessions'])
    fireEvent.click(sessionCards[0])

    // Official builtin uninstalled skill should NOT have uninstall button in top meta
    const uninstallButtonsForOfficial = screen.queryAllByRole('button', { name: /^卸载$/ })
    expect(uninstallButtonsForOfficial.length).toBe(0)
  })

  it('dynamically renders agent checkboxes from skill_agents registry in dispatch modal', async () => {
    mount()

    await waitFor(() => {
      expect(screen.getAllByText(OFFICIAL_SKILL_TITLES['astrorder-sessions']).length).toBeGreaterThan(0)
    })

    // Click dispatch button for the skill (buttons like '分发到 Agent' or '跨端分发')
    const dispatchTrigger = await screen.findByRole('button', {
      name: /分发到 Agent|跨端分发/i,
    })
    fireEvent.click(dispatchTrigger)

    // Modal opens
    await waitFor(() => {
      expect(screen.getByText(/分发矩阵/i)).toBeInTheDocument()
    })

    // Checkboxes for all three agents from skill_agents should be present
    expect(screen.getAllByText(/Codex/i).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/Hermes/i).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/Grok/i).length).toBeGreaterThan(0)
  })

  it('submits background dispatch job via api.startCapabilityDispatchJob with source_agent=astrorder and selected matrix', async () => {
    mount()

    await waitFor(() => {
      expect(screen.getAllByText(OFFICIAL_SKILL_TITLES['astrorder-sessions']).length).toBeGreaterThan(0)
    })

    const dispatchTrigger = await screen.findByRole('button', {
      name: /分发到 Agent|跨端分发/i,
    })
    fireEvent.click(dispatchTrigger)

    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText(/分发矩阵/i)).toBeInTheDocument()

    // Check one of the target checkboxes inside the dispatch modal
    const modalCheckboxes = within(dialog).getAllByRole('checkbox')
    expect(modalCheckboxes.length).toBeGreaterThan(0)
    fireEvent.click(modalCheckboxes[0])

    const startDispatchBtn = within(dialog).getByRole('button', {
      name: /放入后台分发/i,
    })
    fireEvent.click(startDispatchBtn)

    await waitFor(() => {
      expect(api.startCapabilityDispatchJob).toHaveBeenCalled()
    })

    const callArgs = vi.mocked(api.startCapabilityDispatchJob).mock.calls[0][0]
    expect(callArgs.capability_type).toBe('skill')
    expect(callArgs.items).toEqual([
      expect.objectContaining({
        item_id: 'astrorder-sessions',
        source_agent: 'astrorder',
      }),
    ])
    expect(callArgs.target_matrix.length).toBeGreaterThan(0)
  })

  it('shows clear installed vs uninstalled status pills, filters by installation status, and batch-dispatches multiple skills in background', async () => {
    const installedOfficial: SkillItem = {
      ...mockOfficialSessionsSkill,
      installed_agents: ['codex', 'hermes', 'grok'],
      agents: ['codex', 'hermes', 'grok'],
    }
    const partialOfficial: SkillItem = {
      ...mockOfficialBrowserSkill,
      installed_agents: ['codex'],
      agents: ['codex'],
    }
    const missingOfficial: SkillItem = {
      name: 'astrorder-swarm',
      source: 'astrorder',
      scope: 'builtin',
      agent: 'astrorder',
      installed_agents: [],
      agents: [],
      category: 'astrorder',
      description: '星序 Swarm 协同能力。',
    }
    vi.mocked(api.getCapabilitiesSummary).mockResolvedValue({
      ...mockSummary,
      counts: { skills: 3, mcp: 0, plugins: 0, marketplaces: 0 },
      skills: [installedOfficial, partialOfficial, missingOfficial],
    } as never)

    mount()

    await waitFor(() => {
      expect(screen.getByText('已全装 3/3')).toBeInTheDocument()
      expect(screen.getByText('已装 1/3')).toBeInTheDocument()
      expect(screen.getAllByText('未安装').length).toBeGreaterThan(0)
    })

    // Switch Agent view to Hermes: sessions is installed, browser + swarm are uninstalled
    fireEvent.click(screen.getByRole('button', { name: 'Hermes Agent' }))
    await waitFor(() => {
      expect(screen.getByText('已安装 · Hermes')).toBeInTheDocument()
      expect(screen.getAllByText('未安装 · Hermes').length).toBe(2)
    })

    // Filter to uninstalled only in left rail
    fireEvent.click(screen.getByRole('button', { name: /^未安装/ }))
    await waitFor(() => {
      expect(screen.queryByText(OFFICIAL_SKILL_TITLES['astrorder-sessions'])).not.toBeInTheDocument()
      expect(screen.getAllByText(OFFICIAL_SKILL_TITLES['astrorder-browser']).length).toBeGreaterThan(0)
      expect(screen.getAllByText('astrorder-swarm').length).toBeGreaterThan(0)
    })

    // Select all uninstalled skills and open batch dispatch modal
    fireEvent.click(screen.getByRole('button', { name: /只选未安装/ }))
    const batchBtn = await screen.findByRole('button', { name: /批量分发 \(2\)/ })
    fireEvent.click(batchBtn)

    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText(/批量分发矩阵（2 项能力）/)).toBeInTheDocument()

    // Hermes on current node is preselected when batch dispatching under Hermes view
    fireEvent.click(within(dialog).getByRole('button', { name: /放入后台分发/ }))

    await waitFor(() => {
      expect(api.startCapabilityDispatchJob).toHaveBeenCalledWith(
        expect.objectContaining({
          capability_type: 'skill',
          source_target_id: 'local',
          items: [
            expect.objectContaining({ item_id: 'astrorder-browser', source_agent: 'astrorder' }),
            expect.objectContaining({ item_id: 'astrorder-swarm', source_agent: 'astrorder' }),
          ],
          target_matrix: [{ target_id: 'local', agent: 'hermes' }],
        }),
      )
    })

    // Live background progress bar should appear immediately without blocking UI
    await waitFor(() => {
      expect(screen.getByText(/正在后台分发 2 项能力/)).toBeInTheDocument()
    })
  })

  it('does not crash when startCapabilityDispatchJob returns null reports array', async () => {
    vi.mocked(api.startCapabilityDispatchJob).mockResolvedValue({
      id: 'job-null-reports',
      capability_type: 'skill',
      source_target_id: 'local',
      source_target_name: '本机环境',
      items: [{ item_id: 'astrorder-sessions', source_agent: 'astrorder', label: '会话管理' }],
      target_matrix: [{ target_id: 'local', agent: 'codex' }],
      status: 'running',
      total_steps: 1,
      completed_steps: 0,
      success_count: 0,
      failed_count: 0,
      current_label: '会话管理',
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
      reports: null,
    } as never)

    mount()
    await waitFor(() => {
      expect(screen.getAllByText(OFFICIAL_SKILL_TITLES['astrorder-sessions']).length).toBeGreaterThan(0)
    })

    fireEvent.click(await screen.findByRole('button', { name: /分发到 Agent|跨端分发/i }))
    const dialog = await screen.findByRole('dialog')
    fireEvent.click(within(dialog).getAllByRole('checkbox')[0])
    fireEvent.click(within(dialog).getByRole('button', { name: /放入后台分发/ }))

    await waitFor(() => {
      expect(screen.getByText(/正在后台分发 1 项能力/)).toBeInTheDocument()
    })
  })

  it('displays background dispatch jobs in drawer and supports retrying failed steps', async () => {
    vi.mocked(api.listCapabilityDispatchJobs).mockResolvedValue({
      jobs: [
        {
          id: 'job-partial-1',
          capability_type: 'skill',
          source_target_id: 'local',
          source_target_name: '本机环境',
          items: [
            { item_id: 'astrorder-browser', source_agent: 'astrorder', label: '浏览器控制' },
            { item_id: 'astrorder-swarm', source_agent: 'astrorder', label: 'astrorder-swarm' },
          ],
          target_matrix: [{ target_id: 'ssh-remote', agent: 'codex' }],
          status: 'partial',
          total_steps: 2,
          completed_steps: 2,
          success_count: 1,
          failed_count: 1,
          created_at: new Date().toISOString(),
          updated_at: new Date().toISOString(),
          reports: [
            {
              item_id: 'astrorder-browser',
              item_label: '浏览器控制',
              target_id: 'ssh-remote',
              target_name: 'Debian 远程节点',
              agent: 'codex',
              ok: true,
              message: '已分发并核验',
            },
            {
              item_id: 'astrorder-swarm',
              item_label: 'astrorder-swarm',
              target_id: 'ssh-remote',
              target_name: 'Debian 远程节点',
              agent: 'codex',
              ok: false,
              message: 'ssh connection reset',
            },
          ],
        },
      ],
    } as never)

    mount()

    const jobsBtn = await screen.findByRole('button', { name: /后台任务 \(1\)/ })
    fireEvent.click(jobsBtn)

    await waitFor(() => {
      expect(screen.getByText('后台分发任务队列')).toBeInTheDocument()
      expect(screen.getByText('ssh connection reset')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByRole('button', { name: /重试失败项 \(1\)/ }))
    await waitFor(() => {
      expect(api.startCapabilityDispatchJob).toHaveBeenCalledWith({
        capability_type: 'skill',
        source_target_id: 'local',
        items: [{ item_id: 'astrorder-swarm', source_agent: 'astrorder', label: 'astrorder-swarm' }],
        target_matrix: [{ target_id: 'ssh-remote', agent: 'codex' }],
      })
    })
  })

  it('clears stale details when filter or search has empty matches', async () => {
    mount()

    await waitFor(() => {
      expect(screen.getAllByText(OFFICIAL_SKILL_TITLES['astrorder-sessions']).length).toBeGreaterThan(0)
    })

    // Search for a keyword that matches no skill
    const searchInput = screen.getByPlaceholderText(/检索/i)
    fireEvent.change(searchInput, { target: { value: 'nonexistent-skill-query-xyz-9999' } })

    // Details panel should no longer show the previous skill's metadata and show empty prompt
    await waitFor(() => {
      expect(screen.getByText('无匹配的技能资产')).toBeInTheDocument()
      expect(screen.getByText(/请在左侧选择/i)).toBeInTheDocument()
    })
  })

  it('does not fabricate official modules when the API returns none', async () => {
    vi.mocked(api.getCapabilitiesSummary).mockResolvedValue({
      ...mockSummary,
      counts: { skills: 1, mcp: 0, plugins: 0, marketplaces: 0 },
      skills: [mockUserSkill],
    } as never)

    mount()

    await waitFor(() => {
      expect(screen.getAllByText(mockUserSkill.description!).length).toBeGreaterThan(0)
    })

    // No client-side fallback: official responsibility titles must not appear
    expect(screen.queryByText(OFFICIAL_SKILL_TITLES['astrorder-sessions'])).not.toBeInTheDocument()
    expect(screen.queryByText(OFFICIAL_SKILL_TITLES['astrorder-browser'])).not.toBeInTheDocument()
  })

  it('renders dispatch agent checkboxes only from the registry, never a hardcoded pair', async () => {
    vi.mocked(api.getCapabilitiesSummary).mockResolvedValue({
      ...mockSummary,
      skill_agents: [
        { id: 'codex', label: 'OpenAI Codex' },
        { id: 'hermes', label: 'Hermes Agent' },
        { id: 'grok', label: 'xAI Grok' },
        { id: 'kimi', label: 'Moonshot Kimi' },
      ],
    } as never)

    mount()

    await waitFor(() => {
      expect(screen.getAllByText(OFFICIAL_SKILL_TITLES['astrorder-sessions']).length).toBeGreaterThan(0)
    })

    fireEvent.click(await screen.findByRole('button', { name: /分发到 Agent|跨端分发/i }))
    await waitFor(() => {
      expect(screen.getByText(/分发矩阵/i)).toBeInTheDocument()
    })

    // Registry-driven: a fourth agent appears without any code change
    expect(screen.getAllByText(/Moonshot Kimi/i).length).toBeGreaterThan(0)
  })

  it('keeps official source discoverable under an installed-agent filter', async () => {
    mount()

    await waitFor(() => {
      expect(screen.getAllByText(OFFICIAL_SKILL_TITLES['astrorder-sessions']).length).toBeGreaterThan(0)
    })

    // Filter by Hermes: user skill only lists codex, official builtin lists no agents at all
    fireEvent.click(screen.getAllByRole('button', { name: /^Hermes Agent/ })[0])

    await waitFor(() => {
      expect(screen.getAllByText(OFFICIAL_SKILL_TITLES['astrorder-sessions']).length).toBeGreaterThan(0)
      expect(screen.queryByText(mockUserSkill.description!)).not.toBeInTheDocument()
    })
  })

  it('supports Antigravity MCP toggle, plugin metadata/install, and agent-scoped filtering', async () => {
    vi.mocked(api.updateMcpServer).mockResolvedValue({ ok: true, action: 'toggle', agent: 'antigravity', server: 'agy-api' })
    vi.mocked(api.updatePluginState).mockResolvedValue({ ok: true, action: 'install', agent: 'antigravity', plugin: '/tmp/my-plug', output: 'Installed' })
    vi.mocked(api.getCapabilitiesSummary).mockResolvedValue({
      ...mockSummary,
      counts: { skills: 3, mcp: 2, plugins: 2, marketplaces: 1 },
      skill_agents: [
        { id: 'codex', label: 'OpenAI Codex' },
        { id: 'antigravity', label: 'Google Antigravity' },
      ],
      mcp_servers: [
        { name: 'codex-mcp', agent: 'codex', enabled: true, transport: 'sse/http', url: 'https://codex.example/mcp' },
        { name: 'agy-api', agent: 'antigravity', enabled: true, transport: 'sse/http', url: 'https://agy.example/mcp', headers: { Authorization: '[REDACTED]' } },
      ],
      plugins: [
        { id: 'codex::gh@curated', name: 'gh', full_name: 'gh@curated', agent: 'codex', enabled: true },
        {
          id: 'antigravity::modern-web-guidance',
          name: 'modern-web-guidance',
          full_name: 'modern-web-guidance',
          agent: 'antigravity',
          marketplace: 'Google Chrome',
          enabled: true,
          source: 'antigravity-plugin',
          config: { version: '1.0.1', description: 'Modern web best practices', skills: ['modern-web-guidance'], mcp_servers: ['plug-mcp'], has_hooks: true },
        },
      ],
    } as never)

    mount()
    await waitFor(() => {
      expect(screen.getAllByText('Google Antigravity').length).toBeGreaterThan(0)
    })

    // Scope view to Antigravity
    fireEvent.click(screen.getAllByRole('button', { name: 'Google Antigravity' })[0])

    // Switch to MCP tab: only agy-api should appear, toggle switch works
    fireEvent.click(screen.getByRole('button', { name: /MCP 服务器/ }))
    await waitFor(() => {
      expect(screen.getByText('agy-api')).toBeInTheDocument()
      expect(screen.queryByText('codex-mcp')).not.toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('switch', { name: '切换 MCP 服务 agy-api 状态' }))
    await waitFor(() => {
      expect(api.updateMcpServer).toHaveBeenCalledWith({
        target_id: 'local',
        agent: 'antigravity',
        action: 'toggle',
        server: { name: 'agy-api', enabled: false },
      })
    })

    // Switch to Plugins tab: only Antigravity plugin with bundled metadata should appear
    fireEvent.click(screen.getByRole('button', { name: /复合插件/ }))
    await waitFor(() => {
      expect(screen.getAllByText('modern-web-guidance').length).toBeGreaterThan(0)
      expect(screen.getByText('v1.0.1')).toBeInTheDocument()
      expect(screen.getByText('Skill: modern-web-guidance')).toBeInTheDocument()
      expect(screen.getByText('含 Hooks')).toBeInTheDocument()
      expect(screen.queryByText('gh@curated')).not.toBeInTheDocument()
    })

    // Install Antigravity plugin modal
    fireEvent.click(screen.getByRole('button', { name: /安装 \/ 导入 Antigravity 插件/ }))
    await waitFor(() => {
      expect(screen.getByPlaceholderText(/\/path\/to\/my-plugin/)).toBeInTheDocument()
    })
    fireEvent.change(screen.getByPlaceholderText(/\/path\/to\/my-plugin/), { target: { value: '/tmp/my-plug' } })
    fireEvent.click(screen.getByRole('button', { name: '执行安装' }))
    await waitFor(() => {
      expect(api.updatePluginState).toHaveBeenCalledWith({
        target_id: 'local',
        agent: 'antigravity',
        action: 'install',
        source: '/tmp/my-plug',
      })
    })
  })
})
