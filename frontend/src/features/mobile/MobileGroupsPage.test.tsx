import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import type { BotGroup } from '../../domain/types'
import { useAstrorderStore } from '../../state/store'
import { MobileGroupsPage } from './MobileGroupsPage'

const groups: BotGroup[] = [
  {
    id: 'group-alpha',
    name: 'API授权复核讨论',
    description: '复核权限契约',
    members: [
      { id: 'design', machine_id: 'local', agent_id: 'codex', alias: '方案设计' },
      { id: 'dev', machine_id: 'local', agent_id: 'hermes', alias: '后端实现' },
    ],
    member_states: {
      design: { status: 'success', muted: true },
      dev: { status: 'success', muted: false },
    },
    coordination: { status: 'complete' },
    max_hops: 10,
    created_at: '2026-11-20T00:00:00Z',
    updated_at: '2026-11-20T00:00:00Z',
  },
  {
    id: 'group-beta',
    name: '前端动效评审',
    members: [{ id: 'ui', machine_id: 'local', agent_id: 'codex', alias: '视觉专家' }],
    member_states: {},
    coordination: { status: 'complete' },
    max_hops: 8,
    created_at: '2026-11-20T00:00:00Z',
    updated_at: '2026-11-20T00:00:00Z',
  },
]

const sampleSession = {
  id: 'session-1',
  agent_id: 'hermes',
  title: '回到单聊会话',
  workspace: '/workspace/astrorder',
  project_name: 'astrorder',
  status: 'idle' as const,
  updated_at: '2026-11-20T00:00:00Z',
}

function mount(initialPath = '/mobile/groups/group-alpha') {
  return render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient()}>
        <MemoryRouter initialEntries={[initialPath]}>
          <MobileGroupsPage />
        </MemoryRouter>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  useAstrorderStore.getState().resetRuntime()
  useAstrorderStore.getState().hydrateBootstrap({
    protocol_version: 1,
    cursor: 0,
    agents: [
      { id: 'codex', kind: 'codex', name: 'Codex', status: 'ready', capabilities: ['chat'], limitation: null },
      { id: 'hermes', kind: 'hermes', name: 'Hermes', status: 'ready', capabilities: ['chat'], limitation: null },
    ],
    sessions: [sampleSession],
  })
  const preferences = { appearance: {}, session_pins: {}, pinned_projects: [], project_order: [] }
  vi.spyOn(api, 'getPreferences').mockResolvedValue(preferences)
  vi.spyOn(api, 'importPreferences').mockResolvedValue(preferences)
  vi.spyOn(api, 'updatePreferences').mockResolvedValue(preferences)
  vi.spyOn(api, 'getOpenSessions').mockResolvedValue({ known_agent_ids: [], items: [] })
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ provider: 'p', model: 'bound-model' })
  vi.spyOn(api, 'getSessionUsage').mockResolvedValue({ ok: true, session_id: sampleSession.id, model: 'bound-model', context_window: 128000, last_input_tokens: 1000, context_available: true, source: 'native_last_request', used_percentage: 1, total_tokens: 1000, input_tokens: 1000, output_tokens: 0, cached_tokens: 0, reasoning_tokens: 0, cache_hit_rate: 0 })
  vi.spyOn(api, 'getMessages').mockResolvedValue({ items: [], next_cursor: null })
  vi.spyOn(api, 'getCommands').mockResolvedValue({ items: [] })
  vi.spyOn(api, 'getTasks').mockResolvedValue({ items: [] })
  vi.spyOn(api, 'listBotGroups').mockResolvedValue({ items: groups, count: groups.length })
  vi.spyOn(api, 'getBotGroup').mockImplementation(async (id) => ({
    group: groups.find((item) => item.id === id) || groups[0],
  }))
  vi.spyOn(api, 'listGroupMessages').mockResolvedValue({ items: [], count: 0 })
  vi.spyOn(api, 'getGroupArtifacts').mockResolvedValue({ items: [], references: [], checking: false })
})

