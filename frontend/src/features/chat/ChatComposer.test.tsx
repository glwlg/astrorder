import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import type { Agent, Session } from '../../domain/types'
import { useAstrorderStore } from '../../state/store'
import { ChatComposer } from './ChatComposer'

const queueMemory = vi.hoisted(() => ({ rows: [] as never[] }))
vi.mock('../mobile/mobileOutboxStorage', () => ({
  mobileOutboxStorage: {
    load: async () => queueMemory.rows,
    save: async (rows: never[]) => { queueMemory.rows = rows },
  },
}))

const session: Session = { id: 'native-one', agent_id: 'hermes', title: 'one', workspace: null, status: 'idle', updated_at: '2026-01-01T00:00:00Z' }
const agent: Agent = { id: 'hermes', name: 'Hermes', kind: 'hermes', status: 'ready', capabilities: ['chat', 'stop', 'attachments'], limitation: null }
afterEach(() => { cleanup(); vi.restoreAllMocks(); queueMemory.rows = []; useAstrorderStore.getState().resetRuntime() })
it('embeds the model picker and follows the native session status for stop', async () => {
  useAstrorderStore.getState().resetRuntime()
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'native-model', provider: 'provider' })
  const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: input.action === 'stop' ? 'completed' : 'running', attachments: [], created_at: session.updated_at, error: null, target_id: input.target_id ?? null }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrap = (value: Session) => <MantineProvider><QueryClientProvider client={client}><ChatComposer session={value} agent={agent} /></QueryClientProvider></MantineProvider>
  const view = render(wrap(session))
  const model = await screen.findByRole('button', { name: '选择会话模型' })
  expect(model.closest('.composer-card')).not.toBeNull()
  expect(screen.queryByText('模型', { exact: true })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '排队发送' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '停止' })).not.toBeInTheDocument()
  fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: 'hello' } })
  fireEvent.click(screen.getByRole('button', { name: '发送' }))
  await waitFor(() => expect(create).toHaveBeenCalledOnce())
  expect(screen.queryByRole('button', { name: '停止' })).not.toBeInTheDocument()
  view.rerender(wrap({ ...session, status: 'running' }))
  expect(screen.getByRole('button', { name: '停止' })).toBeEnabled()
  fireEvent.click(screen.getByRole('button', { name: '停止' }))
  await waitFor(() => expect(create).toHaveBeenCalledTimes(2))
  // Native stop requires target_id == session_id (the session to interrupt),
  // not a command id — see service._capability_error.
  expect(create.mock.calls[1][0]).toMatchObject({ action: 'stop', text: '', target_id: session.id, session_id: session.id })
  view.rerender(wrap(session))
  expect(screen.getByRole('button', { name: '发送' })).toBeInTheDocument()
  client.clear()
})
it('pastes clipboard images into the draft instead of ignoring them', async () => {
  useAstrorderStore.getState().resetRuntime()
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'native-model', provider: 'provider' })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<MantineProvider><QueryClientProvider client={client}><ChatComposer session={session} agent={agent} /></QueryClientProvider></MantineProvider>)
  const file = new File(['png'], 'shot.png', { type: 'image/png' })
  fireEvent.paste(screen.getByLabelText('消息内容'), { clipboardData: { files: [file], items: [{ kind: 'file', type: 'image/png', getAsFile: () => file }] } })
  expect(await screen.findByText('shot.png')).toBeInTheDocument()
  client.clear()
})

it('shows the native failure detail returned by a failed command', async () => {
  useAstrorderStore.getState().resetRuntime()
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'native-model', provider: 'provider' })
  const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: 'accepted', attachments: [], created_at: session.updated_at, error: null, target_id: input.target_id ?? null }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<MantineProvider><QueryClientProvider client={client}><ChatComposer session={session} agent={agent} /></QueryClientProvider></MantineProvider>)

  fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: 'hello' } })
  fireEvent.click(screen.getByRole('button', { name: '发送' }))
  await waitFor(() => expect(create).toHaveBeenCalledOnce())
  const submitted = create.mock.calls[0][0]
  act(() => useAstrorderStore.getState().mergeCommands([{
    ...submitted,
    state: 'failed',
    attachments: [],
    created_at: session.updated_at,
    error: 'Missing environment variable: OPENCODEX_API_AUTH_TOKEN.',
  }]))

  expect(await screen.findByText('Missing environment variable: OPENCODEX_API_AUTH_TOKEN.')).toBeInTheDocument()
  client.clear()
})

