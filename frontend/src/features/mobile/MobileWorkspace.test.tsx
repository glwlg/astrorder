import { MantineProvider } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { selectMessages, useAstrorderStore } from '../../state/store'
import { MobileWorkspace, cleanServerName, displayShortModel } from './MobileWorkspace'
import type { OutboxEntry } from './mobileOutbox'
import { mobileOutboxStorage, directOutboxStorage } from './mobileOutboxStorage'
import { scopeKey } from '../../domain/semantics'
import { draftStorage } from '../chat/draftStorage'
import * as localVoiceStream from '../chat/localVoiceStream'

vi.mock('./serverOutbox', async importOriginal => {
  const actual = await importOriginal<typeof import('./serverOutbox')>()
  return { ...actual, sharedServerOutbox: (storage: import('./mobileOutbox').OutboxStorage) => new actual.ServerOutbox(storage) }
})
const memory = vi.hoisted(() => ({ rows: [] as OutboxEntry[], directRows: [] as OutboxEntry[] }))
vi.mock('./mobileOutboxStorage', () => ({
  mobileOutboxStorage: { load: async () => memory.rows, save: async (rows: OutboxEntry[]) => { memory.rows = rows } },
  directOutboxStorage: { load: async () => memory.directRows, save: async (rows: OutboxEntry[]) => { memory.directRows = rows } },
}))
vi.mock('../../hooks/useAstrorderData', () => ({ useSessionResources: () => ({ visibleMessages: selectMessages(useAstrorderStore.getState(), 'inert', 'native-test'), messages: { fetchNextPage: vi.fn(), hasNextPage: false }, commands: { isSuccess: true }, tasks: { isSuccess: true } }) }))
const session = { id: 'native-test', agent_id: 'inert', title: '隔离移动测试', workspace: null, status: 'idle' as const, updated_at: '2026-09-08T00:00:00Z' }
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals() })
function mount() {
  return render(<MantineProvider><QueryClientProvider client={new QueryClient()}><MemoryRouter initialEntries={['/mobile/chat/native-test?agent_id=inert']}><MobileWorkspace /></MemoryRouter></QueryClientProvider></MantineProvider>)
}
async function dropQueued(zone: 'send' | 'edit') {
  const item = document.querySelector('.m-queue-item') as HTMLElement
  fireEvent.pointerDown(item, { pointerId: 1, button: 0, clientX: 80, clientY: 400 })
  fireEvent.pointerMove(item, { pointerId: 1, clientX: 80, clientY: zone === 'send' ? 380 : 420 })
  fireEvent.pointerUp(item, { pointerId: 1, button: 0, clientX: 80, clientY: zone === 'send' ? 380 : 420 })
}
beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  memory.rows = []
  memory.directRows = []
  const preferences = { appearance: {}, session_pins: {}, pinned_projects: [], project_order: [] }
  vi.spyOn(api, 'getPreferences').mockResolvedValue(preferences)
  vi.spyOn(api, 'importPreferences').mockImplementation(async values => ({ ...preferences, ...values, appearance: values.appearance as typeof preferences.appearance || {} }))
  vi.spyOn(api, 'updatePreferences').mockImplementation(async values => ({ ...preferences, ...values, appearance: values.appearance as typeof preferences.appearance || {} }))
  vi.spyOn(api, 'getOpenSessions').mockResolvedValue({ known_agent_ids: [], items: [] })
  vi.spyOn(api, 'createCommand').mockImplementation(async payload => ({ ...payload, state: payload.action === 'enqueue' ? 'queued' : 'accepted', attachments: [], created_at: session.updated_at, error: null }))
  vi.spyOn(api, 'sendQueuedCommand').mockImplementation(async (id, aid, sid) => ({ ...Object.values(useAstrorderStore.getState().commands).find(c => c.id === id && c.agent_id === aid && c.session_id === sid)!, state: 'accepted' }))
  vi.spyOn(api, 'deleteQueuedCommand').mockImplementation(async (id, aid, sid) => ({ ...Object.values(useAstrorderStore.getState().commands).find(c => c.id === id && c.agent_id === aid && c.session_id === sid)!, state: 'cancelled' }))
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ provider: 'p', model: 'bound-model', branch: 'feature/native' })
  vi.spyOn(api, 'getConnections').mockResolvedValue({ local: { kind: 'hermes', state: 'connected', available: true, version: null, agent_id: 'inert', profile_name: 'fixture', session_id: 'native-test', detail: '' }, ssh: { items: [], state: 'unconfigured', settings: null, detail: '' } })
  const values = new Map<string, string>()
  vi.stubGlobal('localStorage', { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => values.set(key, value), removeItem: (key: string) => values.delete(key) })
  vi.spyOn(draftStorage, 'load').mockResolvedValue(undefined)
  vi.spyOn(draftStorage, 'save').mockResolvedValue(undefined)
  vi.spyOn(draftStorage, 'remove').mockResolvedValue(undefined)
  useAstrorderStore.getState().resetRuntime()
  useAstrorderStore.getState().hydrateBootstrap({ protocol_version: 1, cursor: 0, agents: [{ id: 'inert', kind: 'hermes', name: '协议测试', status: 'ready', capabilities: ['chat', 'stop', 'attachments'], limitation: null }], sessions: [session] })
})
describe('independent mobile composer', () => {
  it.each(['scheduled', 'handoff'] as const)('distinguishes a %s relationship in the mobile conversation header', async relation => {
    const child = { ...session, id: 'child', title: '协作目标', handoff_from_agent_id: session.agent_id, handoff_from_session_id: session.id,
      parent_agent_id: session.agent_id, parent_session_id: session.id, handoff_context: relation === 'handoff' ? '交接摘要' : null }
    useAstrorderStore.setState(state => ({ sessions: { ...state.sessions, [scopeKey(child.agent_id, child.id)]: child } }))
    mount()
    await screen.findByText('bound-model')
    if (relation === 'scheduled') {
      expect(screen.queryByRole('button', { name: /已转交至|转交自|^来自 / })).not.toBeInTheDocument()
    } else {
      fireEvent.click(await screen.findByRole('button', { name: '已转交至 Hermes' }))
      expect(await screen.findByRole('heading', { level: 1, name: child.title })).toBeInTheDocument()
      fireEvent.click(await screen.findByRole('button', { name: '转交自 Hermes' }))
      expect(await screen.findByRole('heading', { level: 1, name: session.title })).toBeInTheDocument()
    }
  })

  it('locks an externally owned session and preserves its queue until native retry unlocks it', async () => {
    useAstrorderStore.getState().setConnection('connected')
    vi.spyOn(api, 'getSessionModel').mockResolvedValue({ provider: 'p', model: 'bound-model', read_only: true })
    memory.rows = [{ payload: { id: 'held', agent_id: session.agent_id, session_id: session.id, action: 'send', text: '保留待发内容', attachment_ids: [], target_id: null }, files: [], attachments: [], state: 'queued' }]
    const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: 'completed', attachments: [], created_at: session.updated_at, error: null }))
    const sync = vi.spyOn(api, 'syncSession').mockImplementation(async () => {
      vi.mocked(api.getSessionModel).mockResolvedValue({ provider: 'p', model: 'bound-model', read_only: false })
      return { ...session, control_state: 'owned' as const }
    })
    mount()
    expect(await screen.findByText('已在另一个应用中打开')).toBeVisible()
    expect(screen.queryByLabelText('消息内容')).not.toBeInTheDocument()
    expect(create).not.toHaveBeenCalled()
    expect(memory.rows[0]?.payload.text).toBe('保留待发内容')
    fireEvent.click(screen.getByRole('button', { name: '会话操作' }))
    expect(await screen.findByRole('menuitem', { name: '继续' })).toHaveAttribute('data-disabled', 'true')
    fireEvent.keyDown(document, { key: 'Escape' })
    fireEvent.click(screen.getByRole('button', { name: /^重试$/ }))
    await waitFor(() => expect(sync).toHaveBeenCalledWith(session.id, session.agent_id))
    expect(await screen.findByLabelText('消息内容')).toBeEnabled()
    await waitFor(() => expect(create).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ id: 'held', agent_id: session.agent_id, session_id: session.id, text: '保留待发内容' })))
  })
  it('uses the Agent command to create a goal and only then shows mobile goal controls', async () => {
    const save = vi.spyOn(api, 'setSessionGoal').mockImplementation(async (_sid, _aid, objective) => ({ source: 'hermes', goal: { title: objective, status: 'active', turns_used: 0, max_turns: 20 } }))
    vi.spyOn(api, 'getAgentCommands').mockResolvedValue({ items: [{ name: 'goal', description: '设置原生目标', input_hint: '目标内容' }] })
    const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: 'completed', error: null, attachments: [], created_at: session.updated_at }))
    const status = vi.spyOn(api, 'setSessionGoalStatus').mockImplementation(async (_sid, _aid, value) => ({ source: 'hermes', goal: { title: '完成移动端对齐', status: value, turns_used: 0, max_turns: 20 } }))
    const clear = vi.spyOn(api, 'clearSessionGoal').mockResolvedValue({ source: 'hermes', goal: null })
    useAstrorderStore.getState().setConnection('connected')
    mount()
    await screen.findByText('bound-model')
    expect(screen.queryByRole('button', { name: '目标管理' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '设置目标' })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '会话操作' }))
    expect(screen.queryByRole('menuitem', { name: '目标管理' })).not.toBeInTheDocument()
    fireEvent.keyDown(document, { key: 'Escape' })
    fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '/go' } })
    fireEvent.click(await screen.findByRole('option', { name: /\/goal/ }))
    expect(screen.getByLabelText('消息内容')).toHaveValue('/goal ')
    fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '/goal 完成移动端对齐' } })
    fireEvent.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({ text: '/goal 完成移动端对齐', agent_id: session.agent_id, session_id: session.id })))
    expect(save).not.toHaveBeenCalled()
    act(() => useAstrorderStore.getState().mergeTasks([{ id: 'hermes:goal:current', agent_id: session.agent_id, session_id: session.id, kind: 'todo', title: '目标: 完成移动端对齐', status: 'running', progress: { type: 'goal', source: 'hermes', native_status: 'active', turns_used: 0, max_turns: 20 }, command: null, target_id: null, logs: [], created_at: session.updated_at, updated_at: session.updated_at }]))
    fireEvent.click(await screen.findByRole('button', { name: '目标管理' }))
    fireEvent.click(await screen.findByRole('button', { name: '暂停目标' }))
    await waitFor(() => expect(status).toHaveBeenLastCalledWith(session.id, session.agent_id, 'paused'))
    expect(await screen.findByText(/按 Hermes 原生行为重置已用轮次/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '恢复目标' }))
    await waitFor(() => expect(status).toHaveBeenLastCalledWith(session.id, session.agent_id, 'active'))
    fireEvent.click(screen.getByRole('button', { name: '修改目标' }))
    expect(await screen.findByLabelText('目标内容')).toHaveValue('完成移动端对齐')
    fireEvent.change(screen.getByLabelText('目标内容'), { target: { value: '修订目标' } })
    fireEvent.click(screen.getByRole('button', { name: '保存目标' }))
    await waitFor(() => expect(save).toHaveBeenLastCalledWith(session.id, session.agent_id, '修订目标'))
    fireEvent.click(await screen.findByRole('button', { name: '删除目标' }))
    const confirmation = screen.getByRole('dialog', { name: '删除目标？' })
    expect(clear).not.toHaveBeenCalled()
    fireEvent.click(within(confirmation).getByRole('button', { name: '删除目标' }))
    await waitFor(() => expect(clear).toHaveBeenCalledWith(session.id, session.agent_id))
    await waitFor(() => expect(screen.queryByRole('button', { name: '修改目标' })).not.toBeInTheDocument())
    expect(screen.queryByRole('button', { name: '设置目标' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '目标管理' })).not.toBeInTheDocument()
  })
  it('opens completed child details, reported output and exact child navigation', async () => {
    const child = { ...session, id: 'child-native', title: '子会话', status: 'idle' as const }
    useAstrorderStore.setState(state => ({ sessions: { ...state.sessions, [scopeKey(child.agent_id, child.id)]: child } }))
    useAstrorderStore.getState().mergeTasks([{ id: 'finished-child', agent_id: session.agent_id, session_id: session.id, kind: 'subagent', title: '完成的子代理', status: 'completed', progress: { child_session_id: child.id, model: 'native-child-model', input_tokens: 123, summary: '子代理验证结果' }, command: null, logs: [], target_id: child.id, created_at: session.updated_at, updated_at: session.updated_at }])
    mount()
    fireEvent.click(screen.getByRole('button', { name: '任务与子代理' }))
    fireEvent.click(screen.getByRole('button', { name: '展开运行详情' }))
    fireEvent.click(screen.getByRole('button', { name: '完成的子代理' }))
    expect(screen.getByText('模型：native-child-model')).toBeInTheDocument()
    expect(screen.getByText('子代理验证结果')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '停止任务' })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '打开子会话' }))
    await waitFor(() => expect(document.querySelector('.m-session-deck')).toHaveAttribute('data-session-key', scopeKey(child.agent_id, child.id)))
  })
  it('stops the exact child session without stopping its parent', async () => {
    const child = { ...session, id: 'child-running', title: '运行子会话', status: 'running' as const }
    useAstrorderStore.setState(state => ({ sessions: { ...state.sessions, [scopeKey(child.agent_id, child.id)]: child } }))
    useAstrorderStore.getState().mergeTasks([{ id: 'running-child', agent_id: session.agent_id, session_id: session.id, kind: 'subagent', title: '运行的子代理', status: 'running', progress: { child_session_id: child.id }, command: null, logs: [], target_id: child.id, created_at: session.updated_at, updated_at: session.updated_at }])
    const stop = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: 'completed', attachments: [], created_at: session.updated_at, error: null }))
    mount()
    fireEvent.click(screen.getByRole('button', { name: '任务与子代理' }))
    fireEvent.click(screen.getByRole('button', { name: '展开运行详情' }))
    fireEvent.click(within(screen.getByLabelText('运行任务列表')).getByRole('button', { name: '运行的子代理' }))
    fireEvent.click(screen.getByRole('button', { name: '停止任务' }))
    const confirm = screen.getByRole('dialog', { name: '停止任务？' })
    expect(stop).not.toHaveBeenCalled()
    expect(confirm).not.toHaveTextContent('本轮其他活动')
    fireEvent.click(within(confirm).getByRole('button', { name: '停止任务' }))
    await waitFor(() => expect(stop).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ agent_id: child.agent_id, session_id: child.id, target_id: child.id, action: 'stop' })))
  })
  it('sends the final held transcript once after release and preserves the typed draft', async () => {
    useAstrorderStore.getState().setConnection('connected')
    vi.stubGlobal('navigator', Object.create(navigator, { mediaDevices: { value: { getUserMedia: vi.fn().mockResolvedValue({ getTracks: () => [{ stop: vi.fn() }] }) } } }))
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue({ id: 'r2t2-local-voice', name: 'R2T2', enabled: true, state: 'running', available: true, progress: null, error: null, pid: 1, device: 'Vulkan0', model: 'test' })
    let callbacks!: localVoiceStream.LocalVoiceStreamCallbacks
    const controller = { stop: vi.fn(), cancel: vi.fn() }
    vi.spyOn(localVoiceStream, 'startLocalVoiceStream').mockImplementation(async (_media, next) => { callbacks = next; return controller })
    const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: input.action === 'enqueue' ? 'queued' : 'accepted', attachments: [], created_at: session.updated_at, error: null }))
    mount()
    const input = screen.getByLabelText('消息内容')
    fireEvent.change(input, { target: { value: '保留草稿' } })
    fireEvent.pointerDown(input, { pointerId: 1, pointerType: 'touch', clientX: 180, clientY: 600 })
    await waitFor(() => expect(callbacks).toBeDefined())
    act(() => callbacks.onTranscript('还没说完'))
    expect(create).not.toHaveBeenCalled()
    fireEvent.pointerUp(input, { pointerId: 1 })
    expect(controller.stop).toHaveBeenCalledOnce()
    expect(create).not.toHaveBeenCalled()
    act(() => callbacks.onDone('最终语音消息'))
    act(() => callbacks.onDone('重复结束'))
    await waitFor(() => expect(create).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ text: '最终语音消息', action: 'send', session_id: session.id })))
    expect(input).toHaveValue('保留草稿')
  })
  it('shows native context usage and opens detailed runtime facts', async () => {
    const usage = vi.spyOn(api, 'getSessionUsage').mockResolvedValue({ ok: true, session_id: session.id, model: 'bound-model', context_window: 200000, last_input_tokens: 24000, context_available: true, source: 'native_last_request', used_percentage: 12, total_tokens: 32100, input_tokens: 24000, output_tokens: 8100, cached_tokens: 12000, reasoning_tokens: 3000, cache_hit_rate: 50 })
    mount()
    const chip = await screen.findByRole('button', { name: '上下文用量' })
    expect(chip).toHaveTextContent('24.0k / 200k')
    fireEvent.click(chip)
    expect(await screen.findByLabelText('运行信息')).toHaveTextContent('缓存命中率 50%')
    expect(usage).toHaveBeenCalledWith(session.id, session.agent_id)
  })
  it('shows native connection, agent, model and branch in runtime status', async () => {
    mount()
    fireEvent.click(screen.getByRole('button', { name: '会话操作' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '运行状态' }))
    const facts = screen.getByLabelText('运行信息')
    await waitFor(() => expect(facts).toHaveTextContent('feature/native'))
    expect(facts).toHaveTextContent('p/bound-model')
    expect(facts).toHaveTextContent('Hermes')
    expect(facts).toHaveTextContent('本机 · fixture')
    expect(facts).toHaveTextContent('已连接')
  })
  it('opens an anchored message menu, not a sheet, and quotes into the composer', () => {
    useAstrorderStore.getState().mergeMessages('inert', 'native-test', [{ id: 'm', agent_id: 'inert', session_id: 'native-test', role: 'assistant', kind: 'message', text: '引用这条消息', attachments: [], created_at: session.updated_at, command_id: null, tool: null }])
    const view = mount()
    fireEvent.contextMenu(view.container.querySelector('.m-msg')!, { clientX: 140, clientY: 220 })
    expect(screen.getByRole('menu', { name: '消息操作' })).toBeInTheDocument()
    expect(view.container.querySelector('.m-sheet')).toBeNull()
    fireEvent.click(screen.getByRole('menuitem', { name: '引用' }))
    expect(view.container.querySelector('.m-quote')).toHaveTextContent('引用这条消息')
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  })
  it('dismisses message actions with Escape after focus returns to the message', () => {
    useAstrorderStore.getState().mergeMessages('inert', 'native-test', [{ id: 'escape-message', agent_id: 'inert', session_id: 'native-test', role: 'assistant', kind: 'message', text: '关闭消息操作', attachments: [], created_at: session.updated_at, command_id: null, tool: null }])
    const view = mount()
    const message = view.container.querySelector<HTMLElement>('.m-msg')!
    fireEvent.contextMenu(message, { clientX: 140, clientY: 220 })
    message.focus()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByRole('menu', { name: '消息操作' })).not.toBeInTheDocument()
    expect(message).toHaveFocus()
  })

  it('enables native text selection only for the message selected from its menu', async () => {
    useAstrorderStore.getState().mergeMessages('inert', 'native-test', [{ id: 'select-message', agent_id: 'inert', session_id: 'native-test', role: 'assistant', kind: 'message', text: '选择这条消息', attachments: [], created_at: session.updated_at, command_id: null, tool: null }])
    const view = mount()
    const message = view.container.querySelector('.m-msg')!
    fireEvent.contextMenu(message, { clientX: 140, clientY: 220 })
    fireEvent.click(screen.getByRole('menuitem', { name: '选择' }))
    await waitFor(() => expect(message.querySelector('.m-bubble')).toHaveClass('m-select-text'))
    expect(window.getSelection()?.toString()).toBe('选择这条消息')
    expect(fireEvent.contextMenu(message.querySelector('.m-bubble')!)).toBe(true)
    expect(screen.queryByRole('menu', { name: '消息操作' })).not.toBeInTheDocument()
    fireEvent.pointerDown(screen.getByLabelText('消息内容'))
    expect(message.querySelector('.m-bubble')).not.toHaveClass('m-select-text')
  })

  it('reads and displays the native-bound model immediately when opening a session', async () => {
    mount()
    expect(await screen.findByText('bound-model')).toBeVisible()
    expect(screen.getByRole('button', { name: '选择会话模型' })).toHaveAttribute('title', 'p/bound-model')
    expect(api.getSessionModel).toHaveBeenCalledWith('native-test', 'inert')
  })
  it('opens the complete mobile workbench from session actions', async () => {
    mount()
    fireEvent.click(screen.getByRole('button', { name: '会话操作' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '工作区' }))
    const dialog = await screen.findByRole('dialog', { name: '会话工作区' })
    for (const name of ['文件', '终端', '知识记忆', '侧边聊天', '决策状态机', '浏览器']) expect(within(dialog).getByRole('button', { name: new RegExp(`^${name}`) })).toBeVisible()
    fireEvent.click(within(dialog).getByRole('button', { name: '关闭工作区' }))
    expect(screen.queryByRole('dialog', { name: '会话工作区' })).not.toBeInTheDocument()
  })
  it('opens the current session blackboard from the session menu', async () => {
    const blackboard = vi.spyOn(api, 'getBlackboard').mockResolvedValue({ namespace: 'session:inert::native-test', items: {}, count: 0 })
    mount()
    fireEvent.click(screen.getByRole('button', { name: '会话操作' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '黑板' }))
    const dialog = await screen.findByRole('dialog', { name: '会话黑板' })
    expect(dialog).toHaveClass('m-blackboard-dialog')
    expect(document.querySelector('.mobile-workspace')).toHaveAttribute('data-sheet', 'blackboard')
    await waitFor(() => expect(blackboard).toHaveBeenCalledWith('session:inert::native-test'))
    expect(screen.getByText('当前黑板暂无共享参数')).toBeInTheDocument()
  })
  it('dictates from the microphone straight into the mobile input without opening a sheet', async () => {
    const track = { stop: vi.fn() }
    vi.stubGlobal('navigator', Object.create(navigator, { mediaDevices: { value: { getUserMedia: vi.fn().mockResolvedValue({ getTracks: () => [track] }) } } }))
    class Recorder {
      state = 'inactive'; mimeType = 'audio/webm'
      ondataavailable: ((e: { data: Blob }) => void) | null = null
      onstop: (() => void) | null = null
      start() { this.state = 'recording' }
      stop() { this.state = 'inactive'; this.ondataavailable?.({ data: new Blob(['audio']) }); this.onstop?.() }
    }
    vi.stubGlobal('MediaRecorder', Recorder)
    vi.spyOn(api, 'transcribeAudio').mockResolvedValue({ text: '手机听写' })
    const send = vi.spyOn(api, 'createCommand')
    mount()
    const input = screen.getByLabelText('消息内容') as HTMLTextAreaElement
    Object.defineProperty(input, 'scrollHeight', { configurable: true, get: () => input.value ? 108 : 34 })
    fireEvent.click(screen.getByRole('button', { name: '语音消息' }))
    fireEvent.click(await screen.findByRole('button', { name: '停止录音' }))
    await waitFor(() => expect(screen.getByLabelText('消息内容')).toHaveValue('手机听写'))
    expect(input.style.height).toBe('108px')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(send).not.toHaveBeenCalled()
  })
  it('keeps the composer to attach, input, voice and send', () => {
    mount()
    const composer = document.querySelector('.m-composer') as HTMLElement
    expect(within(composer).getByRole('button', { name: '添加附件' })).toBeInTheDocument()
    expect(within(composer).getByRole('button', { name: '语音消息' })).toBeInTheDocument()
    expect(within(composer).getByRole('button', { name: '发送' })).toBeInTheDocument()
    expect(within(composer).queryByRole('button', { name: '选择会话模型' })).not.toBeInTheDocument()
    expect(within(composer).queryByRole('button', { name: /当前审批模式/ })).not.toBeInTheDocument()
    expect(within(composer).queryByRole('button', { name: '会话操作' })).not.toBeInTheDocument()
  })
  it('sends an idle session directly and shows the user message immediately', async () => {
    useAstrorderStore.getState().setConnection('connected')
    const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: 'received', attachments: [], created_at: session.updated_at, error: null }))
    mount()
    const input = screen.getByLabelText('消息内容')
    await screen.findByText('bound-model')
    fireEvent.change(input, { target: { value: '直接发送' } })
    fireEvent.click(screen.getByRole('button', { name: '发送' }))
    expect(input).toHaveValue('')
    expect(selectMessages(useAstrorderStore.getState(), 'inert', 'native-test').some(message => message.text === '直接发送')).toBe(true)
    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({ text: '直接发送', action: 'send' })))
    await waitFor(() => expect(memory.directRows[0]?.state).toBe('received'))
    expect(memory.directRows).toHaveLength(1)
    expect(memory.directRows[0].payload.id).toBe(create.mock.calls[0][0].id)
  })
  it('persists direct image-only delivery before upload and retains an uncertain receipt', async () => {
    useAstrorderStore.getState().setConnection('connected')
    const file = new File(['image contents'], 'direct.png', { type: 'image/png' })
    vi.stubGlobal('URL', class extends URL { static createObjectURL = vi.fn(() => 'blob:inert-direct'); static revokeObjectURL = vi.fn() })
    const uploaded = { id: 'direct-image', name: file.name, media_type: file.type, url: '/api/v1/attachments/direct-image' }
    const upload = vi.spyOn(api, 'uploadAttachment').mockImplementation(async () => {
      expect(memory.directRows).toHaveLength(1)
      expect(memory.directRows[0].files).toEqual([file])
      return uploaded
    })
    const create = vi.spyOn(api, 'createCommand').mockImplementation(async payload => {
      expect(memory.directRows[0]).toMatchObject({ state: 'submitting', files: [], attachments: [uploaded], payload: { id: payload.id, text: '', attachment_ids: [uploaded.id] } })
      throw new TypeError('Connection lost after command write')
    })
    const view = mount()
    await screen.findByText('bound-model')
    fireEvent.change(view.container.querySelector('input[type=file]')!, { target: { files: [file] } })
    fireEvent.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(memory.directRows[0]?.state).toBe('unknown'))
    expect(upload).toHaveBeenCalledOnce()
    expect(create).toHaveBeenCalledOnce()
    expect(memory.directRows[0].payload.attachment_ids).toEqual([uploaded.id])
    const optimistic = selectMessages(useAstrorderStore.getState(), session.agent_id, session.id).find(message => message.command_id === memory.directRows[0].payload.id)
    expect(optimistic?.attachments).toEqual([uploaded])
    expect(await screen.findByRole('button', { name: '核对发送结果' })).toBeVisible()
    expect(screen.queryByRole('button', { name: '重新发送' })).not.toBeInTheDocument()
  })

  it('sends on pointer down so dismissing the keyboard does not eat the tap', async () => {
    useAstrorderStore.getState().setConnection('connected')
    const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: input.action === 'enqueue' ? 'queued' : 'accepted', attachments: [], created_at: session.updated_at, error: null }))
    mount()
    await screen.findByText('bound-model')
    fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '按下即发' } })
    fireEvent.pointerDown(screen.getByRole('button', { name: '发送' }), { button: 0 })
    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({ text: '按下即发', action: 'send' })))
  })
  it('keeps sending available while skill evolution runs in the background', async () => {
    useAstrorderStore.getState().setConnection('connected')
    useAstrorderStore.getState().mergeTasks([{ id: 'skill-task', session_id: session.id, agent_id: session.agent_id, kind: 'background', title: '工具：skill_manage', status: 'running', progress: { blocking: false }, command: null, logs: [], target_id: 'call', created_at: session.updated_at, updated_at: session.updated_at }])
    const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: input.action === 'enqueue' ? 'queued' : 'accepted', attachments: [], created_at: session.updated_at, error: null }))
    mount()
    await screen.findByText('bound-model')
    fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '继续提问' } })
    fireEvent.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({ text: '继续提问', action: 'send' })))
  })
  it('queues while the agent is running and can send that entry as guidance', async () => {
    useAstrorderStore.getState().setConnection('connected')
    useAstrorderStore.getState().hydrateBootstrap({ protocol_version: 1, cursor: 1, agents: [{ id: 'inert', kind: 'hermes', name: '协议测试', status: 'ready', capabilities: ['chat', 'stop', 'attachments'], limitation: null }], sessions: [{ ...session, status: 'running' }] })
    const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: input.action === 'enqueue' ? 'queued' : 'accepted', attachments: [], created_at: session.updated_at, error: null }))
    mount()
    fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '排队消息' } })
    fireEvent.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(create).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({text:'排队消息', action:'enqueue'})))
    expect(api.sendQueuedCommand).not.toHaveBeenCalled()
    expect(selectMessages(useAstrorderStore.getState(), 'inert', 'native-test')).toEqual([])
    await dropQueued('send')
    await waitFor(() => expect(api.sendQueuedCommand).toHaveBeenCalledOnce())
  })
  it('migrates its offline queue without dispatching from the browser', async () => {
    memory.rows = [{ payload: { id: 'offline-reload', agent_id: session.agent_id, session_id: session.id, action: 'send', text: '离线恢复', attachment_ids: [], target_id: null }, files: [], attachments: [], state: 'queued' }]
    const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: 'completed', attachments: [], created_at: session.updated_at, error: null }))
    const send = vi.spyOn(api, 'sendQueuedCommand')
    mount()
    await screen.findByLabelText('排队消息')
    await screen.findByText('bound-model')
    expect(create).not.toHaveBeenCalled()
    act(() => useAstrorderStore.getState().setConnection('connected'))
    await waitFor(() => expect(memory.rows).toEqual([]))
    expect(create).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ id: 'offline-reload', agent_id: session.agent_id, session_id: session.id }))
    expect(create.mock.calls[0][0].action).toBe('enqueue')
    expect(send).not.toHaveBeenCalled()
  })

  it('offers explicit guidance and edit controls for queued messages without requiring a drag', async () => {
    useAstrorderStore.getState().setConnection('connected')
    useAstrorderStore.setState(state => ({ sessions: { ...state.sessions, [scopeKey(session.agent_id, session.id)]: { ...session, status: 'running' } } }))
    memory.rows = [{ payload: { id: 'guide', agent_id: session.agent_id, session_id: session.id, action: 'send', text: '立即修正方向', attachment_ids: [], target_id: null }, files: [], attachments: [], state: 'queued' }]
    const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: input.action === 'enqueue' ? 'queued' : 'accepted', attachments: [], created_at: session.updated_at, error: null }))
    mount()
    const row = await screen.findByLabelText('排队消息')
    await screen.findByText('bound-model')
    expect(within(row).getByRole('button', { name: '编辑待发消息' })).toBeEnabled()
    fireEvent.click(within(row).getByRole('button', { name: '立即引导' }))
    await waitFor(() => expect(create).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ id: 'guide', agent_id: session.agent_id, session_id: session.id, action: 'enqueue', text: '立即修正方向' })))
    await waitFor(() => expect(api.sendQueuedCommand).toHaveBeenCalledExactlyOnceWith('guide', session.agent_id, session.id))
  })
  it('keeps a queued message when the browser cancels its touch gesture', async () => {
    useAstrorderStore.getState().setConnection('connected')
    useAstrorderStore.setState(state => ({ sessions: { ...state.sessions, [scopeKey(session.agent_id, session.id)]: { ...session, status: 'running' } } }))
    memory.rows = [{ payload: { id: 'cancel-touch', agent_id: session.agent_id, session_id: session.id, action: 'send', text: '滚动时保留这条消息', attachment_ids: [], target_id: null }, files: [], attachments: [], state: 'queued' }]
    const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: input.action === 'enqueue' ? 'queued' : 'accepted', attachments: [], created_at: session.updated_at, error: null }))
    mount()
    const row = await screen.findByLabelText('排队消息')
    await screen.findByText('bound-model')
    fireEvent.pointerDown(row, { pointerId: 1, button: 0, clientX: 80, clientY: 400 })
    fireEvent.pointerMove(row, { pointerId: 1, clientX: 80, clientY: 380 })
    await act(async () => { fireEvent.pointerCancel(row, { pointerId: 1, clientX: 80, clientY: 380 }) })
    expect(create).not.toHaveBeenCalled()
    expect(memory.rows).toHaveLength(1)
    expect(memory.rows[0].state).toBe('queued')
    expect(screen.getByLabelText('消息内容')).toHaveValue('')
    expect(document.querySelector('.m-queue-ghost')).toBeNull()
  })
  it('retries only the selected failed entry when another message precedes it', async () => {
    useAstrorderStore.getState().setConnection('connected')
    useAstrorderStore.setState(state => ({ sessions: { ...state.sessions, [scopeKey(session.agent_id, session.id)]: { ...session, status: 'running' } } }))
    memory.rows = [
      { payload: { id: 'leave-queued', agent_id: session.agent_id, session_id: session.id, action: 'send', text: '稍后发送', attachment_ids: [], target_id: null }, files: [], attachments: [], state: 'queued' },
      { payload: { id: 'failed-selected', agent_id: session.agent_id, session_id: session.id, action: 'send', text: '仅重发这条', attachment_ids: [], target_id: null }, files: [], attachments: [], state: 'failed' },
    ]
    const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: input.action === 'enqueue' ? 'queued' : 'accepted', attachments: [], created_at: session.updated_at, error: null }))
    mount()
    await screen.findByText('bound-model')
    fireEvent.click(await screen.findByRole('button', { name: '重新发送' }))
    await waitFor(() => expect(create).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ text: '仅重发这条', action: 'enqueue', agent_id: session.agent_id, session_id: session.id })))
    await waitFor(() => expect(api.sendQueuedCommand).toHaveBeenCalledOnce())
    expect(create.mock.calls[0][0].id).not.toBe('failed-selected')
    expect(Object.values(useAstrorderStore.getState().commands).find(row => row.id === 'leave-queued')?.state).toBe('queued')
  })
  it('keeps uploaded attachments when editing and submitting a failed message', async () => {
    useAstrorderStore.getState().setConnection('connected')
    const uploaded = { id: 'already-uploaded', name: 'saved.txt', media_type: 'text/plain', url: '/api/v1/attachments/already-uploaded' }
    memory.rows = [{ payload: { id: 'failed-file', agent_id: session.agent_id, session_id: session.id, action: 'send', text: '附件原文', attachment_ids: [uploaded.id], target_id: null }, files: [], attachments: [uploaded], state: 'failed' }]
    const upload = vi.spyOn(api, 'uploadAttachment').mockResolvedValue(uploaded)
    const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: 'completed', attachments: [uploaded], created_at: session.updated_at, error: null }))
    mount()
    await screen.findByText('bound-model')
    fireEvent.click(await screen.findByRole('button', { name: '编辑待发消息' }))
    await waitFor(() => expect(screen.getByLabelText('消息内容')).toHaveValue('附件原文'))
    expect(screen.getByRole('button', { name: '移除 saved.txt' })).toBeVisible()
    fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '附件修订' } })
    fireEvent.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(create).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ text: '附件修订', attachment_ids: ['already-uploaded'] })))
    expect(upload).not.toHaveBeenCalled()
  })
  it('edits a queued message back into the composer', async () => {
    useAstrorderStore.getState().setConnection('connected')
    useAstrorderStore.getState().hydrateBootstrap({ protocol_version: 1, cursor: 1, agents: [{ id: 'inert', kind: 'hermes', name: '协议测试', status: 'ready', capabilities: ['chat', 'stop', 'attachments'], limitation: null }], sessions: [{ ...session, status: 'running' }] })
    mount()
    fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '待编辑消息' } })
    fireEvent.click(screen.getByRole('button', { name: '发送' }))
    await screen.findByText('待编辑消息')
    await dropQueued('edit')
    await waitFor(() => expect(screen.getByLabelText('消息内容')).toHaveValue('待编辑消息'))
    expect(memory.rows).toEqual([])
  })
  it('prevents changing model, reasoning and approval mode for a read-only session', async () => {
    vi.spyOn(api, 'getSessionModel').mockResolvedValue({ provider: 'p', model: 'bound-model', read_only: true })
    vi.spyOn(api, 'getSessionModels').mockResolvedValue({ items: [{ provider: 'p', model: 'next-model', label: '下个模型' }] })
    const change = vi.spyOn(api, 'setSessionModel')
    const effort = vi.spyOn(api, 'setSessionReasoning')
    const approval = vi.spyOn(api, 'setSessionApprovalMode')
    mount()
    await screen.findByText('已在另一个应用中打开')
    fireEvent.click(screen.getByRole('button', { name: '选择会话模型' }))
    expect(await screen.findByRole('button', { name: '下个模型' })).toBeDisabled()
    for (const button of screen.getAllByRole('button', { name: /^思考强度 / })) expect(button).toBeDisabled()
    for (const button of document.querySelectorAll('.approval-mode-option')) expect(button).toBeDisabled()
    expect(change).not.toHaveBeenCalled()
    expect(effort).not.toHaveBeenCalled()
    expect(approval).not.toHaveBeenCalled()
  })
  it('confirms model selection inside the mobile sheet, without a native browser confirm', async () => {
    vi.spyOn(api, 'getSessionModels').mockResolvedValue({ items: [{ provider: 'p', model: 'm', label: 'Provider · m' }] })
    const change = vi.spyOn(api, 'setSessionModel').mockResolvedValue({ provider: 'p', model: 'm' })
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    vi.spyOn(notifications, 'show')
    mount()
    fireEvent.click(screen.getByRole('button', { name: '选择会话模型' }))
    expect(await screen.findByText('应如何批准操作？')).toBeInTheDocument()
    fireEvent.click(await screen.findByRole('button', { name: 'Provider · m' }))
    fireEvent.click(screen.getByRole('button', { name: '确认切换模型' }))
    await waitFor(() => expect(change).toHaveBeenCalledWith('native-test', 'inert', 'p', 'm'))
    expect(confirm).not.toHaveBeenCalled()
    await waitFor(() => {
      const btn = screen.getByRole('button', { name: '选择会话模型' })
      expect(btn).toHaveTextContent('m')
      expect(btn).toHaveAttribute('title', 'p/m')
    })
    expect(notifications.show).not.toHaveBeenCalled()
  })
  it('truncates model names to the last segment and cleans agent suffixes from server names', () => {
    expect(displayShortModel('opencodex/google-antigravity/gemini-3.8-flash')).toBe('gemini-3.8-flash')
    expect(displayShortModel('openai/gpt-4o · 下轮生效')).toBe('gpt-4o · 下轮生效')
    expect(displayShortModel('读取模型…')).toBe('读取模型…')

    expect(cleanServerName({ id: 'local', name: '本机 Codex', kind: 'codex', status: 'ready', capabilities: [], limitation: null })).toBe('本机')
    expect(cleanServerName({ id: 'remote', name: 'WSL · Codex', connection_id: 'wsl', kind: 'codex', status: 'ready', capabilities: [], limitation: null })).toBe('WSL')
    expect(cleanServerName({ id: 'remote-hermes', name: 'WSL Hermes', connection_id: 'wsl', kind: 'hermes', status: 'ready', capabilities: [], limitation: null })).toBe('WSL')
  })
  it('confirms mobile thinking effort without a success toast, and toasts only on failure', async () => {
    vi.spyOn(api, 'getSessionModel').mockResolvedValue({ provider: 'p', model: 'bound-model', effort: 'medium' })
    vi.spyOn(api, 'getSessionModels').mockResolvedValue({ items: [] })
    const changeEffort = vi.spyOn(api, 'setSessionReasoning').mockResolvedValue({ effort: 'high' })
    vi.spyOn(notifications, 'show')
    mount()
    fireEvent.click(screen.getByRole('button', { name: '选择会话模型' }))
    fireEvent.click(await screen.findByRole('button', { name: '思考强度 高' }))
    await waitFor(() => expect(changeEffort).toHaveBeenCalledWith('native-test', 'inert', 'high'))
    expect(notifications.show).not.toHaveBeenCalled()

    changeEffort.mockRejectedValueOnce(new Error('思考强度未确认'))
    fireEvent.click(screen.getByRole('button', { name: '思考强度 低' }))
    await waitFor(() => expect(notifications.show).toHaveBeenCalled())
    expect(notifications.show).toHaveBeenCalledWith(expect.objectContaining({ message: '思考强度未确认', color: 'red' }))
  })
  it('shows mobile pins above all projects, without a duplicate project row', () => {
    localStorage.setItem('astrorder_pinned_sessions', JSON.stringify({ [scopeKey('inert', session.id)]: true }))
    const view = mount()
    fireEvent.click(screen.getByRole('button', { name: '打开会话列表' }))
    const pinned = screen.getByRole('region', { name: '置顶会话' })
    expect(pinned).toHaveTextContent('隔离移动测试')
    const project = view.container.querySelector('.m-project-list > section:not(.m-pinned-sessions)')!
    expect(pinned.compareDocumentPosition(project) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(project.querySelectorAll('.m-session-row')).toHaveLength(0)
  })
  it('keeps session filters behind a toggle, defaulting to all', async () => {
    mount()
    fireEvent.click(screen.getByRole('button', { name: '打开会话列表' }))
    expect(await screen.findByRole('dialog', { name: '会话列表' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '未读' })).not.toBeInTheDocument()
    expect(screen.queryByLabelText('按 Agent 筛选')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '筛选' }))
    expect(screen.getByRole('button', { name: '未读' })).toBeInTheDocument()
    expect(screen.getByLabelText('按连接筛选')).toBeInTheDocument()
    expect(screen.getByRole('group', { name: '按 Agent 类型筛选' })).toBeInTheDocument()
  })
  it('opens the session drawer from a left-edge right swipe', async () => {
    const view = mount()
    const shell = view.container.querySelector('.mobile-workspace')!
    fireEvent.touchStart(shell, { touches: [{ clientX: 8, clientY: 300 }] })
    fireEvent.touchEnd(shell, { changedTouches: [{ clientX: 112, clientY: 307 }] })
    expect(await screen.findByRole('dialog', { name: '会话列表' })).toBeInTheDocument()
    expect(view.container.querySelector('.m-session-sheet')).toBeInTheDocument()
  })
  it('swipes through open session cards in an infinite loop', async () => {
    useAstrorderStore.getState().hydrateBootstrap({
      protocol_version: 1,
      cursor: 1,
      agents: [{ id: 'inert', kind: 'hermes', name: '协议测试', status: 'ready', capabilities: ['chat'], limitation: null }],
      sessions: [
        { ...session, id: 'session-1', is_open: true },
        { ...session, id: 'session-2', is_open: true },
      ],
    })
    const view = render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient()}>
          <MemoryRouter initialEntries={['/mobile/chat/session-2?agent_id=inert']}>
            <MobileWorkspace />
          </MemoryRouter>
        </QueryClientProvider>
      </MantineProvider>,
    )
    const transcript = view.container.querySelector('.m-transcript')!
    // Swipe next from session-2 (the tail) -> should loop back to session-1
    fireEvent.touchStart(transcript, { touches: [{ clientX: 260, clientY: 300 }] })
    fireEvent.touchEnd(transcript, { changedTouches: [{ clientX: 140, clientY: 390 }] })
    await waitFor(() => {
      expect(view.container.querySelector('.m-session-deck')).toHaveAttribute('data-session-key', 'inert::session-1')
    })
    // Swipe previous from session-1 (the head) -> should loop back to session-2
    const nextTranscript = view.container.querySelector('.m-session-stage:last-child .m-transcript')!
    fireEvent.touchStart(nextTranscript, { touches: [{ clientX: 140, clientY: 390 }] })
    fireEvent.touchEnd(nextTranscript, { changedTouches: [{ clientX: 260, clientY: 300 }] })
    await waitFor(() => {
      expect(view.container.querySelector('.m-session-deck')).toHaveAttribute('data-session-key', 'inert::session-2')
    })
  })
  it('does not close the session drawer on a vertical list scroll', async () => {
    const view = mount()
    fireEvent.click(screen.getByRole('button', { name: '打开会话列表' }))
    const sheet = await screen.findByRole('dialog', { name: '会话列表' })
    fireEvent.touchStart(sheet, { touches: [{ clientX: 180, clientY: 220 }] })
    fireEvent.touchMove(sheet, { touches: [{ clientX: 168, clientY: 390 }] })
    fireEvent.touchEnd(sheet)
    expect(sheet.style.transform).toBe('')
    expect(sheet.style.animation).toBe('')
    expect(screen.getByRole('dialog', { name: '会话列表' })).toBeInTheDocument()
    expect(view.container.querySelector('.m-session-sheet')).toBeInTheDocument()
  })
  it('leaves taps untouched before click dispatch, including after reopening', () => {
    mount()
    for (let attempt = 0; attempt < 3; attempt++) {
      fireEvent.click(screen.getByRole('button', { name: '打开会话列表' }))
      const sheet = screen.getByRole('dialog', { name: '会话列表' })
      const filter = within(sheet).getByRole('button', { name: '筛选' })
      const before = filter.getAttribute('aria-pressed')
      fireEvent.touchStart(filter, { touches: [{ clientX: 250, clientY: 40 }] })
      fireEvent.touchEnd(filter)
      expect(sheet.style.transform).toBe('')
      expect(sheet.style.animation).toBe('')
      fireEvent.click(filter)
      expect(filter.getAttribute('aria-pressed')).not.toBe(before)
      const row = within(sheet).getByRole('button', { name: '隔离移动测试' })
      fireEvent.touchStart(row, { touches: [{ clientX: 150, clientY: 220 }] })
      fireEvent.touchEnd(row)
      expect(sheet.style.transform).toBe('')
      fireEvent.click(row)
      expect(screen.queryByRole('dialog', { name: '会话列表' })).not.toBeInTheDocument()
    }
  })
  it('settles an incomplete swipe without replaying entry, and still closes on a full swipe', async () => {
    mount()
    fireEvent.click(screen.getByRole('button', { name: '打开会话列表' }))
    const sheet = screen.getByRole('dialog', { name: '会话列表' })
    fireEvent.touchStart(sheet, { touches: [{ clientX: 280, clientY: 220 }] })
    fireEvent.touchMove(sheet, { touches: [{ clientX: 190, clientY: 220 }] })
    expect(sheet.style.transform).toBe('translate3d(-90px, 0, 0)')
    fireEvent.touchEnd(sheet)
    await waitFor(() => expect(sheet.style.transition).toBe('none'))
    expect(sheet.style.transform).toBe('translate3d(0px, 0, 0)')
    expect(sheet.style.animation).toBe('none')
    fireEvent.touchStart(sheet, { touches: [{ clientX: 280, clientY: 220 }] })
    fireEvent.touchMove(sheet, { touches: [{ clientX: 140, clientY: 220 }] })
    fireEvent.touchEnd(sheet)
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '会话列表' })).not.toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', { name: '打开会话列表' }))
    expect(screen.getByRole('dialog', { name: '会话列表' }).style.transform).toBe('')
  })
  it('stores an offline file and draft before clearing without uploading', async () => {
    const upload = vi.spyOn(api, 'uploadAttachment').mockRejectedValue(new Error('must not upload offline'))
    const view = mount()
    fireEvent.change(view.container.querySelector('input[type=file]')!, { target: { files: [new File(['offline'], 'test.txt', { type: 'text/plain' })] } })
    fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: 'offline message' } })
    fireEvent.click(screen.getByRole('button', { name: /^发送$/ }))
    await waitFor(() => expect(memory.rows).toHaveLength(1))
    expect(memory.rows[0].files[0].name).toBe('test.txt')
    expect(memory.rows[0].payload.text).toBe('offline message')
    expect(upload).not.toHaveBeenCalled()
    await waitFor(() => expect(screen.getByLabelText('消息内容')).toHaveValue(''))
  })

  it('restores an offline draft with its quote and file when durable queue storage fails', async () => {
    const create = vi.spyOn(api, 'createCommand')
    const upload = vi.spyOn(api, 'uploadAttachment')
    const notify = vi.spyOn(notifications, 'show')
    useAstrorderStore.getState().mergeMessages(session.agent_id, session.id, [{ id: 'quoted', agent_id: session.agent_id, session_id: session.id, role: 'assistant', kind: 'message', text: '需要保留的引用', attachments: [], created_at: session.updated_at, command_id: null, tool: null }])
    const view = mount()
    await screen.findByText('bound-model')
    const save = vi.spyOn(mobileOutboxStorage, 'save').mockRejectedValue(new Error('本地队列保存被中断，输入内容已保留'))
    fireEvent.contextMenu(view.container.querySelector('.m-msg')!, { clientX: 140, clientY: 220 })
    fireEvent.click(screen.getByRole('menuitem', { name: '引用' }))
    const file = new File(['offline contents'], 'keep.txt', { type: 'text/plain' })
    fireEvent.change(view.container.querySelector('input[type=file]')!, { target: { files: [file] } })
    fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '必须保留的草稿' } })
    fireEvent.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(notify).toHaveBeenCalledWith(expect.objectContaining({ message: '本地队列保存被中断，输入内容已保留', color: 'red' })))
    expect(save).toHaveBeenCalled()
    expect(memory.rows).toEqual([])
    expect(screen.getByLabelText('消息内容')).toHaveValue('必须保留的草稿')
    expect(document.querySelector('.m-quote')).toHaveTextContent('需要保留的引用')
    expect(screen.getByRole('button', { name: '移除 keep.txt' })).toBeVisible()
    expect(useAstrorderStore.getState().drafts[scopeKey(session.agent_id, session.id)].attachments[0].file).toBe(file)
    expect(create).not.toHaveBeenCalled()
    expect(upload).not.toHaveBeenCalled()
  })

  it('restores an online draft without leaving a sent bubble when persistence fails', async () => {
    useAstrorderStore.getState().setConnection('connected')
    const create = vi.spyOn(api, 'createCommand')
    const upload = vi.spyOn(api, 'uploadAttachment')
    const notify = vi.spyOn(notifications, 'show')
    mount()
    await screen.findByText('bound-model')
    vi.spyOn(directOutboxStorage, 'save').mockRejectedValue(new Error('queue quota exceeded'))
    fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '仍未发送的消息' } })
    fireEvent.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(notify).toHaveBeenCalledWith(expect.objectContaining({ message: 'queue quota exceeded', color: 'red' })))
    expect(screen.getByLabelText('消息内容')).toHaveValue('仍未发送的消息')
    expect(selectMessages(useAstrorderStore.getState(), session.agent_id, session.id)).toEqual([])
    expect(memory.rows).toEqual([])
    expect(create).not.toHaveBeenCalled()
    expect(upload).not.toHaveBeenCalled()
  })

  it('confirms mobile session deletion in a UI popover instead of a browser dialog', async () => {
    const deleteSession = vi.spyOn(api, 'deleteSession').mockResolvedValue({ ok: true, id: session.id })
    const nativeConfirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    mount()

    fireEvent.click(screen.getByRole('button', { name: '打开会话列表' }))
    const row = await screen.findByRole('button', { name: '隔离移动测试' })
    fireEvent.contextMenu(row, { clientX: 180, clientY: 300 })
    fireEvent.click(screen.getByRole('menuitem', { name: '删除' }))

    const confirmation = await screen.findByRole('dialog', { name: '删除会话？' })
    expect(nativeConfirm).not.toHaveBeenCalled()
    expect(confirmation).toHaveStyle({ left: '188px', top: '308px' })
    fireEvent.click(within(confirmation).getByRole('button', { name: '删除' }))

    await waitFor(() => expect(deleteSession).toHaveBeenCalledWith(session.id, session.agent_id))
  })

  it('confirms ending all mobile tasks in a UI popover instead of a browser dialog', async () => {
    const stop = vi.spyOn(api, 'createCommand').mockImplementation(async (input) => ({ ...input, state: 'completed', attachments: [], created_at: session.updated_at, error: null }))
    const nativeConfirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    mount()

    fireEvent.click(screen.getByRole('button', { name: '会话操作' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '终止全部任务' }), { clientX: 300, clientY: 220 })

    const confirmation = await screen.findByRole('dialog', { name: '终止全部任务？' })
    expect(nativeConfirm).not.toHaveBeenCalled()
    fireEvent.click(within(confirmation).getByRole('button', { name: '终止全部' }))

    await waitFor(() => expect(stop).toHaveBeenCalledWith(expect.objectContaining({ agent_id: session.agent_id, session_id: session.id, action: 'stop' })))
  })

  it('confirms mobile project deletion in a UI popover instead of a browser dialog', async () => {
    const projectSession = { ...session, workspace: '/workspace/mobile', source_id: 'source-mobile', project_id: 'project-mobile', project_name: '移动项目' }
    useAstrorderStore.getState().hydrateBootstrap({
      protocol_version: 1,
      cursor: 1,
      agents: [{ id: 'inert', kind: 'hermes', name: '协议测试', status: 'ready', capabilities: ['chat', 'stop', 'attachments'], limitation: null, source_id: 'source-mobile' }],
      projects: [{ id: 'project-row', source_id: 'source-mobile', agent_id: 'inert', project_id: 'project-mobile', project_name: '移动项目', workspace: '/workspace/mobile', session_count: 1, updated_at: session.updated_at }],
      sessions: [projectSession],
    })
    const deleteProject = vi.spyOn(api, 'deleteProject').mockResolvedValue({ ok: true, deleted_sessions: [{ agent_id: session.agent_id, id: session.id }] })
    const nativeConfirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    mount()

    fireEvent.click(screen.getByRole('button', { name: '打开会话列表' }))
    fireEvent.click(await screen.findByRole('button', { name: '项目操作 移动项目' }))
    fireEvent.click(screen.getByRole('menuitem', { name: '删除项目' }), { clientX: 240, clientY: 280 })

    const confirmation = await screen.findByRole('dialog', { name: '删除项目？' })
    expect(nativeConfirm).not.toHaveBeenCalled()
    fireEvent.click(within(confirmation).getByRole('button', { name: '删除' }))

    await waitFor(() => expect(deleteProject).toHaveBeenCalledWith(expect.objectContaining({ project_id: 'project-mobile', source_id: 'source-mobile', delete_sessions: true })))
  })

  it('confirms stopping a mobile task in a UI popover instead of a browser dialog', async () => {
    const task = { id: 'running-task', session_id: session.id, agent_id: session.agent_id, kind: 'todo' as const, title: '运行任务', status: 'running' as const, progress: null, command: null, logs: [], target_id: 'native-turn', created_at: session.updated_at, updated_at: session.updated_at }
    useAstrorderStore.getState().mergeTasks([task])
    const stop = vi.spyOn(api, 'createCommand').mockImplementation(async (input) => ({ ...input, state: 'completed', attachments: [], created_at: session.updated_at, error: null }))
    const nativeConfirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    mount()

    fireEvent.click(await screen.findByRole('button', { name: '运行任务' }))
    fireEvent.click(screen.getByRole('button', { name: '停止任务' }), { clientX: 260, clientY: 360 })

    const confirmation = await screen.findByRole('dialog', { name: '停止任务？' })
    expect(nativeConfirm).not.toHaveBeenCalled()
    fireEvent.click(within(confirmation).getByRole('button', { name: '停止任务' }))

    await waitFor(() => expect(stop).toHaveBeenCalledWith(expect.objectContaining({ agent_id: session.agent_id, session_id: session.id, action: 'stop' })))
  })

  it('updates an open task from store events and does not stop it after completion', async () => {
    const task = { id: 'live-task', session_id: session.id, agent_id: session.agent_id, kind: 'todo' as const, title: '实时任务', status: 'running' as const, progress: null, command: null, logs: [], target_id: 'native-turn', created_at: session.updated_at, updated_at: session.updated_at }
    useAstrorderStore.getState().mergeTasks([task])
    const stop = vi.spyOn(api, 'createCommand')
    mount()
    fireEvent.click(await screen.findByRole('button', { name: '实时任务' }))
    fireEvent.click(screen.getByRole('button', { name: '停止任务' }))
    const confirmation = await screen.findByRole('dialog', { name: '停止任务？' })
    act(() => useAstrorderStore.getState().mergeTasks([{ ...task, status: 'completed', logs: [{ id: 'final', text: '最终日志', level: 'info', created_at: session.updated_at }] }]))
    expect(screen.getByText('已完成')).toBeInTheDocument()
    expect(screen.getByText('最终日志')).toBeInTheDocument()
    // Only the already-open confirmation remains, and it rechecks the live task.
    expect(screen.getAllByRole('button', { name: '停止任务' })).toHaveLength(1)
    fireEvent.click(within(confirmation).getByRole('button', { name: '停止任务' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '停止任务？' })).not.toBeInTheDocument())
    expect(stop).not.toHaveBeenCalled()
    expect(screen.queryByRole('button', { name: '停止任务' })).not.toBeInTheDocument()
  })

  it('opens agent command menu on typing / in mobile composer and selects command', async () => {
    vi.spyOn(api, 'getAgentCommands').mockResolvedValue({
      items: [
        { name: 'compact', description: '压缩上下文', input_hint: null },
      ],
    })
    mount()
    const input = screen.getByRole('textbox', { name: '消息内容' })
    fireEvent.change(input, { target: { value: '/' } })
    const listbox = await screen.findByRole('listbox', { name: 'Agent 命令' })
    expect(listbox).toBeInTheDocument()
    expect(screen.getByText('/compact')).toBeInTheDocument()
    fireEvent.click(screen.getByText('/compact'))
    expect(input).toHaveValue('/compact')
  })

  it('opens agent mention menu on typing @ in mobile composer and selects mention', async () => {
    vi.spyOn(api, 'getAgentMentions').mockResolvedValue({
      items: [
        { name: 'browser', description: '浏览器操作技能', kind: 'skill' as const, path: '/skills/browser' },
      ],
    })
    vi.spyOn(api, 'searchFiles').mockResolvedValue({ root: '/workspace', items: [] })
    mount()
    const input = screen.getByRole('textbox', { name: '消息内容' })
    fireEvent.change(input, { target: { value: '@' } })
    const listbox = await screen.findByRole('listbox', { name: '可提及资源' })
    expect(listbox).toBeInTheDocument()
    expect(screen.getByText('@browser')).toBeInTheDocument()
    fireEvent.click(screen.getByText('@browser'))
    expect(input).toHaveValue('@browser ')
  })

  it('offers handoff dialog from session menu when another agent is available', async () => {
    useAstrorderStore.getState().hydrateBootstrap({
      protocol_version: 1,
      cursor: 0,
      agents: [
        { id: 'inert', kind: 'hermes', name: 'Hermes', status: 'ready', capabilities: ['chat', 'stop', 'attachments'], limitation: null },
        { id: 'codex', kind: 'codex', name: 'Codex', status: 'ready', capabilities: ['chat'], limitation: null },
      ],
      sessions: [session],
    })
    mount()
    fireEvent.click(screen.getByRole('button', { name: '会话操作' }))
    const handoffItem = await screen.findByRole('menuitem', { name: '转交' })
    expect(handoffItem).toBeInTheDocument()
    fireEvent.click(handoffItem)
    expect(await screen.findByRole('dialog', { name: '转交会话' })).toBeInTheDocument()
  })

  it('opens rename modal and updates session title', async () => {
    const update = vi.spyOn(api, 'updateSession').mockResolvedValue({ ...session, title: '新会话标题' })
    mount()
    fireEvent.click(screen.getByRole('button', { name: '会话操作' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '重命名' }))
    const dialog = await screen.findByRole('dialog', { name: '重命名会话' })
    const input = within(dialog).getByRole('textbox', { name: '会话名称' })
    fireEvent.change(input, { target: { value: '新会话标题' } })
    fireEvent.click(within(dialog).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(update).toHaveBeenCalledWith(session.id, { agent_id: session.agent_id, title: '新会话标题' }))
  })
})
