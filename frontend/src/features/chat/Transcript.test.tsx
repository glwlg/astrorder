import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
afterEach(cleanup)
import type { Message } from '../../domain/types'
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

describe('transcript follow mode', () => {
  it('groups reasoning and tools into folds and hides empty assistant bubbles', () => {
    render(<MantineProvider><Transcript outbox={[]} messages={[
      { ...message, id: 'thinking', kind: 'thinking', text: '检查数据' },
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
})