it('queues while a session is running and steers only after explicit confirmation', async () => {
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'native-model', provider: 'provider' })
  const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: 'accepted', attachments: [], created_at: session.updated_at, error: null, target_id: input.target_id ?? null }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<MantineProvider><QueryClientProvider client={client}><ChatComposer session={{ ...session, status: 'running' }} agent={agent} /></QueryClientProvider></MantineProvider>)

  fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '先排队' } })
  fireEvent.click(screen.getByRole('button', { name: '加入队列' }))
  expect(await screen.findByText('先排队')).toBeInTheDocument()
  expect(create).not.toHaveBeenCalled()

  fireEvent.click(screen.getByRole('button', { name: '立即引导' }))
  await waitFor(() => expect(create).toHaveBeenCalledOnce())
  expect(create.mock.calls[0][0]).toMatchObject({ action: 'send', text: '先排队' })
  await waitFor(() => expect(screen.queryByText('先排队')).not.toBeInTheDocument())
  client.clear()
})

it('edits a queued message by removing from queue and writing text back to input', async () => {
  useAstrorderStore.getState().resetRuntime()
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'native-model', provider: 'provider' })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<MantineProvider><QueryClientProvider client={client}><ChatComposer session={{ ...session, status: 'running' }} agent={agent} /></QueryClientProvider></MantineProvider>)

  const input = screen.getByLabelText('消息内容')
  fireEvent.change(input, { target: { value: '待编辑消息' } })
  fireEvent.click(screen.getByRole('button', { name: '加入队列' }))
  expect(await screen.findByText('待编辑消息')).toBeInTheDocument()
  expect(input).toHaveValue('')

  fireEvent.click(screen.getByRole('button', { name: '编辑' }))
  await waitFor(() => expect(screen.queryByLabelText('排队消息')).not.toBeInTheDocument())
  expect(input).toHaveValue('待编辑消息')
  client.clear()
})

it('deletes a queued message directly from outbox', async () => {
  useAstrorderStore.getState().resetRuntime()
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'native-model', provider: 'provider' })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<MantineProvider><QueryClientProvider client={client}><ChatComposer session={{ ...session, status: 'running' }} agent={agent} /></QueryClientProvider></MantineProvider>)

  const input = screen.getByLabelText('消息内容')
  fireEvent.change(input, { target: { value: '待删除消息' } })
  fireEvent.click(screen.getByRole('button', { name: '加入队列' }))
  expect(await screen.findByText('待删除消息')).toBeInTheDocument()

  fireEvent.click(screen.getByRole('button', { name: '删除' }))
  await waitFor(() => expect(screen.queryByLabelText('排队消息')).not.toBeInTheDocument())
  expect(input).toHaveValue('')
  client.clear()
})

it('does not swap send for stop just because native activity is recent', async () => {
  useAstrorderStore.getState().resetRuntime()
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'native-model', provider: 'provider' })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<MantineProvider><QueryClientProvider client={client}><ChatComposer session={{ ...session, live: true }} agent={agent} /></QueryClientProvider></MantineProvider>)
  expect(screen.getByRole('button', { name: '发送' })).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '停止' })).not.toBeInTheDocument()
  client.clear()
})

it('syncs its measured height to parent style as --composer-height and invokes onHeightChange', async () => {
  useAstrorderStore.getState().resetRuntime()
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'native-model', provider: 'provider' })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const onHeightChange = vi.fn()
  const { container } = render(
    <MantineProvider>
      <QueryClientProvider client={client}>
        <div className="chat-column">
          <ChatComposer session={session} agent={agent} onHeightChange={onHeightChange} />
        </div>
      </QueryClientProvider>
    </MantineProvider>,
  )
  const column = container.querySelector('.chat-column') as HTMLElement
  expect(column).not.toBeNull()
  await waitFor(() => expect(column.style.getPropertyValue('--composer-height')).toBeTruthy())
  expect(onHeightChange).toHaveBeenCalled()
  client.clear()
})
