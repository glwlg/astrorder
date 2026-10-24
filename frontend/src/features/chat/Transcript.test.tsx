import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
afterEach(cleanup)
import type { Approval, Message, OutboxEntry, Session } from '../../domain/types'
import { Transcript } from './Transcript'

const message: Message = {
  id: 'message-1',
  session_id: 'session-1',
  agent_id: 'agent-1',
  role: 'assistant',
  kind: 'message',
  text: '可滚动的消息',
  attachments: [],
  created_at: '2026-01-01T00:00:00Z',
  command_id: null,
  tool: null,
}

const pendingApproval: Approval = {
  id: 'approval-1',
  agent_id: 'agent-1',
  session_id: 'session-1',
  title: 'Codex 请求执行授权',
  detail: '/usr/bin/zsh -lc "echo 123"',
  state: 'pending',
  target_id: 'call-1',
  data: {},
}

const runningSession: Session = {
  id: 'session-1', agent_id: 'agent-1', title: '会话', workspace: null,
  status: 'running', updated_at: '2026-01-01T00:00:00Z',
}

const outbound: OutboxEntry = {
  command: {
    id: 'command-1', session_id: 'session-1', agent_id: 'agent-1', action: 'send',
    state: 'accepted', text: '新消息', attachments: [], created_at: '2026-01-01T00:00:00Z', error: null,
  },
  status: 'accepted',
  error: null,
}

