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
  it('folds long pasted text while keeping attachments and copying the complete original', () => {
    const text = `第一行\n${'长文本\n'.repeat(2000)}最后一行`
    const copy = vi.fn().mockResolvedValue(undefined)
    const previousClipboard = Object.getOwnPropertyDescriptor(navigator, 'clipboard')
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: copy } })
    try {
      const view = render(<MantineProvider><Transcript outbox={[]} messages={[{
        ...message, role: 'user', text,
        attachments: [{ id: 'image', name: 'image.png', media_type: 'image/png', url: '/image.png' }],
      }]} /></MantineProvider>)
      expect(view.container.textContent).not.toContain('最后一行')
      expect(screen.getByAltText('image.png')).toBeInTheDocument()
      fireEvent.click(screen.getByRole('button', { name: '复制消息' }))
      expect(copy).toHaveBeenCalledWith(text)
      fireEvent.click(screen.getByText('展开全文'))
      expect(view.container.textContent).toContain('最后一行')
      fireEvent.click(screen.getByText('收起长消息'))
      expect(view.container.textContent).not.toContain('最后一行')
    } finally {
      if (previousClipboard) Object.defineProperty(navigator, 'clipboard', previousClipboard)
      else Reflect.deleteProperty(navigator, 'clipboard')
    }
  })

  it('counts failed operations as processed and leaves unknown and running operations pending', () => {
    const states = ['completed', 'failed', 'running', undefined]
    render(<MantineProvider><Transcript session={runningSession} outbox={[]} messages={states.map((status, i) => ({
      ...message, id: `state-${i}`, role: 'tool', kind: 'tool',
      tool: { name: 'commandExecution', status, arguments: { command: `echo ${i}` } },
    }))} /></MantineProvider>)
    expect(screen.getByLabelText('已处理 2 项，共 4 项')).toHaveTextContent('2 / 4')
    expect(screen.getByTestId('message-state-1')).toHaveAttribute('data-status', 'failed')
    expect(screen.getByTestId('message-state-2')).toHaveAttribute('data-status', 'running')
    expect(screen.getByTestId('message-state-3')).toHaveAttribute('data-status', 'recorded')
  })

  it('keeps long command output intact when opening and copying it', () => {
    const command = 'printf "%s" long-output'
    const output = `${'ordinary progress\n'.repeat(30)}fatal: complete output\n`
    const copy = vi.fn().mockResolvedValue(undefined)
    const previousClipboard = Object.getOwnPropertyDescriptor(navigator, 'clipboard')
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: copy } })
    try {
      render(<MantineProvider><Transcript outbox={[]} messages={[
        { ...message, id: 'command', role: 'tool', kind: 'tool', text: output, tool: { name: 'commandExecution', status: 'completed', arguments: { command } } },
      ]} /></MantineProvider>)
      const pack = screen.getByRole('region', { name: '思考与工具' })
      fireEvent.click(pack.querySelector('summary')!)
      fireEvent.click(screen.getByTestId('message-command').querySelector('summary')!)
      expect(pack.querySelector('.shell-output-text')?.textContent).toBe(output)
      expect(screen.queryByRole('button', { name: /降噪/ })).not.toBeInTheDocument()
      fireEvent.click(screen.getByRole('button', { name: '复制终端输出' }))
      expect(copy).toHaveBeenCalledWith(`$ ${command}\n${output}`)
    } finally {
      if (previousClipboard) Object.defineProperty(navigator, 'clipboard', previousClipboard)
      else Reflect.deleteProperty(navigator, 'clipboard')
    }
  })

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

  it('windows very long transcripts to recent visual rows and expands older rows on demand', () => {
    const many: Message[] = Array.from({ length: 90 }, (_, i) => ({
      ...message,
      id: `m-${i}`,
      role: i % 2 === 0 ? 'user' : 'assistant',
      text: `第 ${i} 条内容`,
    }))
    render(<MantineProvider><Transcript outbox={[]} messages={many} /></MantineProvider>)
    expect(screen.queryByTestId('message-m-0')).toBeNull()
    expect(screen.getByTestId('message-m-89')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /显示更早 30 条记录/ }))
    expect(screen.getByTestId('message-m-0')).toBeInTheDocument()
  })

  it('keeps the active execution bar open across brief mid-turn status dips and collapses once a final reply arrives', () => {
    const toolMsg: Message = {
      ...message,
      id: 'tool-1',
      role: 'tool',
      kind: 'tool',
      text: 'ok',
      tool: { name: 'run_command', status: 'completed', arguments: { command: 'echo hi' } },
    }
    const view = render(
      <MantineProvider>
        <Transcript outbox={[]} session={runningSession} messages={[toolMsg]} />
      </MantineProvider>,
    )

    const details = screen.getByRole('region', { name: '思考与工具' }).querySelector('details')!
    expect(details.open).toBe(true)

    // Brief mid-turn idle blip with no final reply yet must NOT immediately collapse or remount the bar.
    view.rerender(
      <MantineProvider>
        <Transcript outbox={[]} session={{ ...runningSession, status: 'idle' }} messages={[toolMsg]} />
      </MantineProvider>,
    )
    expect(screen.getByRole('region', { name: '思考与工具' }).querySelector('details')).toBe(details)
    expect(details.open).toBe(true)

    // Once the final assistant reply arrives, the execution bar collapses immediately without remounting.
    view.rerender(
      <MantineProvider>
        <Transcript
          outbox={[]}
          session={{ ...runningSession, status: 'idle' }}
          messages={[
            toolMsg,
            { ...message, id: 'final-reply', role: 'assistant', kind: 'message', text: '全部完成了。', created_at: '2026-01-02T00:01:00Z' },
          ]}
        />
      </MantineProvider>,
    )
    expect(screen.getByRole('region', { name: '思考与工具' }).querySelector('details')).toBe(details)
    expect(details.open).toBe(false)
  })

  it('opens in-session Ctrl+F find bar, cycles matches, toggles case sensitivity, unfolds older hits, and closes on Esc', async () => {
    const msgs: Message[] = Array.from({ length: 70 }, (_, idx) => ({
      ...message,
      id: `find-m-${idx}`,
      role: idx % 2 === 0 ? 'user' : 'assistant',
      text: idx === 2
        ? '早期记录里包含 Alpha 关键词'
        : idx === 68
          ? '最新回复里提到 alpha 和 ALPHA 两次'
          : `普通对话内容 #${idx}`,
      created_at: `2026-01-01T00:${String(Math.floor(idx / 60)).padStart(2, '0')}:${String(idx % 60).padStart(2, '0')}Z`,
    }))

    render(
      <MantineProvider>
        <Transcript outbox={[]} messages={msgs} enableFind />
      </MantineProvider>,
    )

    // Message #2 is folded behind the 60-row window initially
    expect(screen.queryByTestId('message-find-m-2')).not.toBeInTheDocument()

    // Trigger Ctrl+F
    fireEvent.keyDown(window, { key: 'f', ctrlKey: true })
    const input = await screen.findByRole('searchbox', { name: '搜索会话内容' })
    expect(input).toHaveFocus()

    // Case-insensitive search matches 3 occurrences (1 in folded m-2, 2 in m-68) and auto-expands m-2
    fireEvent.change(input, { target: { value: 'alpha' } })
    await waitFor(() => {
      expect(screen.getByRole('status')).toHaveTextContent('1 / 3')
      expect(screen.getByTestId('message-find-m-2')).toBeInTheDocument()
    })

    // Enter advances to 2 / 3, Shift+Enter returns to 1 / 3
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(screen.getByRole('status')).toHaveTextContent('2 / 3')
    fireEvent.keyDown(input, { key: 'Enter', shiftKey: true })
    expect(screen.getByRole('status')).toHaveTextContent('1 / 3')

    // Case-sensitive toggle narrows to only lowercase 'alpha' (1 hit)
    fireEvent.click(screen.getByRole('button', { name: '区分大小写' }))
    expect(screen.getByRole('status')).toHaveTextContent('1 / 1')

    // Esc closes find bar
    fireEvent.keyDown(input, { key: 'Escape' })
    expect(screen.queryByRole('searchbox', { name: '搜索会话内容' })).not.toBeInTheDocument()
  })

  it('does not hijack Ctrl+F when focus is inside a Monaco editor', () => {
    render(
      <MantineProvider>
        <div className="monaco-editor">
          <textarea aria-label="代码编辑器" />
        </div>
        <Transcript outbox={[]} messages={[message]} enableFind />
      </MantineProvider>,
    )

    const editor = screen.getByLabelText('代码编辑器')
    editor.focus()
    fireEvent.keyDown(editor, { key: 'f', ctrlKey: true })
    expect(screen.queryByRole('searchbox', { name: '搜索会话内容' })).not.toBeInTheDocument()
  })

  it('expands collapsed activity pack and scrolls via scroller.scrollTo when navigating matches', async () => {
    const packMessages: Message[] = [
      {
        ...message,
        id: 'pack-think-1',
        role: 'assistant',
        kind: 'thinking',
        text: '正在分析模板结构与字段约束',
        created_at: '2026-01-01T00:00:00Z',
        attachments: [],
      },
      {
        ...message,
        id: 'reply-after-1',
        role: 'assistant',
        kind: 'message',
        text: '已完成处理，详情请看上方分析。',
        created_at: '2026-01-01T00:01:00Z',
        attachments: [],
      },
      {
        ...message,
        id: 'reply-after-2',
        role: 'assistant',
        kind: 'message',
        text: '这是第二处包含模板关键词的普通回复。',
        created_at: '2026-01-01T00:02:00Z',
        attachments: [],
      },
    ]

    const view = render(
      <MantineProvider>
        <Transcript outbox={[]} messages={packMessages} enableFind />
      </MantineProvider>,
    )

    const scroller = view.container.querySelector<HTMLDivElement>('.transcript')!
    const scrollToSpy = vi.fn()
    scroller.scrollTo = scrollToSpy
    Object.defineProperty(scroller, 'scrollTop', { value: 1200, writable: true })
    vi.spyOn(scroller, 'getBoundingClientRect').mockReturnValue({
      top: 100,
      bottom: 700,
      left: 0,
      right: 800,
      width: 800,
      height: 600,
      x: 0,
      y: 100,
      toJSON: () => ({}),
    })

    // Collapsed pack does not mount inner message article before searching
    expect(screen.queryByTestId('message-pack-think-1')).not.toBeInTheDocument()

    fireEvent.keyDown(window, { key: 'f', ctrlKey: true })
    const input = await screen.findByRole('searchbox', { name: '搜索会话内容' })
    fireEvent.change(input, { target: { value: '模板' } })

    expect(screen.getByRole('status')).toHaveTextContent('1 / 2')

    // Collapsed activity pack and its inner fold auto-expand so the match becomes visible
    await waitFor(() => {
      expect(screen.getByTestId('message-pack-think-1')).toBeInTheDocument()
      expect(screen.getByTestId('message-pack-think-1').querySelector('details')?.open).toBe(true)
    })

    // Verify scroller.scrollTo is called with non-zero bounding rect calculation
    const targetMsg = view.container.querySelector<HTMLElement>('[data-turn-id="reply-after-2"]')!
    vi.spyOn(targetMsg, 'getBoundingClientRect').mockReturnValue({
      top: 40,
      bottom: 80,
      left: 0,
      right: 500,
      width: 500,
      height: 40,
      x: 0,
      y: 40,
      toJSON: () => ({}),
    })
    scrollToSpy.mockClear()
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(screen.getByRole('status')).toHaveTextContent('2 / 2')
    await waitFor(() => {
      expect(scrollToSpy).toHaveBeenCalled()
    })
    const lastCall = scrollToSpy.mock.calls[scrollToSpy.mock.calls.length - 1][0] as { top: number }
    expect(lastCall.top).toBeCloseTo(1200 + (40 - 100) - 600 * 0.38, 1)
  })

  it('keeps following bottom when content grows asynchronously without user upward scroll', async () => {
    render(
      <MantineProvider>
        <Transcript messages={[message]} outbox={[]} session={runningSession} />
      </MantineProvider>,
    )
    const transcript = screen.getByRole('log')
    let currentHeight = 1000
    let currentTop = 900
    Object.defineProperties(transcript, {
      clientHeight: { configurable: true, get: () => 100 },
      scrollHeight: { configurable: true, get: () => currentHeight },
      scrollTop: {
        configurable: true,
        get: () => currentTop,
        set: (value: number) => { currentTop = value },
      },
    })
    const scrollTo = vi.fn(({ top }: { top: number }) => { currentTop = Math.max(0, top - 100) })
    Object.defineProperty(transcript, 'scrollTo', { configurable: true, value: scrollTo })

    await new Promise<void>(resolve => requestAnimationFrame(() => resolve()))
    // Establish pinned bottom baseline (scrollTop = 900, scrollHeight = 1000, clientHeight = 100)
    fireEvent.scroll(transcript)
    expect(screen.queryByTestId('return-bottom')).not.toBeInTheDocument()

    // Simulate async streaming / ActivityPack growth (+400px) firing a scroll event while scrollTop stayed at 900
    currentHeight = 1400
    fireEvent.scroll(transcript)

    expect(screen.queryByTestId('return-bottom')).not.toBeInTheDocument()
    expect(scrollTo).toHaveBeenLastCalledWith({ top: 1400, behavior: 'auto' })
    expect(currentTop).toBe(1300)
  })

  it('keeps the top history slice anchored when appending new messages in long sessions', () => {
    const initialMessages = Array.from({ length: 70 }, (_, idx) => ({
      ...message,
      id: `m-${idx}`,
      role: (idx % 2 === 0 ? 'user' : 'assistant') as 'user' | 'assistant',
      text: `消息 ${idx}`,
      created_at: new Date(Date.UTC(2026, 0, 1, 0, idx)).toISOString(),
    }))
    const view = render(
      <MantineProvider>
        <Transcript messages={initialMessages} outbox={[]} session={runningSession} />
      </MantineProvider>,
    )
    expect(screen.getByRole('button', { name: '显示更早 10 条记录（共折叠 10 条）' })).toBeInTheDocument()
    expect(screen.getByTestId('message-m-10')).toBeInTheDocument()

    const appended = [
      ...initialMessages,
      { ...message, id: 'm-70', role: 'user' as const, text: '新发送的消息', created_at: '2026-01-01T02:00:00Z' },
      { ...message, id: 'm-71', role: 'assistant' as const, text: '最新流式输出', created_at: '2026-01-01T02:01:00Z' },
    ]
    view.rerender(
      <MantineProvider>
        <Transcript messages={appended} outbox={[]} session={runningSession} />
      </MantineProvider>,
    )
    // Appending must NOT evict m-10 from the top or bump folded count from 10 -> 12
    expect(screen.getByRole('button', { name: '显示更早 10 条记录（共折叠 10 条）' })).toBeInTheDocument()
    expect(screen.getByTestId('message-m-10')).toBeInTheDocument()
    expect(screen.getByTestId('message-m-71')).toBeInTheDocument()
  })

  it('does not yank a user back to the bottom when an acknowledged outbox entry clears', async () => {
    const view = render(
      <MantineProvider>
        <Transcript messages={[message]} outbox={[outbound]} session={runningSession} />
      </MantineProvider>,
    )
    const transcript = screen.getByRole('log')
    let currentTop = 900
    Object.defineProperties(transcript, {
      clientHeight: { configurable: true, value: 100 },
      scrollHeight: { configurable: true, value: 1000 },
      scrollTop: {
        configurable: true,
        get: () => currentTop,
        set: (value: number) => { currentTop = value },
      },
    })
    Object.defineProperty(transcript, 'scrollTo', {
      configurable: true,
      value: vi.fn(({ top }: { top: number }) => { currentTop = top }),
    })

    await new Promise<void>(resolve => requestAnimationFrame(() => resolve()))
    currentTop = 100
    fireEvent.scroll(transcript)
    await waitFor(() => expect(screen.getByTestId('return-bottom')).toBeInTheDocument())

    // Outbox clears once server acknowledges the command — user's manual scroll position must remain paused
    view.rerender(
      <MantineProvider>
        <Transcript messages={[message]} outbox={[]} session={runningSession} />
      </MantineProvider>,
    )
    expect(screen.getByTestId('return-bottom')).toBeInTheDocument()
    expect(currentTop).toBe(100)
  })

  it('immediately pauses follow on upward mouse wheel without bouncing back during initial ticks or streaming', async () => {
    const view = render(
      <MantineProvider>
        <Transcript messages={[message]} outbox={[]} session={runningSession} />
      </MantineProvider>,
    )
    const transcript = screen.getByRole('log')
    let currentHeight = 1000
    let currentTop = 900
    Object.defineProperties(transcript, {
      clientHeight: { configurable: true, get: () => 100 },
      scrollHeight: { configurable: true, get: () => currentHeight },
      scrollTop: {
        configurable: true,
        get: () => currentTop,
        set: (value: number) => { currentTop = value },
      },
    })
    const scrollTo = vi.fn(({ top }: { top: number }) => { currentTop = Math.max(0, top - 100) })
    Object.defineProperty(transcript, 'scrollTo', { configurable: true, value: scrollTo })

    await new Promise<void>(resolve => requestAnimationFrame(() => resolve()))
    scrollTo.mockClear()

    // User flicks mouse wheel upward from the bottom; first smooth-scroll frame only moves 15px (< 48px threshold).
    fireEvent.wheel(transcript, { deltaY: -100 })
    currentTop = 885
    fireEvent.scroll(transcript)

    await waitFor(() => expect(screen.getByTestId('return-bottom')).toBeInTheDocument())
    expect(scrollTo).not.toHaveBeenCalled()

    // Streaming delta arrives right after upward wheel — must NOT yank/bounce the user back to bottom!
    currentHeight = 1200
    view.rerender(
      <MantineProvider>
        <Transcript
          messages={[{ ...message, text: `${message.text}\n新增流式输出内容` }]}
          outbox={[]}
          session={runningSession}
        />
      </MantineProvider>,
    )
    expect(screen.getByTestId('return-bottom')).toBeInTheDocument()
    expect(scrollTo).not.toHaveBeenCalled()
    expect(currentTop).toBe(885)

    // Rolling wheel back downward to the bottom smoothly re-engages bottom follow.
    fireEvent.wheel(transcript, { deltaY: 120 })
    currentTop = 1080
    fireEvent.scroll(transcript)
    await waitFor(() => expect(screen.queryByTestId('return-bottom')).not.toBeInTheDocument())
  })

  it('does not show return-bottom button on upward wheel when transcript does not overflow', () => {
    render(
      <MantineProvider>
        <Transcript messages={[message]} outbox={[]} session={runningSession} />
      </MantineProvider>,
    )
    const transcript = screen.getByRole('log')
    Object.defineProperties(transcript, {
      clientHeight: { configurable: true, value: 600 },
      scrollHeight: { configurable: true, value: 400 },
      scrollTop: { configurable: true, writable: true, value: 0 },
    })

    fireEvent.wheel(transcript, { deltaY: -100 })
    expect(screen.queryByTestId('return-bottom')).not.toBeInTheDocument()
  })

  it('hides HandoffSummaryCard while loading, unresolved, or after multiple turns, and keeps it for fresh/first-turn handoffs', () => {
    const handoffSession: Session = {
      ...runningSession,
      status: 'idle',
      handoff_from_agent_id: 'codex-1',
      handoff_from_session_id: 'src-1',
      handoff_context: 'native-handoff-v1:{"agent_id":"codex-1","session_id":"src-1"}',
      handoff_context_consumed: true,
    }
    const pendingSession: Session = { ...handoffSession, handoff_context_consumed: false }
    const oneTurn: Message[] = [
      { ...message, id: 'u-1', role: 'user', text: '第一轮问题' },
      { ...message, id: 'a-1', role: 'assistant', text: '第一轮回复' },
    ]
    const multiTurn: Message[] = [
      ...oneTurn,
      { ...message, id: 'u-2', role: 'user', text: '第二轮问题' },
      { ...message, id: 'a-2', role: 'assistant', text: '第二轮回复' },
    ]

    // 1. While initial history is loading or unresolved, never flash HandoffSummaryCard.
    const view = render(
      <MantineProvider>
        <Transcript session={handoffSession} messages={[]} outbox={[]} loading />
      </MantineProvider>,
    )
    expect(screen.getByText('正在读取会话记录…')).toBeInTheDocument()
    expect(screen.queryByTestId('handoff-summary-card')).not.toBeInTheDocument()

    view.rerender(
      <MantineProvider>
        <Transcript session={handoffSession} messages={[]} outbox={[]} historyResolved={false} />
      </MantineProvider>,
    )
    expect(screen.queryByTestId('handoff-summary-card')).not.toBeInTheDocument()

    // 2. Consumed session with empty messages or multiple user turns must not render HandoffSummaryCard even when hasMoreHistory=false.
    view.rerender(
      <MantineProvider>
        <Transcript session={handoffSession} messages={[]} outbox={[]} />
      </MantineProvider>,
    )
    expect(screen.queryByTestId('handoff-summary-card')).not.toBeInTheDocument()

    view.rerender(
      <MantineProvider>
        <Transcript session={handoffSession} messages={multiTurn} outbox={[]} />
      </MantineProvider>,
    )
    expect(screen.queryByTestId('handoff-summary-card')).not.toBeInTheDocument()

    // 3. Fresh pending handoff session (0 turns) or first turn after injection still shows it.
    view.rerender(
      <MantineProvider>
        <Transcript session={pendingSession} messages={[]} outbox={[]} />
      </MantineProvider>,
    )
    expect(screen.getByTestId('handoff-summary-card')).toBeInTheDocument()

    view.rerender(
      <MantineProvider>
        <Transcript session={handoffSession} messages={oneTurn} outbox={[]} />
      </MantineProvider>,
    )
    expect(screen.getByTestId('handoff-summary-card')).toBeInTheDocument()
  })
})
