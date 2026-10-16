import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import type { Agent, Session } from '../../domain/types'
import { useAstrorderStore } from '../../state/store'
import { ChatComposer, interpretGatewayError } from './ChatComposer'
import { draftStorage } from './draftStorage'

vi.mock('../mobile/serverOutbox', async importOriginal => {
  const actual = await importOriginal<typeof import('../mobile/serverOutbox')>()
  return { ...actual, sharedServerOutbox: (storage: import('../mobile/mobileOutbox').OutboxStorage) => new actual.ServerOutbox(storage) }
})
const queueMemory = vi.hoisted(() => ({ rows: [] as never[] }))
vi.mock('../mobile/mobileOutboxStorage', () => ({
  mobileOutboxStorage: {
    load: async () => queueMemory.rows,
    save: async (rows: never[]) => { queueMemory.rows = rows },
  },
}))

const session: Session = { id: 'native-one', agent_id: 'hermes', title: 'one', workspace: null, status: 'idle', updated_at: '2026-01-01T00:00:00Z' }
const agent: Agent = { id: 'hermes', name: 'Hermes', kind: 'hermes', status: 'ready', capabilities: ['chat', 'stop', 'attachments'], limitation: null }
const savedDrafts = new Map<string, any>()
beforeEach(() => {
  vi.spyOn(api, 'sendQueuedCommand').mockImplementation(async (id, aid, sid) => {
    const command = Object.values(useAstrorderStore.getState().commands).find(c => c.id === id && c.agent_id === aid && c.session_id === sid)!
    return { ...command, state: 'accepted' }
  })
  vi.spyOn(api, 'deleteQueuedCommand').mockImplementation(async (id, aid, sid) => {
    const command = Object.values(useAstrorderStore.getState().commands).find(c => c.id === id && c.agent_id === aid && c.session_id === sid)!
    return { ...command, state: 'cancelled' }
  })
  savedDrafts.clear()
  vi.spyOn(draftStorage, 'load').mockImplementation(async key => savedDrafts.get(key))
  vi.spyOn(draftStorage, 'save').mockImplementation(async (key, draft) => { savedDrafts.set(key, draft) })
  vi.spyOn(draftStorage, 'remove').mockImplementation(async key => { savedDrafts.delete(key) })
})
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
it('records directly from the microphone and appends transcription without a dialog or sending', async () => {
  const track = { stop: vi.fn() }
  vi.stubGlobal('navigator', Object.create(navigator, { mediaDevices: { value: { getUserMedia: vi.fn().mockResolvedValue({ getTracks: () => [track] }) } } }))
  class Recorder {
    state = 'inactive'
    mimeType = 'audio/webm'
    ondataavailable: ((e: { data: Blob }) => void) | null = null
    onstop: (() => void) | null = null
    start() { this.state = 'recording' }
    stop() { this.state = 'inactive'; this.ondataavailable?.({ data: new Blob(['audio']) }); this.onstop?.() }
  }
  vi.stubGlobal('MediaRecorder', Recorder)
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'native-model', provider: 'provider' })
  const transcribe = vi.spyOn(api, 'transcribeAudio').mockResolvedValue({ text: '语音输入的内容' })
  const send = vi.spyOn(api, 'createCommand')
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<MantineProvider><QueryClientProvider client={client}><ChatComposer session={session} agent={agent} /></QueryClientProvider></MantineProvider>)
  fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '原有草稿' } })
  fireEvent.click(screen.getByRole('button', { name: '语音输入' }))
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  fireEvent.click(await screen.findByRole('button', { name: '停止录音' }))
  await waitFor(() => expect(screen.getByLabelText('消息内容')).toHaveValue('原有草稿\n语音输入的内容'))
  expect(transcribe).toHaveBeenCalledOnce()
  expect(send).not.toHaveBeenCalled()
  expect(track.stop).toHaveBeenCalled()
  vi.unstubAllGlobals()
  client.clear()
})

it('uses the same uncommitted-review request as the review relay', async () => {
  const codexSession = { ...session, agent_id: 'codex', workspace: '/repo' }
  const codexAgent = { ...agent, id: 'codex', kind: 'codex' as const }
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'native-model', provider: 'provider' })
  vi.spyOn(api, 'getAgentCommands').mockResolvedValue({ items: [] })
  const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: 'accepted', attachments: [], created_at: session.updated_at, error: null, target_id: input.target_id ?? null }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<MantineProvider><QueryClientProvider client={client}><ChatComposer session={codexSession} agent={codexAgent} /></QueryClientProvider></MantineProvider>)
  fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '/review' } })
  fireEvent.click(await screen.findByText('审查未提交的更改'))
  await waitFor(() => expect(create).toHaveBeenCalledOnce())
  expect(create.mock.calls[0][0]).toMatchObject({ action: 'send', text: '请检查我未提交的更改', agent_id: 'codex', session_id: session.id })
  client.clear()
})

