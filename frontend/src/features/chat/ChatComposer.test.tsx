import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import type { Agent, Session } from '../../domain/types'
import { useAstrorderStore } from '../../state/store'
import { ChatComposer } from './ChatComposer'

const session: Session = { id: 'native-one', agent_id: 'hermes', title: 'one', workspace: null, status: 'idle', updated_at: '2026-01-01T00:00:00Z' }
const agent: Agent = { id: 'hermes', name: 'Hermes', kind: 'hermes', status: 'ready', capabilities: ['chat', 'stop', 'attachments'], limitation: null }
afterEach(() => { cleanup(); vi.restoreAllMocks(); useAstrorderStore.getState().resetRuntime() })
it('embeds the model picker and swaps the primary send button for native stop until completion', async () => {
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
  await waitFor(() => expect(screen.getByRole('button', { name: '停止' })).toBeEnabled())
  fireEvent.click(screen.getByRole('button', { name: '停止' }))
  await waitFor(() => expect(create).toHaveBeenCalledTimes(2))
  // Native stop requires target_id == session_id (the session to interrupt),
  // not a command id — see service._capability_error.
  expect(create.mock.calls[1][0]).toMatchObject({ action: 'stop', text: '', target_id: session.id, session_id: session.id })
  useAstrorderStore.getState().resetRuntime()
  view.rerender(wrap({ ...session, status: 'running' }))
  expect(screen.getByRole('button', { name: '停止' })).toBeInTheDocument()
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
