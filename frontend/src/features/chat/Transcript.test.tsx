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
    await new Promise((resolve) => setTimeout(resolve, 0))

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
    await new Promise((resolve) => setTimeout(resolve, 0))
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