it('keeps goal creation in the native Agent command menu without a persistent goal button', async () => {
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'native-model', provider: 'provider' })
  vi.spyOn(api, 'getAgentCommands').mockResolvedValue({ items: [{ name: 'goal', description: '设置原生目标', input_hint: '目标内容' }] })
  const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: 'accepted', attachments: [], created_at: session.updated_at, error: null, target_id: input.target_id ?? null }))
  const setGoal = vi.spyOn(api, 'setSessionGoal')
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<MantineProvider><QueryClientProvider client={client}><ChatComposer session={session} agent={agent} /></QueryClientProvider></MantineProvider>)
  await screen.findByRole('button', { name: '选择会话模型' })
  expect(screen.queryByRole('button', { name: '设置目标' })).not.toBeInTheDocument()
  fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '/go' } })
  fireEvent.click(await screen.findByRole('option', { name: /\/goal/ }))
  expect(screen.getByLabelText('消息内容')).toHaveValue('/goal ')
  fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '/goal 完成迁移' } })
  fireEvent.click(screen.getByRole('button', { name: '发送' }))
  await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({ action: 'send', text: '/goal 完成迁移', agent_id: session.agent_id, session_id: session.id })))
  expect(setGoal).not.toHaveBeenCalled()
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

it('persists text and arbitrary file attachments and restores them after remount', async () => {
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'native-model', provider: 'provider' })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrap = () => <MantineProvider><QueryClientProvider client={client}><ChatComposer session={session} agent={agent} /></QueryClientProvider></MantineProvider>
  const view = render(wrap())
  await waitFor(() => expect(draftStorage.load).toHaveBeenCalled())

  fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '重启后继续' } })
  const input = view.container.querySelector('input[type="file"]') as HTMLInputElement
  expect(input).not.toHaveAttribute('accept')
  const file = new File(['archive'], 'project.zip', { type: 'application/zip' })
  fireEvent.change(input, { target: { files: [file] } })
  await waitFor(() => expect(savedDrafts.get('hermes::native-one')).toMatchObject({ text: '重启后继续' }))
  expect(savedDrafts.get('hermes::native-one').attachments[0].file.name).toBe('project.zip')

  window.dispatchEvent(new Event('beforeunload'))
  expect(draftStorage.save).toHaveBeenCalled()
  view.unmount()
  useAstrorderStore.getState().resetRuntime()
  render(wrap())

  expect(await screen.findByDisplayValue('重启后继续')).toBeInTheDocument()
  expect(await screen.findByText('project.zip')).toBeInTheDocument()
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
  useAstrorderStore.getState().setConnection('connected')
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'native-model', provider: 'provider' })
  const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: 'queued', attachments: [], created_at: session.updated_at, error: null, target_id: input.target_id ?? null }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<MantineProvider><QueryClientProvider client={client}><ChatComposer session={{ ...session, status: 'running' }} agent={agent} /></QueryClientProvider></MantineProvider>)

  fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '先排队' } })
  fireEvent.click(screen.getByRole('button', { name: '加入队列' }))
  expect(await screen.findByText('先排队')).toBeInTheDocument()
  expect(create).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ action: 'enqueue', text: '先排队' }))
  expect(api.sendQueuedCommand).not.toHaveBeenCalled()

  fireEvent.click(screen.getByRole('button', { name: '立即引导' }))
  await waitFor(() => expect(api.sendQueuedCommand).toHaveBeenCalledOnce())
  expect(create).toHaveBeenCalledOnce()
  await waitFor(() => expect(screen.queryByText('先排队')).not.toBeInTheDocument())
  client.clear()
})

it('retries a confirmed failed queued message with a new command ID', async () => {
  useAstrorderStore.getState().setConnection('connected')
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'native-model', provider: 'provider' })
  const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: 'queued', attachments: [], created_at: session.updated_at, error: null }))
  vi.mocked(api.sendQueuedCommand).mockImplementation(async (id, aid, sid) => ({ ...Object.values(useAstrorderStore.getState().commands).find(c => c.id === id && c.agent_id === aid && c.session_id === sid)!, state: vi.mocked(api.sendQueuedCommand).mock.calls.length === 1 ? 'failed' : 'completed', error: vi.mocked(api.sendQueuedCommand).mock.calls.length === 1 ? '原生确认未发送' : null }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<MantineProvider><QueryClientProvider client={client}><ChatComposer session={{ ...session, status: 'running' }} agent={agent} /></QueryClientProvider></MantineProvider>)
  fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '引导失败后重试' } })
  fireEvent.click(screen.getByRole('button', { name: '加入队列' }))
  await waitFor(() => expect(screen.getByRole('button', { name: '立即引导' })).toBeEnabled())
  fireEvent.click(screen.getByRole('button', { name: '立即引导' }))
  fireEvent.click(await screen.findByRole('button', { name: '重试' }))
  await waitFor(() => expect(create).toHaveBeenCalledTimes(2))
  expect(create.mock.calls[1][0].id).not.toBe(create.mock.calls[0][0].id)
  expect(create.mock.calls[1][0]).toMatchObject({ agent_id: session.agent_id, session_id: session.id, text: '引导失败后重试' })
  await waitFor(() => expect(screen.queryByLabelText('排队消息')).not.toBeInTheDocument())
  client.clear()
})