describe('transcript follow mode', () => {
  it('splits long running activity into 100-item packs and closes earlier packs', () => {
    const messages: Message[] = Array.from({ length: 201 }, (_, i) => ({ ...message, id: `tool-${i}`, role: 'tool', kind: 'tool', text: '', tool: { name: 'commandExecution', status: 'completed', arguments: { command: `echo ${i}` } } }))
    const view = render(<MantineProvider><Transcript outbox={[]} messages={messages} session={runningSession} /></MantineProvider>)
    const packs = screen.getAllByRole('region', { name: '思考与工具' })
    expect(packs).toHaveLength(3)
    expect(packs.map(pack => pack.querySelector('details')!.open)).toEqual([false, false, true])
    fireEvent.click(packs[0].querySelector('summary')!)
    expect(packs[0].querySelectorAll('.activity-fold')).toHaveLength(100)
    view.rerender(<MantineProvider><Transcript outbox={[]} messages={[...messages, { ...messages[0], id: 'tool-201' }]} session={runningSession} /></MantineProvider>)
    expect(packs[0].querySelector('details')).toHaveAttribute('open')
  })
  it('hides empty user echoes without hiding pending text or attachment-only sends', () => {
    const input: Message = { ...message, id: 'optimistic-send', role: 'user', text: '这个要怎么弄？', command_id: 'send' }
    const empty: Message = { ...input, id: 'native-user', text: '' }
    const attachment: Message = { ...input, id: 'attachment-user', text: '', command_id: 'image', attachments: [{ id: 'image', name: 'image.png', media_type: 'image/png', url: '/image.png' }] }
    const view = render(<MantineProvider><Transcript outbox={[]} session={runningSession} onEditLastUserMessage={vi.fn()} messages={[input, empty, { ...empty, id: 'whitespace-user', text: ' \n ' }, attachment]} /></MantineProvider>)
    expect(screen.queryByTestId('message-native-user')).not.toBeInTheDocument()
    expect(screen.queryByTestId('message-whitespace-user')).not.toBeInTheDocument()
    expect(screen.getByTestId('message-optimistic-send')).toHaveTextContent('这个要怎么弄？')
    expect(screen.getByAltText('image.png')).toBeInTheDocument()
    view.rerender(<MantineProvider><Transcript outbox={[]} messages={[{ ...empty, text: '这个要怎么弄？' }]} /></MantineProvider>)
    expect(screen.getByTestId('message-native-user')).toHaveTextContent('这个要怎么弄？')
  })

  it('keeps each tool pack between its surrounding commentary', () => {
    const tool: Message = { ...message, role: 'tool', kind: 'tool', tool: { name: 'commandExecution', status: 'completed', arguments: { command: 'git status' } } }
    const view = render(<MantineProvider><Transcript outbox={[]} messages={[
      { ...message, id: 'intro', text: '第一段说明' },
      { ...tool, id: 'read', created_at: '2026-01-01T00:01:00Z' },
      { ...message, id: 'update', text: '第二段说明' },
      { ...tool, id: 'edit', created_at: '2026-01-01T00:02:00Z' },
      { ...message, id: 'answer', text: '最终答复' },
    ]} /></MantineProvider>)
    const children = Array.from(view.container.querySelector('.transcript-inner')!.children)
      .filter(node => node.classList.contains('transcript-message-entry') || node.classList.contains('activity-pack'))
    expect(children.map(node => node.classList.contains('activity-pack') ? 'tool-pack' : node.querySelector('[data-testid]')?.getAttribute('data-testid')))
      .toEqual(['message-intro', 'tool-pack', 'message-update', 'tool-pack', 'message-answer'])
    for (const pack of screen.getAllByRole('region', { name: '思考与工具' })) fireEvent.click(pack.querySelector('summary')!)
    expect(screen.getByTestId('message-read')).toBeInTheDocument()
    expect(screen.getByTestId('message-edit')).toBeInTheDocument()
  })

  it.each(['codex', 'hermes'])('puts %s commentary metadata at the text end and keeps final reply metadata below', (agentId) => {
    const view = render(<MantineProvider><Transcript outbox={[]} session={{ ...runningSession, status: 'idle' }} messages={[
      { ...message, agent_id: agentId, id: 'intro', text: '第一段说明。', created_at: agentId === 'codex' ? '1970-01-01T00:00:00Z' : message.created_at },
      { ...message, agent_id: agentId, id: 'tool', role: 'tool', kind: 'tool', tool: { name: 'terminal' } },
      { ...message, agent_id: agentId, id: 'final', text: '最终回复。' },
    ]} /></MantineProvider>)
    const intro = screen.getByTestId('message-intro')
    expect(intro.querySelector('.message-bubble > .message-meta-inline')).toContainElement(intro.querySelector('button[aria-label="复制消息"]'))
    expect(intro.querySelectorAll('time')).toHaveLength(agentId === 'hermes' ? 1 : 0)
    const final = screen.getByTestId('message-final')
    expect(final.querySelector(':scope > .message-meta')).toContainElement(final.querySelector('button[aria-label="复制消息"]'))
    expect(final.querySelector('.message-bubble .message-meta')).toBeNull()
    expect(view.container.querySelectorAll('button[aria-label="复制消息"]')).toHaveLength(2)
  })

  it('keeps the running commentary metadata inline until the turn finishes', () => {
    const view = render(<MantineProvider><Transcript outbox={[]} session={runningSession} messages={[message]} /></MantineProvider>)
    expect(screen.getByTestId('message-message-1').querySelector('.message-bubble > .message-meta-inline')).not.toBeNull()
    view.rerender(<MantineProvider><Transcript outbox={[]} session={{ ...runningSession, status: 'idle' }} messages={[message]} /></MantineProvider>)
    expect(screen.getByTestId('message-message-1').querySelector(':scope > .message-meta')).not.toBeNull()
  })

  it('offers a fork only at the end of an assistant turn', () => {
    render(<MantineProvider><Transcript outbox={[]} onForkAtMessage={vi.fn()} messages={[
      { ...message, id: 'intro', text: '先检查' },
      { ...message, id: 'tool', role: 'tool', kind: 'tool', text: 'done', tool: { name: 'terminal' } },
      { ...message, id: 'final', text: '完成' },
    ]} /></MantineProvider>)
    expect(screen.getAllByRole('button', { name: '分叉会话' })).toHaveLength(1)
  })
  it('groups reasoning and tools into folds and hides empty assistant bubbles', () => {
    render(<MantineProvider><Transcript outbox={[]} messages={[
      { ...message, id: 'thinking', kind: 'thinking', text: '检查数据' },
      { ...message, id: 'empty-thinking', kind: 'thinking', text: '' },
      { ...message, id: 'tool', kind: 'tool', role: 'tool', text: '工具结果', tool: { name: 'execute_code', arguments: { code: 'print(1)' } } },
      { ...message, id: 'empty', text: '' },
      { ...message, id: 'answer', text: '最终答复' },
    ]} /></MantineProvider>)
    const pack = screen.getByRole('region', { name: '思考与工具' })
    expect(pack.querySelectorAll('details')).toHaveLength(1)
    expect(pack.querySelector('details')).not.toHaveAttribute('open')
    expect(screen.queryByText('工具 · execute_code')).not.toBeInTheDocument()
    fireEvent.click(pack.querySelector('summary')!)
    expect(pack.querySelectorAll('.activity-fold')).toHaveLength(2)
    expect(screen.getByText('检查数据')).toBeInTheDocument()
    expect(screen.getByText(/execute_code/)).toBeInTheDocument()
    expect(screen.queryByTestId('message-empty')).not.toBeInTheDocument()
    expect(screen.getByText('最终答复')).toBeInTheDocument()
  })

  it('does not leave empty native system messages between tool calls', () => {
    const tool = { ...message, role: 'tool' as const, kind: 'tool' as const, text: '', tool: { name: 'commandExecution', arguments: { command: 'git status' }, status: 'completed' } }
    render(<MantineProvider><Transcript outbox={[]} messages={[
      { ...tool, id: 'tool-before' },
      { ...message, id: 'empty-system', role: 'system', text: '' },
      { ...message, id: 'whitespace-system', role: 'system', text: '  \n ' },
      { ...message, id: 'empty-reasoning', kind: 'thinking', text: '' },
      { ...tool, id: 'tool-after' },
      { ...message, id: 'system-notice', role: 'system', text: '有效的系统提示' },
      { ...message, id: 'final-answer', text: '完整的最终答复' },
    ]} /></MantineProvider>)
    expect(screen.queryByTestId('message-empty-system')).not.toBeInTheDocument()
    expect(screen.queryByTestId('message-whitespace-system')).not.toBeInTheDocument()
    const packs = screen.getAllByRole('region', { name: '思考与工具' })
    expect(packs).toHaveLength(1)
    expect(packs[0]).toHaveTextContent('运行了 2 个命令')
    expect(screen.getByText('有效的系统提示')).toBeInTheDocument()
    expect(screen.getByText('完整的最终答复')).toBeInTheDocument()
    fireEvent.click(packs[0].querySelector('summary')!)
    expect(screen.getByTestId('message-tool-before')).toBeInTheDocument()
    expect(screen.getByTestId('message-tool-after')).toBeInTheDocument()
  })

  it('renders native Codex commentary as messages and drops empty native placeholders', () => {
    const native = (kind: string) => kind as Message['kind']
    render(<MantineProvider><Transcript outbox={[]} messages={[
      { ...message, id: 'commentary', kind: native('agentMessage'), text: '我会只提交本次改动。' },
      { ...message, id: 'empty-native', kind: native('agentMessage'), text: '' },
      { ...message, id: 'reasoning', kind: native('reasoning'), text: '核对提交范围' },
      { ...message, id: 'empty-reasoning-native', kind: native('reasoning'), text: '' },
      { ...message, id: 'plan-native', kind: native('plan'), text: '执行定向发布' },
      { ...message, id: 'silent-command', role: 'tool', kind: 'tool', text: '', tool: { name: 'commandExecution', arguments: { command: 'git add selected.go' }, status: 'completed' } },
    ]} /></MantineProvider>)
    expect(screen.getByTestId('message-commentary')).toHaveClass('message-row')
    expect(screen.getByTestId('message-plan-native')).toHaveClass('message-row')
    expect(screen.queryByTestId('message-empty-native')).not.toBeInTheDocument()
    const packs = screen.getAllByRole('region', { name: '思考与工具' })
    expect(packs).toHaveLength(2)
    for (const pack of packs) fireEvent.click(pack.querySelector('summary')!)
    fireEvent.click(screen.getByTestId('message-reasoning').querySelector('summary')!)
    expect(screen.getByTestId('message-reasoning').querySelector('.activity-thinking-content')).toHaveTextContent('核对提交范围')
    expect(screen.queryByTestId('message-empty-reasoning-native')).not.toBeInTheDocument()
    expect(screen.getByTestId('message-silent-command')).toHaveTextContent('git add selected.go')
    expect(screen.queryByText(/^tool$/)).not.toBeInTheDocument()
  })

  it('uses an animated loader for the latest folded activity while the session is running', () => {
    const view = render(<MantineProvider><Transcript outbox={[]} session={runningSession} messages={[
      { ...message, id: 'thinking', kind: 'thinking', text: '正在处理' },
    ]} /></MantineProvider>)
    const summary = screen.getByRole('region', { name: '思考与工具' }).querySelector('summary')!
    expect(summary.querySelector('.lazy-details-indicator.is-loading')).not.toBeNull()
    expect(summary.querySelectorAll('.lazy-details-indicator svg circle')).toHaveLength(5)
    expect(summary.querySelector('.lazy-details-indicator svg path')).not.toBeNull()
    expect(summary.textContent).not.toMatch(/[▶▸]/)

    view.rerender(<MantineProvider><Transcript outbox={[]} session={{ ...runningSession, status: 'idle' }} messages={[
      { ...message, id: 'thinking', kind: 'thinking', text: '处理完成' },
    ]} /></MantineProvider>)
    const updatedSummary = screen.getByRole('region', { name: '思考与工具' }).querySelector('summary')!
    expect(updatedSummary.querySelector('.lazy-details-indicator.is-loading')).toBeNull()
    expect(updatedSummary.querySelector('.lazy-details-indicator svg')).not.toBeNull()
  })

  it('does not reactivate an earlier activity while waiting for a newer reply', () => {
    render(<MantineProvider><Transcript outbox={[]} session={runningSession} messages={[
      { ...message, id: 'old-tool', kind: 'tool', role: 'tool', text: '旧命令', tool: { name: 'exec_command', arguments: { command: 'echo old' }, status: 'running' } },
      { ...message, id: 'new-user', role: 'user', text: '新问题', created_at: '2026-01-02T00:00:00Z' },
    ]} /></MantineProvider>)

    const summary = screen.getByRole('region', { name: '思考与工具' }).querySelector('summary')!
    expect(summary.querySelector('.lazy-details-indicator.is-loading')).toBeNull()
    expect(summary.querySelector('.activity-badge.is-running')).toBeNull()
    expect(screen.getByText('正在思考并准备回复…')).toBeInTheDocument()
  })

  it('pauses only after a real scroll away from the bottom and resumes manually', async () => {
    render(
      <MantineProvider>
        <Transcript messages={[message]} outbox={[]} />
      </MantineProvider>,
    )
    const transcript = screen.getByRole('log')
    Object.defineProperties(transcript, {
      clientHeight: { configurable: true, value: 100 },
      scrollHeight: { configurable: true, value: 1000 },
      scrollTop: { configurable: true, writable: true, value: 900 },
    })
    const scrollTo = vi.fn(({ top }: { top: number }) => {
      transcript.scrollTop = top
    })
    Object.defineProperty(transcript, 'scrollTo', { configurable: true, value: scrollTo })
    await new Promise<void>(resolve => requestAnimationFrame(() => resolve()))

    transcript.scrollTop = 100
    fireEvent.scroll(transcript)
    await waitFor(() => expect(screen.getByTestId('return-bottom')).toBeInTheDocument())

    fireEvent.click(screen.getByTestId('return-bottom'))
    await waitFor(() => expect(screen.queryByTestId('return-bottom')).not.toBeInTheDocument())
    expect(scrollTo).toHaveBeenLastCalledWith({ top: 1000, behavior: 'auto' })
    expect(transcript.scrollTop).toBe(1000)
  })

  it('returns to the bottom when a message is sent and shows animated dots while running', async () => {
    const view = render(
      <MantineProvider>
        <Transcript messages={[message]} outbox={[]} session={runningSession} />
      </MantineProvider>,
    )
    const transcript = screen.getByRole('log')
    Object.defineProperties(transcript, {
      clientHeight: { configurable: true, value: 100 },
      scrollHeight: { configurable: true, value: 1000 },
      scrollTop: { configurable: true, writable: true, value: 100 },
    })
    Object.defineProperty(transcript, 'scrollTo', {
      configurable: true,
      value: vi.fn(({ top }: { top: number }) => { transcript.scrollTop = top }),
    })
    await new Promise<void>(resolve => requestAnimationFrame(() => resolve()))
    transcript.scrollTop = 100
    fireEvent.scroll(transcript)
    await waitFor(() => expect(screen.getByTestId('return-bottom').querySelector('.return-bottom-wave')).not.toBeNull())

    view.rerender(
      <MantineProvider>
        <Transcript messages={[message]} outbox={[outbound]} session={runningSession} />
      </MantineProvider>,
    )
    await waitFor(() => expect(screen.queryByTestId('return-bottom')).not.toBeInTheDocument())
    expect(transcript.scrollTop).toBe(1000)
  })

  it('renders pending approvals inline in the conversation stream and triggers approval actions', () => {
    const onApproval = vi.fn()
    render(
      <MantineProvider>
        <Transcript
          messages={[message]}
          outbox={[]}
          approvals={[pendingApproval]}
          onApproval={onApproval}
        />
      </MantineProvider>,
    )
    expect(screen.getByRole('region', { name: '对话待处理审批' })).toBeInTheDocument()
    expect(screen.getByText('Codex 请求执行授权')).toBeInTheDocument()
    expect(screen.getByText(/echo 123/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '允许' }))
    expect(onApproval).toHaveBeenCalledWith(pendingApproval, 'approve')
    fireEvent.click(screen.getByRole('button', { name: '取消' }))
    expect(onApproval).toHaveBeenCalledWith(pendingApproval, 'cancel')
  })

  it('renders a native image reference once when the same image is already attached', () => {
    render(
      <MantineProvider>
        <Transcript
          outbox={[]}
          messages={[
            {
              ...message,
              role: 'user',
              text: '请查看\n@image:C:\\native\\upload.png',
              attachments: [
                {
                  id: 'upload-1',
                  name: 'upload.png',
                  media_type: 'image/png',
                  url: '/api/v1/attachments/upload-1',
                },
              ],
            },
          ]}
        />
      </MantineProvider>,
    )

    expect(screen.getAllByAltText('upload.png')).toHaveLength(1)
    expect(screen.queryByText(/@image:/)).not.toBeInTheDocument()
  })
})
