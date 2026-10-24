import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
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

  it('calls real api.dispatchCapability with source_agent=astrorder and selected matrix', async () => {
    mount()

    await waitFor(() => {
      expect(screen.getAllByText(OFFICIAL_SKILL_TITLES['astrorder-sessions']).length).toBeGreaterThan(0)
    })

    const dispatchTrigger = await screen.findByRole('button', {
      name: /分发到 Agent|跨端分发/i,
    })
    fireEvent.click(dispatchTrigger)

    await waitFor(() => {
      expect(screen.getByText(/分发矩阵/i)).toBeInTheDocument()
    })

    // Find and check one of the target checkboxes
    const checkboxes = screen.getAllByRole('checkbox')
    expect(checkboxes.length).toBeGreaterThan(0)
    fireEvent.click(checkboxes[0])

    // Find and click the matrix submit button ('开始分发' or '进入分发矩阵')
    const startDispatchBtn = screen.getByRole('button', {
      name: /开始分发|确认分发/i,
    })
    fireEvent.click(startDispatchBtn)

    await waitFor(() => {
      expect(api.dispatchCapability).toHaveBeenCalled()
    })

    const callArgs = vi.mocked(api.dispatchCapability).mock.calls[0][0]
    expect(callArgs.capability_type).toBe('skill')
    expect(callArgs.source_agent).toBe('astrorder')
    expect(callArgs.item_id).toBe('astrorder-sessions')
    expect(callArgs.target_matrix.length).toBeGreaterThan(0)
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
})
