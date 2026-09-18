import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { describe, expect, it, vi } from 'vitest'
import { BotGroupChatPage } from './BotGroupChatPage'
import { api } from '../../api/client'
import type { Agent, BotGroup, GroupMessage } from '../../domain/types'

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

describe('BotGroupChatPage', () => {
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
})