it('retries a rejected attachment guidance as a new message after the turn completes', async () => {
  useAstrorderStore.getState().setConnection('connected')
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'native-model', provider: 'provider' })
  const attachments = Array.from({length: 4}, (_, i) => ({id: `blob-${i}`, name: `photo-${i}.png`, media_type: 'image/png', url: `/blob-${i}`}))
  useAstrorderStore.getState().mergeCommands([{ id: 'failed-guidance', agent_id: session.agent_id, session_id: session.id, action: 'enqueue', state: 'failed', text: '重新发送附件', attachments, created_at: session.updated_at, target_id: null, error: 'attachments must be queued for the next Hermes turn' }])
  const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: 'queued', attachments, created_at: session.updated_at, error: null }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<MantineProvider><QueryClientProvider client={client}><ChatComposer session={session} agent={agent} /></QueryClientProvider></MantineProvider>)
  fireEvent.click(await screen.findByRole('button', { name: '重试' }))
  await waitFor(() => expect(api.sendQueuedCommand).toHaveBeenCalledOnce())
  expect(create).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ action: 'enqueue', text: '重新发送附件', attachment_ids: attachments.map(a => a.id) }))
  expect(create.mock.calls[0][0].id).not.toBe('failed-guidance')
  expect(api.deleteQueuedCommand).toHaveBeenCalledWith('failed-guidance', session.agent_id, session.id)
  await waitFor(() => expect(screen.queryByLabelText('排队消息')).not.toBeInTheDocument())
  client.clear()
})

it.each(['编辑', '删除'])('shows queue operation failure for %s and retains the message', async label => {
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'native-model', provider: 'provider' })
  vi.mocked(api.deleteQueuedCommand).mockRejectedValue(new Error('Command is no longer queued'))
  useAstrorderStore.getState().mergeCommands([{ id: 'uncertain', agent_id: session.agent_id, session_id: session.id, action: 'enqueue', state: 'unknown', text: '保留这条消息', attachments: [], created_at: session.updated_at, target_id: null, error: null }])
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<MantineProvider><QueryClientProvider client={client}><ChatComposer session={session} agent={agent} /></QueryClientProvider></MantineProvider>)
  fireEvent.click(await screen.findByRole('button', { name: label }))
  expect(await screen.findByText('Command is no longer queued')).toBeInTheDocument()
  expect(screen.getByLabelText('排队消息')).toHaveTextContent('保留这条消息')
  client.clear()
})

it('restores all uploaded attachments when editing a rejected guidance after completion', async () => {
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'native-model', provider: 'provider' })
  const attachments = Array.from({length: 4}, (_, i) => ({id: `blob-${i}`, name: `photo-${i}.png`, media_type: 'image/png', url: `/blob-${i}`}))
  useAstrorderStore.getState().mergeCommands([{ id: 'rejected', agent_id: session.agent_id, session_id: session.id, action: 'enqueue', state: 'failed', text: '带附件重新发送', attachments, created_at: session.updated_at, target_id: null, error: 'attachments must be queued for the next Hermes turn' }])
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<MantineProvider><QueryClientProvider client={client}><ChatComposer session={session} agent={agent} /></QueryClientProvider></MantineProvider>)
  await waitFor(() => expect(draftStorage.load).toHaveBeenCalled())
  fireEvent.click(await screen.findByRole('button', { name: '编辑' }))
  await waitFor(() => expect(screen.queryByLabelText('排队消息')).not.toBeInTheDocument())
  expect(screen.getByLabelText('消息内容')).toHaveValue('带附件重新发送')
  attachments.forEach(item => expect(screen.getByText(item.name)).toBeInTheDocument())
  client.clear()
})

