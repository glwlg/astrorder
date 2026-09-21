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
import { scopeKey } from '../../domain/semantics'
import { draftStorage } from '../chat/draftStorage'

const memory = vi.hoisted(() => ({ rows: [] as OutboxEntry[] }))
vi.mock('./mobileOutboxStorage', () => ({ mobileOutboxStorage: { load: async () => memory.rows, save: async (rows: OutboxEntry[]) => { memory.rows = rows } } }))
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
  memory.rows = []
  const preferences = { appearance: {}, session_pins: {}, pinned_projects: [], project_order: [] }
  vi.spyOn(api, 'getPreferences').mockResolvedValue(preferences)
  vi.spyOn(api, 'importPreferences').mockImplementation(async values => ({ ...preferences, ...values, appearance: values.appearance as typeof preferences.appearance || {} }))
  vi.spyOn(api, 'updatePreferences').mockImplementation(async values => ({ ...preferences, ...values, appearance: values.appearance as typeof preferences.appearance || {} }))
  vi.spyOn(api, 'getOpenSessions').mockResolvedValue({ known_agent_ids: [], items: [] })
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
  it('reads and displays the native-bound model immediately when opening a session', async () => {
    mount()
    expect(await screen.findByText('bound-model')).toBeVisible()
    expect(screen.getByRole('button', { name: '选择会话模型' })).toHaveAttribute('title', 'p/bound-model')
    expect(api.getSessionModel).toHaveBeenCalledWith('native-test', 'inert')
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
    const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: 'accepted', attachments: [], created_at: session.updated_at, error: null }))
    mount()
    fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '直接发送' } })
    fireEvent.click(screen.getByRole('button', { name: '发送' }))
    expect(selectMessages(useAstrorderStore.getState(), 'inert', 'native-test').some(message => message.text === '直接发送')).toBe(true)
    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({ text: '直接发送', action: 'send' })))
    expect(memory.rows).toEqual([])
  })
  it('sends on pointer down so dismissing the keyboard does not eat the tap', async () => {
    useAstrorderStore.getState().setConnection('connected')
    const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: 'accepted', attachments: [], created_at: session.updated_at, error: null }))
    mount()
    fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '按下即发' } })
    fireEvent.pointerDown(screen.getByRole('button', { name: '发送' }), { button: 0 })
    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({ text: '按下即发', action: 'send' })))
  })
  it('keeps sending available while skill evolution runs in the background', async () => {
    useAstrorderStore.getState().setConnection('connected')
    useAstrorderStore.getState().mergeTasks([{ id: 'skill-task', session_id: session.id, agent_id: session.agent_id, kind: 'background', title: '工具：skill_manage', status: 'running', progress: { blocking: false }, command: null, logs: [], target_id: 'call', created_at: session.updated_at, updated_at: session.updated_at }])
    const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: 'accepted', attachments: [], created_at: session.updated_at, error: null }))
    mount()
    fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '继续提问' } })
    fireEvent.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({ text: '继续提问', action: 'send' })))
  })
  it('queues while the agent is running and can send that entry as guidance', async () => {
    useAstrorderStore.getState().setConnection('connected')
    useAstrorderStore.getState().hydrateBootstrap({ protocol_version: 1, cursor: 1, agents: [{ id: 'inert', kind: 'hermes', name: '协议测试', status: 'ready', capabilities: ['chat', 'stop', 'attachments'], limitation: null }], sessions: [{ ...session, status: 'running' }] })
    const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: 'accepted', attachments: [], created_at: session.updated_at, error: null }))
    mount()
    fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '排队消息' } })
    fireEvent.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(memory.rows[0]?.state).toBe('queued'))
    expect(create).not.toHaveBeenCalled()
    expect(selectMessages(useAstrorderStore.getState(), 'inert', 'native-test')).toEqual([])
    await dropQueued('send')
    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({ text: '排队消息', action: 'send' })))
  })
  it('edits a queued message back into the composer', async () => {
    useAstrorderStore.getState().setConnection('connected')
    useAstrorderStore.getState().hydrateBootstrap({ protocol_version: 1, cursor: 1, agents: [{ id: 'inert', kind: 'hermes', name: '协议测试', status: 'ready', capabilities: ['chat', 'stop', 'attachments'], limitation: null }], sessions: [{ ...session, status: 'running' }] })
    mount()
    fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '待编辑消息' } })
    fireEvent.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(memory.rows[0]?.state).toBe('queued'))
    await dropQueued('edit')
    await waitFor(() => expect(screen.getByLabelText('消息内容')).toHaveValue('待编辑消息'))
    expect(memory.rows).toEqual([])
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
    const task = { id: 'running-task', session_id: session.id, agent_id: session.agent_id, kind: 'background' as const, title: '运行任务', status: 'running' as const, progress: null, command: null, logs: [], target_id: 'native-turn', created_at: session.updated_at, updated_at: session.updated_at }
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
    const task = { id: 'live-task', session_id: session.id, agent_id: session.agent_id, kind: 'background' as const, title: '实时任务', status: 'running' as const, progress: null, command: null, logs: [], target_id: 'native-turn', created_at: session.updated_at, updated_at: session.updated_at }
    useAstrorderStore.getState().mergeTasks([task])
    const stop = vi.spyOn(api, 'createCommand')
    mount()
    fireEvent.click(await screen.findByRole('button', { name: '实时任务' }))
    fireEvent.click(screen.getByRole('button', { name: '停止任务' }))
    const confirmation = await screen.findByRole('dialog', { name: '停止任务？' })
    act(() => useAstrorderStore.getState().mergeTasks([{ ...task, status: 'completed', logs: [{ id: 'final', text: '最终日志', level: 'info', created_at: session.updated_at }] }]))
    expect(screen.getByText('completed')).toBeInTheDocument()
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
