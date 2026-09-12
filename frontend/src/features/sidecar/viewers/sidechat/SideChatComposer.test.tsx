import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '../../../../api/client'
import type { Agent, Message, Session } from '../../../../domain/types'
import { scopeKey } from '../../../../domain/semantics'
import { useAstrorderStore } from '../../../../state/store'
import { SideChatViewer } from './SideChatViewer'

const parent: Session = { id: 'parent', agent_id: 'fixture-agent', title: '主会话', workspace: null, status: 'idle', updated_at: '2026-09-11T00:00:00Z' }
const child: Session = { ...parent, id: 'child', title: '侧边聊天', ephemeral: true }
const agent: Agent = { id: parent.agent_id, kind: 'codex', name: 'Fixture only', status: 'ready', capabilities: ['chat', 'attachments', 'stop'], limitation: null }
const message = (sessionId: string, id: string, text: string): Message => ({ id, agent_id: agent.id, session_id: sessionId, role: 'assistant', kind: 'message', text, created_at: parent.updated_at, attachments: [], command_id: null, tool: null })
let client: QueryClient

afterEach(() => { cleanup(); client?.clear(); vi.restoreAllMocks(); useAstrorderStore.getState().resetRuntime() })

it('renders the real main composer and sends only to the child with independent drafts and replies', async () => {
  const store = useAstrorderStore.getState()
  store.resetRuntime()
  store.hydrateBootstrap({ protocol_version: 1, agents: [agent], sessions: [parent], cursor: 1 })
  store.mergeMessages(agent.id, parent.id, [message(parent.id, 'old', '主会话上下文不显示')])
  store.setDraft(agent.id, parent.id, { text: '主会话草稿保留', attachments: [] })
  vi.spyOn(api, 'createSession').mockResolvedValue(child)
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'fixture-model', provider: 'fixture' })
  vi.spyOn(api, 'getSessionApprovalMode').mockResolvedValue({ mode: 'manual' })
  const create = vi.spyOn(api, 'createCommand').mockImplementation(async payload => ({ ...payload, state: 'completed', attachments: [], created_at: parent.updated_at, error: null }))
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const { container } = render(<MantineProvider><QueryClientProvider client={client}>
    <SideChatViewer artifact={{ id: 'sidechat:parent', sessionId: parent.id, agentId: agent.id, name: '侧边聊天', kind: 'workspace_file', readUrl: '', mediaType: 'application/x-astrorder-side-chat', writable: false }} />
  </QueryClientProvider></MantineProvider>)
  const input = await screen.findByRole('textbox', { name: '消息内容' })
  expect(input.tagName).toBe('TEXTAREA')
  expect(input.closest('.composer-card')).not.toBeNull()
  expect(container.querySelector('.sc-chat-input')).toBeNull()
  expect(screen.getByLabelText('添加附件')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: '选择会话模型' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: '语音输入' })).toBeInTheDocument()
  expect(screen.queryByText('主会话上下文不显示')).toBeNull()
  fireEvent.change(input, { target: { value: '只问侧边' } })
  fireEvent.click(screen.getByRole('button', { name: /^发送$/ }))
  await waitFor(() => expect(create).toHaveBeenCalledTimes(1))
  expect(create.mock.calls[0][0]).toMatchObject({ session_id: child.id, agent_id: agent.id, action: 'send', text: '只问侧边' })
  expect(useAstrorderStore.getState().drafts[scopeKey(agent.id, parent.id)].text).toBe('主会话草稿保留')
  act(() => {
    store.mergeMessages(agent.id, child.id, [message(child.id, 'reply', '侧边真实事件回复')])
    store.mergeMessages(agent.id, parent.id, [message(parent.id, 'live', '主会话后续消息不显示')])
  })
  expect(await screen.findByText('侧边真实事件回复')).toBeInTheDocument()
  expect(screen.queryByText('主会话后续消息不显示')).toBeNull()
  expect(screen.queryByText('已发送，等待回复…')).toBeNull()
})
