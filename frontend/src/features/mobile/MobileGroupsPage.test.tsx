import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
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

function mount(initialPath = '/mobile/groups/group-alpha') {
  return render(
    <MantineProvider>
      <MemoryRouter initialEntries={[initialPath]}>
        <Routes>
          <Route path="/mobile/groups" element={<MobileGroupsPage />} />
          <Route path="/mobile/groups/:groupId" element={<MobileGroupsPage />} />
          <Route path="/mobile" element={<div>mobile-home</div>} />
        </Routes>
      </MemoryRouter>
    </MantineProvider>,
  )
}

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  useAstrorderStore.getState().resetRuntime()
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

describe('MobileGroupsPage 2.0', () => {
  it('switches groups via the bottom sheet and collapses member mute cards by default', async () => {
    localStorage.setItem('astrorder:group:blackboard:group-alpha', 'open')
    mount()
    const picker = await screen.findByRole('button', { name: '切换群聊' })
    await waitFor(() => expect(picker).toHaveTextContent('API授权复核讨论'))

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

    // Open mobile bottom sheet to switch to another group
    fireEvent.click(picker)
    const sheet = await screen.findByRole('dialog', { name: '选择群聊' })
    fireEvent.click(within(sheet).getByRole('button', { name: /前端动效评审/ }))
    await waitFor(() => expect(screen.getByRole('button', { name: '切换群聊' })).toHaveTextContent('前端动效评审'))
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