it('checks an uncertain queued result without resubmitting or assigning a new ID', async () => {
  useAstrorderStore.getState().setConnection('connected')
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'native-model', provider: 'provider' })
  const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: 'queued', attachments: [], created_at: session.updated_at, error: null }))
  vi.mocked(api.sendQueuedCommand).mockImplementation(async (id, aid, sid) => ({ ...Object.values(useAstrorderStore.getState().commands).find(c => c.id === id && c.agent_id === aid && c.session_id === sid)!, state: 'unknown', error: '回执丢失' }))
  const lookup = vi.spyOn(api, 'getCommands').mockResolvedValue({ items: [] })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<MantineProvider><QueryClientProvider client={client}><ChatComposer session={{ ...session, status: 'running' }} agent={agent} /></QueryClientProvider></MantineProvider>)
  fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '只核对这条引导' } })
  fireEvent.click(screen.getByRole('button', { name: '加入队列' }))
  await waitFor(() => expect(screen.getByRole('button', { name: '立即引导' })).toBeEnabled())
  fireEvent.click(screen.getByRole('button', { name: '立即引导' }))
  expect(await screen.findByText('回执丢失')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '重试' })).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '核对结果' }))
  await waitFor(() => expect(lookup).toHaveBeenCalledWith(session.id, session.agent_id))
  expect(create).toHaveBeenCalledOnce()
  expect(screen.getByLabelText('排队消息')).toHaveTextContent('只核对这条引导')
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

it('shows native goals from any agent through progress.type, including Hermes turn progress', async () => {
  useAstrorderStore.getState().resetRuntime()
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'native-model', provider: 'provider' })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const goal = {
    id: 'hermes:goal:current', session_id: session.id, agent_id: session.agent_id, kind: 'todo' as const, title: '目标: 完成迁移',
    status: 'running' as const, progress: { type: 'goal', source: 'hermes', native_status: 'active', turns_used: 0, max_turns: 20 },
    command: null, logs: [], target_id: null, created_at: session.updated_at, updated_at: session.updated_at,
  }
  const wrap = (tasks: typeof goal[]) => <MantineProvider><QueryClientProvider client={client}><ChatComposer session={session} agent={agent} tasks={tasks} /></QueryClientProvider></MantineProvider>
  const view = render(wrap([goal]))
  expect(screen.getByRole('status', { name: '目标进行中 · 第 0/20 轮：完成迁移' })).toBeInTheDocument()
  view.rerender(wrap([{ ...goal, status: 'cancelled' as never }]))
  expect(screen.queryByText('完成迁移')).not.toBeInTheDocument()
  client.clear()
})

it('interprets quota exceeded errors with recommended model', () => {
  const parsed = interpretGatewayError('openai.APIConnectionError: 429: quota_exceeded')
  expect(parsed).not.toBeNull()
  expect(parsed?.type).toBe('quota')
  expect(parsed?.title).toBe('网关模型配额已耗尽')
  expect(parsed?.recommendedModel?.model).toBe('gemini-3.8-flash')
})

it('interprets server disconnection gracefully', () => {
  const parsed = interpretGatewayError('httpx.RemoteProtocolError: Server disconnected without sending a response.')
  expect(parsed).not.toBeNull()
  expect(parsed?.type).toBe('disconnect')
  expect(parsed?.title).toBe('网关服务断开或连接超时')
})


it('calibrates Grok capabilities: hides image paste hint, rejects image attachments, and allows text files', async () => {
  const grokAgent: Agent = {
    id: 'grok-1',
    kind: 'grok',
    name: 'Grok',
    status: 'ready',
    capabilities: ['history', 'queue', 'approvals', 'task_events', 'attachments'],
    limitation: null,
  }
  const grokSession: Session = {
    ...session,
    agent_id: 'grok-1',
  }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <MantineProvider>
      <QueryClientProvider client={client}>
        <ChatComposer session={grokSession} agent={grokAgent} />
      </QueryClientProvider>
    </MantineProvider>
  )

  const textarea = screen.getByLabelText('消息内容')
  expect(textarea).toHaveAttribute('placeholder', '随心输入')

  const fileInput = document.querySelector('input[type="file"]') as HTMLInputElement
  expect(fileInput).toHaveAttribute('accept')

  const imageFile = new File(['fake-png-content'], 'test.png', { type: 'image/png' })
  fireEvent.change(fileInput, { target: { files: [imageFile] } })

  await waitFor(() => {
    expect(screen.getByRole('alert')).toHaveTextContent('当前智能体不支持图片附件')
  })
  expect(screen.queryByLabelText('待发送附件')).not.toBeInTheDocument()

  const textFile = new File(['hello text'], 'test.txt', { type: 'text/plain' })
  fireEvent.change(fileInput, { target: { files: [textFile] } })

  await waitFor(() => {
    expect(screen.getByLabelText('待发送附件')).toBeInTheDocument()
    expect(screen.getByText('test.txt')).toBeInTheDocument()
  })

  client.clear()
})