import { act, render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { BotGroupChatPage } from './BotGroupChatPage'
import { api } from '../../api/client'
import type { Agent, BotGroup, GroupMessage } from '../../domain/types'
import { useAstrorderStore } from '../../state/store'
import { groupSidecarKey, useSidecarStore } from '../sidecar/sidecarStore'

const group: BotGroup = {
  id: 'group-1',
  name: '测试群聊',
  description: '协作开发',
  members: [
    { machine_id: 'local', agent_id: 'local-codex', name: 'Codex', alias: '前端开发' },
    { machine_id: 'ssh-deb', agent_id: 'remote-hermes', name: 'Hermes', alias: '后端运维' },
  ],
  max_hops: 3,
  active_hop: 1,
  active_speaker_agent_id: 'local-codex',
  created_at: '2026-09-17T00:00:00Z',
  updated_at: '2026-09-17T00:00:00Z',
}

const agents: Record<string, Agent> = {
  'local-codex': { id: 'local-codex', kind: 'codex', name: 'Codex', status: 'ready', capabilities: ['chat'], limitation: null },
  'remote-hermes': { id: 'remote-hermes', kind: 'hermes', name: 'Hermes', status: 'ready', capabilities: ['chat'], limitation: null },
}

const initialMessages: GroupMessage[] = [
  {
    id: 'm-1',
    group_id: 'group-1',
    sender_type: 'user',
    sender_id: 'human',
    text: '请 @Codex 进行组件修改',
    mentions: ['local-codex'],
    hop_count: 0,
    created_at: '2026-09-17T00:00:00Z',
  },
]

beforeEach(() => {
  vi.spyOn(api, 'getGroupArtifacts').mockResolvedValue({ items: [], references: [], checking: false })
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.useRealTimers()
  useAstrorderStore.setState({ sessions: {}, drafts: {} })
})

describe('BotGroupChatPage', () => {
  it('pastes an image once, sends image-only content, and renders the stored image', async () => {
    vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:group-image')
    const revoke = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
    vi.spyOn(api, 'getBotGroup').mockResolvedValue({ group })
    vi.spyOn(api, 'listGroupMessages').mockResolvedValue({ items: [], count: 0 })
    const image = { id: 'image', name: 'shot.png', media_type: 'image/png', url: '/api/v1/attachments/image' }
    const upload = vi.spyOn(api, 'uploadAttachment').mockResolvedValue(image)
    const sent: GroupMessage = { ...initialMessages[0], text: '', attachments: [image] }
    const send = vi.spyOn(api, 'sendGroupMessage').mockImplementation(async () => {
      vi.mocked(api.listGroupMessages).mockResolvedValue({ items: [sent], count: 1 })
      return { ok: true, message: sent, target_agent_id: null, history_count: 1 }
    })
    render(<MantineProvider><BotGroupChatPage group={group} agents={agents} /></MantineProvider>)
    await screen.findByText('群聊已就绪')
    const file = new File(['image bytes'], 'shot.png', { type: 'image/png' })
    fireEvent.paste(screen.getByLabelText('群聊消息'), { clipboardData: { files: [file], items: [{ kind: 'file', getAsFile: () => file }] } })
    expect(screen.getAllByAltText('待发送：shot.png')).toHaveLength(1)
    expect(screen.getByRole('button', { name: '发送' })).toBeEnabled()
    fireEvent.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(send).toHaveBeenCalledWith(group.id, { text: '', attachment_ids: ['image'] }))
    expect(upload).toHaveBeenCalledExactlyOnceWith(file)
    expect(await screen.findByRole('button', { name: '查看图片：shot.png' })).toBeInTheDocument()
    expect(screen.queryByAltText('待发送：shot.png')).not.toBeInTheDocument()
    expect(revoke).toHaveBeenCalledWith('blob:group-image')
    fireEvent.click(screen.getByRole('button', { name: '查看图片：shot.png' }))
    expect(await screen.findByRole('dialog', { name: 'shot.png' })).toBeInTheDocument()
  })

  it('retains text and images on partial upload failure and reuses successful uploads', async () => {
    vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:group-image')
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
    vi.spyOn(api, 'getBotGroup').mockResolvedValue({ group })
    vi.spyOn(api, 'listGroupMessages').mockResolvedValue({ items: initialMessages, count: 1 })
    const upload = vi.spyOn(api, 'uploadAttachment')
      .mockResolvedValueOnce({ id: 'first', name: 'first.png', media_type: 'image/png', url: '/first' })
      .mockRejectedValueOnce(new Error('上传中断'))
      .mockResolvedValueOnce({ id: 'second', name: 'second.png', media_type: 'image/png', url: '/second' })
    const send = vi.spyOn(api, 'sendGroupMessage').mockResolvedValue({ ok: true, message: initialMessages[0], target_agent_id: null, history_count: 2 })
    render(<MantineProvider><BotGroupChatPage group={group} agents={agents} /></MantineProvider>)
    await screen.findByText(initialMessages[0].text)
    fireEvent.change(screen.getByLabelText('群聊消息'), { target: { value: '看这两张图' } })
    const files = ['first', 'second'].map((name) => new File(['image'], `${name}.png`, { type: 'image/png' }))
    fireEvent.paste(screen.getByLabelText('群聊消息'), { clipboardData: { files, items: [] } })
    fireEvent.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(screen.getByRole('button', { name: '发送' })).toBeEnabled())
    expect(send).not.toHaveBeenCalled()
    expect(screen.getByLabelText('群聊消息')).toHaveValue('看这两张图')
    expect(screen.getAllByAltText(/^待发送/)).toHaveLength(2)
    fireEvent.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(send).toHaveBeenCalledWith(group.id, { text: '看这两张图', attachment_ids: ['first', 'second'] }))
    expect(upload).toHaveBeenCalledTimes(3)
    expect(upload.mock.calls[2][0]).toBe(files[1])
  })

  it('keeps each group draft separate while switching groups and allows removing pasted images', async () => {
    vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:group-image')
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
    vi.spyOn(api, 'getBotGroup').mockResolvedValue({ group })
    vi.spyOn(api, 'listGroupMessages').mockResolvedValue({ items: [], count: 0 })
    const view = render(<MantineProvider><BotGroupChatPage group={group} agents={agents} /></MantineProvider>)
    await screen.findByText('群聊已就绪')
    fireEvent.change(screen.getByLabelText('群聊消息'), { target: { value: '仅发到原群' } })
    fireEvent.paste(screen.getByLabelText('群聊消息'), { clipboardData: { files: [new File(['image'], 'shot.png', { type: 'image/png' })], items: [] } })
    view.rerender(<MantineProvider><BotGroupChatPage group={{ ...group, id: 'other-group' }} agents={agents} /></MantineProvider>)
    expect(screen.getByLabelText('群聊消息')).toHaveValue('')
    expect(screen.queryByAltText('待发送：shot.png')).not.toBeInTheDocument()
    view.rerender(<MantineProvider><BotGroupChatPage group={group} agents={agents} /></MantineProvider>)
    expect(screen.getByLabelText('群聊消息')).toHaveValue('仅发到原群')
    expect(screen.getByAltText('待发送：shot.png')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '移除 shot.png' }))
    expect(screen.queryByAltText('待发送：shot.png')).not.toBeInTheDocument()
  })

  it('renders rich replies while preserving multiline user text and isolating member filesystems', async () => {
    const richGroup: BotGroup = {
      ...group,
      members: [
        { id: 'design', machine_id: 'ssh:design', agent_id: 'local-codex', alias: '设计', workspace: '/design' },
        { id: 'dev', machine_id: 'ssh:dev', agent_id: 'local-codex', alias: '开发', workspace: '/dev' },
      ],
      member_states: { design: { status: 'success', session_id: 'design-thread' }, dev: { status: 'success', session_id: 'dev-thread' } },
      coordination: { status: 'complete' },
    }
    useAstrorderStore.setState({ sessions: {
      unrelated: { id: 'unrelated', agent_id: 'other', title: '其他会话', workspace: '/other', connection_id: 'ssh:other', status: 'idle', updated_at: '' },
    } })
    const replies: GroupMessage[] = [
      { ...initialMessages[0], text: '第一行\n第二行\n\n第四行\n\n![用户图片](/tmp/user.png)' },
      { ...initialMessages[0], id: 'rich', sender_type: 'agent', sender_id: 'local-codex', sender_member_id: 'design',
        text: '# 设计结论\n\n**授权范围**\n\n> 复核通过\n\n| 模块 | 状态 |\n| --- | --- |\n| API | 通过 |\n\n```go\nfmt.Println("ok")\n```\n\n![设计图](output/result.png)\n\n<script>window.evil = 1</script>' },
      { ...initialMessages[0], id: 'dev', sender_type: 'agent', sender_id: 'local-codex', sender_member_id: 'dev', text: '![开发图](output/result.png)' },
    ]
    vi.spyOn(api, 'getBotGroup').mockResolvedValue({ group: richGroup })
    vi.spyOn(api, 'listGroupMessages').mockResolvedValue({ items: replies, count: replies.length })
    vi.spyOn(api, 'getWorkspaceRootFiles').mockResolvedValue({ root: '', items: [] })
    vi.spyOn(api, 'checkWorkspaceFiles').mockResolvedValue({ existing: [] })
    useSidecarStore.getState().switchSession(groupSidecarKey(richGroup.id))
    vi.mocked(api.getGroupArtifacts).mockResolvedValue({ items: [], checking: false, references: [{
      id: 'file-aaaa', group_id: richGroup.id, member_id: 'design', member_name: '设计', agent_id: 'local-codex',
      session_id: 'design-thread', connection_id: 'ssh:design', workspace: '/design', path: '/design/output/result.png',
      aliases: ['output/result.png'], content_key: 'artifacts.file-aaaa', name: 'result.png', created_at: '',
    }] })
    const { container } = render(<MantineProvider><BotGroupChatPage group={richGroup} agents={agents} /></MantineProvider>)
    expect(await screen.findByRole('heading', { name: '设计结论' })).toBeInTheDocument()
    expect(screen.getByText('授权范围').tagName).toBe('STRONG')
    expect(screen.getByText('复核通过').closest('blockquote')).not.toBeNull()
    expect(within(screen.getByRole('table')).getByRole('cell', { name: 'API' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '复制代码' })).toBeInTheDocument()
    expect(container.querySelector('.message-user-prose p')).toHaveTextContent('第一行 第二行')
    expect(container.querySelector('.message-user-prose p')?.textContent).toBe('第一行\n第二行')
    expect(container.querySelector('.message-user-prose')?.textContent).toContain('第四行')
    expect(container.querySelector('script')).toBeNull()
    for (const [label, path, thread, connection] of [
      ['设计图', '/design/output/result.png', 'design-thread', 'ssh:design'],
      ['开发图', '/design/output/result.png', 'design-thread', 'ssh:design'],
    ]) {
      await screen.findByAltText(label)
      const src = new URL(screen.getByAltText(label).getAttribute('src')!, 'http://localhost')
      expect(src.searchParams.get('path')).toBe(path)
      expect(src.searchParams.get('session_id')).toBe(thread)
      expect(src.searchParams.get('connection_id')).toBe(connection)
    }
    expect(screen.getByText('用户图片')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '用户图片' })).not.toBeInTheDocument()
    expect(screen.queryByAltText('用户图片')).not.toBeInTheDocument()
  })

  it('places live progress after the messages and removes it on stop without waiting for a poll', async () => {
    const running: BotGroup = { ...group, coordination: { status: 'running' }, member_states: { 'local-codex': { status: 'running' } } }
    vi.spyOn(api, 'getBotGroup').mockResolvedValue({ group: running })
    vi.spyOn(api, 'listGroupMessages').mockResolvedValue({ items: initialMessages, count: 1 })
    vi.spyOn(api, 'stopGroup').mockResolvedValue({ ok: true, group: running })
    const { container } = render(<MantineProvider><BotGroupChatPage group={running} agents={agents} /></MantineProvider>)
    await screen.findByText('请 @Codex 进行组件修改')
    const messages = container.querySelector('.bot-group-chat-messages')!
    const status = within(messages as HTMLElement).getByRole('status', { name: '群聊处理进度' })
    expect(status).toHaveTextContent('前端开发 正在处理中')
    expect(messages.lastElementChild).toContainElement(status)
    fireEvent.click(screen.getByRole('button', { name: '暂停群聊动效' }))
    expect(status.parentElement).toHaveAttribute('data-motion-paused', 'true')
    expect(screen.getByRole('button', { name: '播放群聊动效' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '播放状态动效' }))
    expect(status.parentElement).not.toHaveAttribute('data-motion-paused')
    expect(screen.getByRole('button', { name: '暂停群聊动效' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '中断流转' }))
    await waitFor(() => expect(status).toBeEmptyDOMElement())
  })

  it('follows new messages only while the reader stays near the bottom', async () => {
    vi.useFakeTimers()
    const history = vi.spyOn(api, 'listGroupMessages').mockResolvedValue({ items: initialMessages, count: 1 })
    vi.spyOn(api, 'getBotGroup').mockResolvedValue({ group })
    const { container } = render(<MantineProvider><BotGroupChatPage group={group} agents={agents} /></MantineProvider>)
    await act(async () => {})
    const chat = container.querySelector('.bot-group-chat-messages') as HTMLDivElement
    Object.defineProperties(chat, { scrollHeight: { configurable: true, value: 2000 }, clientHeight: { configurable: true, value: 300 } })
    chat.scrollTop = 100
    fireEvent.scroll(chat)
    const reply: GroupMessage = { ...initialMessages[0], id: 'new-reply', sender_type: 'agent', text: '新的回复' }
    history.mockResolvedValue({ items: [...initialMessages, reply], count: 2 })
    await act(() => vi.advanceTimersByTimeAsync(3000))
    expect(screen.getByText('新的回复')).toBeInTheDocument()
    expect(chat.scrollTop).toBe(100)
    chat.scrollTop = 1700
    fireEvent.scroll(chat)
    history.mockResolvedValue({ items: [...initialMessages, reply, { ...reply, id: 'next', text: '下一条回复' }], count: 3 })
    await act(() => vi.advanceTimersByTimeAsync(3000))
    expect(chat.scrollTop).toBe(2000)
  })

  it('renders messages and handles user input with @mention', async () => {
    vi.spyOn(api, 'getBotGroup').mockResolvedValue({ group })
    vi.spyOn(api, 'listGroupMessages').mockResolvedValue({ items: initialMessages, count: 1 })
    const sendSpy = vi.spyOn(api, 'sendGroupMessage').mockResolvedValue({
      ok: true,
      message: {
        id: 'm-2',
        group_id: 'group-1',
        sender_type: 'user',
        sender_id: 'human',
        text: '继续任务',
        mentions: [],
        hop_count: 0,
        created_at: '2026-09-17T00:01:00Z',
      },
      target_agent_id: 'local-codex',
      history_count: 2,
    })

    render(
      <MantineProvider>
        <BotGroupChatPage group={group} agents={agents} />
      </MantineProvider>,
    )

    expect(await screen.findByText('测试群聊')).toBeInTheDocument()
    expect(await screen.findByText('请 @Codex 进行组件修改')).toBeInTheDocument()

    // Send new message
    const textarea = screen.getByPlaceholderText(/输入消息/)
    fireEvent.change(textarea, { target: { value: '继续任务' } })
    fireEvent.click(screen.getByRole('button', { name: '发送' }))

    await waitFor(() => {
      expect(sendSpy).toHaveBeenCalledWith('group-1', { text: '继续任务' })
    })

    sendSpy.mockRestore()
  })

  it('allows stopping active group orchestration', async () => {
    vi.spyOn(api, 'getBotGroup').mockResolvedValue({ group })
    vi.spyOn(api, 'listGroupMessages').mockResolvedValue({ items: initialMessages, count: 1 })
    const stopSpy = vi.spyOn(api, 'stopGroup').mockResolvedValue({ ok: true, group })

    render(
      <MantineProvider>
        <BotGroupChatPage group={group} agents={agents} />
      </MantineProvider>,
    )

    const stopBtns = await screen.findAllByRole('button', { name: '中断流转' })
    expect(stopBtns.length).toBeGreaterThan(0)
    fireEvent.click(stopBtns[0])

    await waitFor(() => {
      expect(stopSpy).toHaveBeenCalledWith('group-1')
    })

    stopSpy.mockRestore()
  })

  it('addresses and displays same-Agent members by their independent identity', async () => {
    const duplicateGroup: BotGroup = {
      ...group,
      active_speaker_agent_id: 'two',
      active_speakers: ['two'],
      members: [
        { id: 'one', machine_id: 'local', agent_id: 'local-codex', name: 'Codex', alias: 'Codex-1', workspace: '/one' },
        { id: 'two', machine_id: 'local', agent_id: 'local-codex', name: 'Codex', alias: 'Codex-2', workspace: '/two' },
      ],
      member_states: { one: { status: 'success' }, two: { status: 'running' } },
    }
    const replies: GroupMessage[] = [
      { ...initialMessages[0], id: 'reply-1', sender_type: 'agent', sender_id: 'local-codex', sender_member_id: 'one', text: '来自第一个成员' },
      { ...initialMessages[0], id: 'reply-2', sender_type: 'agent', sender_id: 'local-codex', sender_member_id: 'two', text: '来自第二个成员' },
    ]
    vi.spyOn(api, 'getBotGroup').mockResolvedValue({ group: duplicateGroup })
    vi.spyOn(api, 'listGroupMessages').mockResolvedValue({ items: replies, count: 2 })
    render(<MantineProvider><BotGroupChatPage group={duplicateGroup} agents={agents} /></MantineProvider>)

    expect(await screen.findByRole('article', { name: 'Codex-1 (Codex)的消息' })).toBeInTheDocument()
    expect(screen.getByRole('article', { name: 'Codex-2 (Codex)的消息' })).toBeInTheDocument()
    const secondMember = screen.getByRole('button', { name: 'Codex-2禁言' })
    expect(secondMember).toHaveTextContent('执行中')
    expect(secondMember).not.toHaveTextContent('/two')
    expect(secondMember).toHaveAttribute('aria-description', expect.stringContaining('/two'))
    expect(secondMember.dataset.tone).not.toBe(screen.getByRole('button', { name: 'Codex-1禁言' }).dataset.tone)
    fireEvent.change(screen.getByPlaceholderText(/输入消息/), { target: { value: '@Codex-2' } })
    fireEvent.keyDown(screen.getByPlaceholderText(/输入消息/), { key: 'Enter' })
    expect(screen.getByPlaceholderText(/输入消息/)).toHaveValue('@Codex-2 ')
  })

  it('offers recovery for timed-out replies and retries failed decisions', async () => {
    const blocked: BotGroup = {
      ...group,
      active_speaker_agent_id: null,
      member_states: { 'local-codex': { status: 'failed', last_error: 'context deadline exceeded' } },
      coordination: { status: 'error', error: 'System One 判断失败' },
    }
    vi.spyOn(api, 'getBotGroup').mockResolvedValue({ group: blocked })
    vi.spyOn(api, 'listGroupMessages').mockResolvedValue({ items: initialMessages, count: 1 })
    const recover = vi.spyOn(api, 'recoverGroup').mockResolvedValue({ ok: true, group: blocked })
    const retry = vi.spyOn(api, 'retryGroup').mockResolvedValue({ ok: true, group: blocked })
    render(<MantineProvider><BotGroupChatPage group={blocked} agents={agents} /></MantineProvider>)

    expect(await screen.findByText('System One 判断失败')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '恢复回复并继续' }))
    await waitFor(() => expect(recover).toHaveBeenCalledWith('group-1'))
    fireEvent.click(screen.getByRole('button', { name: '重新判断' }))
    await waitFor(() => expect(retry).toHaveBeenCalledWith('group-1'))
  })

  it('keeps the pause reason above long messages and marks waiting members as unsent', async () => {
    const paused: BotGroup = {
      ...group,
      active_hop: 0,
      // Coordination and member state take precedence over old speaker fields.
      member_states: { 'local-codex': { status: 'waiting' }, 'remote-hermes': { status: 'success' } },
      coordination: { status: 'error', error: '第 4 次接力超过上限 3 次，消息尚未发送' },
    }
    const longMessage = { ...initialMessages[0], id: 'long', sender_type: 'agent' as const, text: '很长的设计回复\n'.repeat(200), hop_count: 3 }
    vi.spyOn(api, 'getBotGroup').mockResolvedValue({ group: paused })
    vi.spyOn(api, 'listGroupMessages').mockResolvedValue({ items: [initialMessages[0], longMessage], count: 2 })
    const { container } = render(<MantineProvider><BotGroupChatPage group={paused} agents={agents} /></MantineProvider>)
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('消息尚未发送')
    expect(container.querySelector('.bot-group-chat-messages')).not.toContainElement(alert)
    expect(screen.getByText('已暂停')).toBeInTheDocument()
    expect(within(screen.getByRole('button', { name: '前端开发禁言' })).getByText('未发送')).toBeInTheDocument()
    expect(within(screen.getByRole('button', { name: '后端运维禁言' })).getByText('完成')).toBeInTheDocument()
    expect(await screen.findByText('接力 3/3')).toBeInTheDocument()
    expect(screen.queryByText(/等待中|接力流转中/)).not.toBeInTheDocument()
  })

  it('shows System One deferred deliveries consistently in the header and member list', async () => {
    const waiting: BotGroup = {
      ...group, active_hop: 0, active_speaker_agent_id: null,
      member_states: { 'local-codex': { status: 'waiting' } },
      coordination: { status: 'waiting' },
    }
    vi.spyOn(api, 'getBotGroup').mockResolvedValue({ group: waiting })
    vi.spyOn(api, 'listGroupMessages').mockResolvedValue({ items: initialMessages, count: 1 })
    render(<MantineProvider><BotGroupChatPage group={waiting} agents={agents} /></MantineProvider>)
    expect((await screen.findAllByText('暂缓发送')).length).toBe(2)
    expect(within(screen.getByRole('button', { name: '前端开发禁言' })).getByText('暂缓发送')).toBeInTheDocument()
    expect(screen.getByRole('status', { name: '群聊发送状态' })).toHaveTextContent('System One 暂缓发送，待发消息已保留')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('refreshes the displayed limit after changing settings while retaining the paused state', async () => {
    const paused: BotGroup = { ...group, active_hop: 0, active_speaker_agent_id: null, coordination: { status: 'error', error: '接力达到上限' } }
    const updated = { ...paused, max_hops: 20 }
    vi.spyOn(api, 'getBotGroup').mockResolvedValue({ group: paused })
    vi.spyOn(api, 'listGroupMessages').mockResolvedValue({ items: initialMessages, count: 1 })
    const page = (value: BotGroup) => <MantineProvider><BotGroupChatPage group={value} agents={agents} /></MantineProvider>
    const { rerender } = render(page(paused))
    expect(await screen.findByText('请 @Codex 进行组件修改')).toBeInTheDocument()
    rerender(page(updated))
    expect(await screen.findByText('接力 0/20')).toBeInTheDocument()
    expect(screen.getByText('已暂停')).toBeInTheDocument()
  })

  it('shows history failures without a false empty group and still refreshes the group state', async () => {
    const paused: BotGroup = {
      ...group, max_hops: 10, active_hop: 0, active_speaker_agent_id: null,
      member_states: { 'local-codex': { status: 'waiting' } },
      coordination: { status: 'error', error: '读取群聊记录失败' },
    }
    vi.spyOn(api, 'getBotGroup').mockResolvedValue({ group: paused })
    const history = vi.spyOn(api, 'listGroupMessages')
      .mockRejectedValueOnce(new Error('session is not daemon-owned'))
      .mockResolvedValue({ items: initialMessages, count: 1 })
    const retry = vi.spyOn(api, 'retryGroup')
    const send = vi.spyOn(api, 'sendGroupMessage')
    render(<MantineProvider><BotGroupChatPage group={group} agents={agents} /></MantineProvider>)
    expect(await screen.findByRole('alert')).toHaveTextContent('群聊记录加载失败：session is not daemon-owned')
    expect(screen.getByText('已暂停')).toBeInTheDocument()
    expect(within(screen.getByRole('button', { name: '前端开发禁言' })).getByText('未发送')).toBeInTheDocument()
    expect(screen.getByText('接力 —/10')).toBeInTheDocument()
    expect(screen.queryByText(/群聊已就绪/)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重新判断' })).toBeDisabled()
    fireEvent.change(screen.getByPlaceholderText(/输入消息/), { target: { value: '追加说明' } })
    fireEvent.keyDown(screen.getByPlaceholderText(/输入消息/), { key: 'Enter' })
    expect(send).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: '重新加载记录' }))
    expect(await screen.findByText('请 @Codex 进行组件修改')).toBeInTheDocument()
    expect(history).toHaveBeenCalledTimes(2)
    expect(screen.getByRole('button', { name: '重新判断' })).toBeEnabled()
    expect(screen.queryByRole('button', { name: '重新加载记录' })).not.toBeInTheDocument()
    expect(retry).not.toHaveBeenCalled()
    expect(send).not.toHaveBeenCalled()
  })

  it('keeps successfully loaded messages when only the group status request fails', async () => {
    vi.spyOn(api, 'getBotGroup').mockRejectedValue(new Error('状态服务暂时不可用'))
    vi.spyOn(api, 'listGroupMessages').mockResolvedValue({ items: initialMessages, count: 1 })
    render(<MantineProvider><BotGroupChatPage group={group} agents={agents} /></MantineProvider>)
    expect(await screen.findByText('请 @Codex 进行组件修改')).toBeInTheDocument()
    expect(screen.getByRole('alert')).toHaveTextContent('群聊状态更新失败：状态服务暂时不可用')
    expect(screen.queryByText(/群聊已就绪/)).not.toBeInTheDocument()
  })

  it('does not overlap slow history requests during polling', async () => {
    vi.useFakeTimers()
    let resolveHistory!: (value: { items: GroupMessage[]; count: number }) => void
    const pending = new Promise<{ items: GroupMessage[]; count: number }>(resolve => { resolveHistory = resolve })
    const history = vi.spyOn(api, 'listGroupMessages').mockReturnValue(pending)
    vi.spyOn(api, 'getBotGroup').mockResolvedValue({ group })
    render(<MantineProvider><BotGroupChatPage group={group} agents={agents} /></MantineProvider>)
    expect(screen.getByText('正在加载群聊记录…')).toBeInTheDocument()
    expect(screen.queryByText(/群聊已就绪/)).not.toBeInTheDocument()
    await act(() => vi.advanceTimersByTimeAsync(9000))
    expect(history).toHaveBeenCalledTimes(1)
    await act(async () => { resolveHistory({ items: initialMessages, count: 1 }) })
    expect(screen.getByText('请 @Codex 进行组件修改')).toBeInTheDocument()
  })

  it('ignores a late response from the previously selected group', async () => {
    let resolveOld!: (value: { items: GroupMessage[]; count: number }) => void
    const oldResponse = new Promise<{ items: GroupMessage[]; count: number }>(resolve => { resolveOld = resolve })
    const other: BotGroup = { ...group, id: 'other', name: '另一个群', active_hop: 0, active_speaker_agent_id: null }
    vi.spyOn(api, 'getBotGroup').mockImplementation(async id => ({ group: id === group.id ? group : other }))
    vi.spyOn(api, 'listGroupMessages').mockImplementation(id => id === group.id ? oldResponse : Promise.resolve({ items: [], count: 0 }))
    const page = (value: BotGroup) => <MantineProvider><BotGroupChatPage group={value} agents={agents} /></MantineProvider>
    const { rerender } = render(page(group))
    rerender(page(other))
    expect(await screen.findByText(/群聊已就绪/)).toBeInTheDocument()
    await act(async () => { resolveOld({ items: initialMessages, count: 1 }) })
    expect(screen.getByText('另一个群')).toBeInTheDocument()
    expect(screen.queryByText('请 @Codex 进行组件修改')).not.toBeInTheDocument()
  })
})
