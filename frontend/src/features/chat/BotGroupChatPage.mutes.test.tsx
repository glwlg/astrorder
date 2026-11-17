import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import type { BotGroup } from '../../domain/types'
import { useAstrorderStore } from '../../state/store'
import { BotGroupChatPage } from './BotGroupChatPage'

const group: BotGroup = {
  id: 'mute-group',
  name: '方案与开发',
  members: [
    { id: 'design', machine_id: 'local', agent_id: 'codex', alias: '设计' },
    { id: 'developer', machine_id: 'local', agent_id: 'codex', alias: '开发' },
  ],
  member_states: { design: { status: 'success' }, developer: { status: 'success' } },
  coordination: { status: 'complete' },
  max_hops: 10,
  created_at: '',
  updated_at: '',
}

beforeEach(() => {
  vi.spyOn(api, 'getGroupArtifacts').mockResolvedValue({ items: [], references: [], checking: false })
  vi.spyOn(api, 'listGroupMessages').mockResolvedValue({ items: [], count: 0 })
  vi.spyOn(api, 'getBotGroup').mockResolvedValue({ group })
})

afterEach(() => {
  vi.restoreAllMocks()
  useAstrorderStore.setState({ drafts: {} })
})

describe('group member mutes', () => {
  it('toggles the exact member without inserting a mention or changing the draft', async () => {
    const update = vi.spyOn(api, 'updateBotGroup')
      .mockResolvedValueOnce({
        ok: true,
        group: { ...group, member_states: { ...group.member_states, design: { status: 'success', muted: true } } },
      })
      .mockResolvedValueOnce({ ok: true, group })
    render(<MantineProvider><BotGroupChatPage group={group} agents={{}} /></MantineProvider>)
    await screen.findByText('群聊已就绪')
    fireEvent.change(screen.getByLabelText('群聊消息'), { target: { value: '先记下开发要求' } })
    fireEvent.click(screen.getByRole('button', { name: '设计禁言' }))
    await waitFor(() => expect(screen.getByRole('button', { name: '设计禁言' })).toHaveAttribute('aria-pressed', 'true'))
    expect(update).toHaveBeenNthCalledWith(1, group.id, { member_mutes: { design: true } })
    expect(within(screen.getByRole('button', { name: '设计禁言' })).getByText('已禁言')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '开发禁言' })).toHaveAttribute('aria-pressed', 'false')
    expect(screen.getByRole('button', { name: '所有成员禁言' })).toHaveAttribute('aria-pressed', 'mixed')
    expect(screen.getByLabelText('群聊消息')).toHaveValue('先记下开发要求')
    fireEvent.click(screen.getByRole('button', { name: '设计禁言' }))
    await waitFor(() => expect(screen.getByRole('button', { name: '设计禁言' })).toHaveAttribute('aria-pressed', 'false'))
    expect(update).toHaveBeenNthCalledWith(2, group.id, { member_mutes: { design: false } })
    expect(screen.getByLabelText('群聊消息')).toHaveValue('先记下开发要求')
  })

  it('mutes and restores everyone from a partially muted group', async () => {
    const partial: BotGroup = { ...group, member_states: { ...group.member_states, design: { status: 'success', muted: true } } }
    vi.mocked(api.getBotGroup).mockResolvedValue({ group: partial })
    const update = vi.spyOn(api, 'updateBotGroup')
      .mockResolvedValueOnce({ ok: true, group: {
        ...group, member_states: { design: { status: 'success', muted: true }, developer: { status: 'success', muted: true } },
      } })
      .mockResolvedValueOnce({ ok: true, group })
    const user = userEvent.setup()
    render(<MantineProvider><BotGroupChatPage group={partial} agents={{}} /></MantineProvider>)
    await screen.findByText('群聊已就绪')
    const everyone = screen.getByRole('button', { name: '所有成员禁言' })
    expect(everyone).toHaveAttribute('aria-pressed', 'mixed')
    everyone.focus()
    await user.keyboard('[Space]')
    await waitFor(() => expect(everyone).toHaveAttribute('aria-pressed', 'true'))
    expect(update).toHaveBeenNthCalledWith(1, group.id, { member_mutes: { design: true, developer: true } })
    expect(screen.getAllByText('已禁言')).toHaveLength(2)
    expect(within(everyone).getByText('全部恢复')).toBeInTheDocument()
    await user.keyboard('[Enter]')
    await waitFor(() => expect(everyone).toHaveAttribute('aria-pressed', 'false'))
    expect(update).toHaveBeenNthCalledWith(2, group.id, { member_mutes: { design: false, developer: false } })
    expect(screen.queryByText('已禁言')).not.toBeInTheDocument()
    expect(screen.getByLabelText('群聊消息')).toHaveValue('')
  })

  it('keeps the previous state when saving fails and prevents duplicate requests', async () => {
    let rejectUpdate!: (reason: Error) => void
    const update = vi.spyOn(api, 'updateBotGroup').mockImplementation(() => new Promise((_, reject) => { rejectUpdate = reject }))
    const notify = vi.spyOn(notifications, 'show')
    render(<MantineProvider><BotGroupChatPage group={group} agents={{}} /></MantineProvider>)
    await screen.findByText('群聊已就绪')
    const design = screen.getByRole('button', { name: '设计禁言' })
    fireEvent.click(design)
    fireEvent.click(design)
    expect(update).toHaveBeenCalledTimes(1)
    expect(design).toHaveAttribute('aria-disabled', 'true')
    expect(design).toHaveAttribute('aria-pressed', 'false')
    await act(async () => { rejectUpdate(new Error('保存失败')) })
    expect(design).toHaveAttribute('aria-disabled', 'false')
    expect(design).toHaveAttribute('aria-pressed', 'false')
    expect(notify).toHaveBeenCalledWith({ color: 'red', message: '保存失败' })
  })

  it('ignores an older polling snapshot that returns after a mute update', async () => {
    let resolveSnapshot!: (value: { group: BotGroup }) => void
    vi.mocked(api.getBotGroup).mockImplementationOnce(() => new Promise((resolve) => { resolveSnapshot = resolve }))
    vi.spyOn(api, 'updateBotGroup').mockResolvedValue({
      ok: true,
      group: { ...group, member_states: { ...group.member_states, design: { status: 'success', muted: true } } },
    })
    render(<MantineProvider><BotGroupChatPage group={group} agents={{}} /></MantineProvider>)
    fireEvent.click(screen.getByRole('button', { name: '设计禁言' }))
    await waitFor(() => expect(screen.getByRole('button', { name: '设计禁言' })).toHaveAttribute('aria-pressed', 'true'))
    await act(async () => { resolveSnapshot({ group }) })
    expect(screen.getByRole('button', { name: '设计禁言' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: '开发禁言' })).toHaveAttribute('aria-pressed', 'false')
  })
})