afterEach(() => {
  cleanup()
  localStorage.clear()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('MobileGroupsPage 2.0 (Unified Sidebar Shell)', () => {
  it('uses the unified mobile header and left sidebar drawer with sliding session/group tabs', async () => {
    localStorage.setItem('astrorder:group:blackboard:group-alpha', 'open')
    mount()

    // Header shows active group name in the same top-bar title slot as project names
    const headerTitle = await screen.findByRole('button', { name: '当前群聊 API授权复核讨论，打开群聊列表' })
    expect(headerTitle).toHaveTextContent('API授权复核讨论')
    expect(screen.getByRole('button', { name: '应用设置' })).toBeInTheDocument()

    // Mobile never auto-covers chat with fullscreen blackboard from desktop localStorage
    expect(screen.queryByRole('complementary', { name: '群聊工作台' })).toBeNull()

    // Member cards stay collapsed by default on mobile to preserve screen height
    expect(screen.queryByRole('button', { name: '方案设计禁言' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '群设置' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '黑板' })).toBeInTheDocument()
    const membersToggle = await screen.findByRole('button', { name: '群成员管理' })
    expect(membersToggle).toHaveAttribute('aria-expanded', 'false')
    fireEvent.click(membersToggle)
    expect(membersToggle).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByRole('button', { name: '方案设计禁言' })).toBeInTheDocument()

    // Open left sidebar drawer from the header title -> defaults to 群聊 tab
    fireEvent.click(headerTitle)
    const drawer = await screen.findByRole('dialog', { name: '群聊列表' })
    expect(drawer).toHaveClass('m-session-sheet')
    const tablist = within(drawer).getByRole('tablist', { name: '移动端模式切换' })
    expect(tablist).toHaveAttribute('data-active-tab', 'groups')
    expect(within(tablist).getByRole('tab', { name: /群聊/ })).toHaveAttribute('aria-selected', 'true')

    // Switch to another group directly inside the sidebar drawer
    fireEvent.click(within(drawer).getByRole('button', { name: '前端动效评审' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '群聊列表' })).not.toBeInTheDocument())
    expect(await screen.findByRole('button', { name: '当前群聊 前端动效评审，打开群聊列表' })).toHaveTextContent('前端动效评审')

    // Reopen drawer and slide to 对话 tab IN-PLACE without closing the drawer
    fireEvent.click(screen.getByRole('button', { name: '打开群聊列表' }))
    const reopened = await screen.findByRole('dialog', { name: '群聊列表' })
    fireEvent.click(within(reopened).getByRole('tab', { name: '对话' }))
    expect(screen.getByRole('dialog', { name: '会话列表' })).toBeInTheDocument()
    expect(within(screen.getByRole('dialog', { name: '会话列表' })).getByRole('tablist')).toHaveAttribute('data-active-tab', 'sessions')

    // Select a session from the sidebar to return smoothly to single-session chat
    fireEvent.click(await within(screen.getByRole('dialog', { name: '会话列表' })).findByRole('button', { name: '回到单聊会话' }))
    await waitFor(() => expect(screen.getByRole('heading', { level: 1, name: '回到单聊会话' })).toBeInTheDocument())
    expect(screen.getByRole('button', { name: '当前项目 astrorder，打开会话列表' })).toHaveTextContent('astrorder')
  })

  it('uses mobile pill composer: Enter inserts newline, @ button opens mention menu, and Send button submits', async () => {
    const sendSpy = vi.spyOn(api, 'sendGroupMessage').mockResolvedValue({
      ok: true,
      message: {
        id: 'gm-1',
        group_id: 'group-alpha',
        sender_type: 'user',
        sender_id: 'human',
        text: '@后端实现 请确认接口',
        mentions: ['dev'],
        hop_count: 0,
        created_at: '2026-11-22T00:00:00Z',
      },
      target_agent_id: 'hermes',
      history_count: 1,
    })

    mount()
    await screen.findByText('群聊已就绪')
    const textarea = screen.getByRole('textbox', { name: '群聊消息' })
    expect(textarea).toHaveAttribute('enterkeyhint', 'enter')
    expect(textarea.closest('.mobile-voice-hold')).not.toBeNull()
    expect(screen.getByRole('button', { name: '语音消息' })).toBeInTheDocument()

    // Tap @ quick trigger to open mobile mention list and pick a member
    fireEvent.click(screen.getByRole('button', { name: '提及成员' }))
    const listbox = await screen.findByRole('listbox', { name: '群成员建议' })
    fireEvent.click(within(listbox).getByRole('button', { name: /@后端实现/ }))
    expect(textarea).toHaveValue('@后端实现 ')

    // Bare Enter does NOT send on mobile
    fireEvent.change(textarea, { target: { value: '@后端实现 请确认接口' } })
    fireEvent.keyDown(textarea, { key: 'Enter' })
    expect(sendSpy).not.toHaveBeenCalled()

    // IME composing Ctrl+Enter does NOT send
    fireEvent.keyDown(textarea, { key: 'Enter', ctrlKey: true, keyCode: 229 })
    expect(sendSpy).not.toHaveBeenCalled()

    // Explicit Send button submits cleanly
    fireEvent.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(sendSpy).toHaveBeenCalledWith('group-alpha', { text: '@后端实现 请确认接口' }))
  })
})
