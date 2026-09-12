import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { MobileTranscript } from './MobileTranscript'
import type { Approval, Message } from '../../domain/types'

afterEach(cleanup)
const row: Message = { id: 'm', agent_id: 'a', session_id: 's', role: 'assistant', kind: 'thinking', text: 'large hidden reasoning', attachments: [], command_id: null, tool: null, created_at: '2026-01-01T00:00:00Z' }
const pendingApproval: Approval = {
  id: 'approval-m1',
  agent_id: 'a',
  session_id: 's',
  title: 'Codex 请求执行授权',
  detail: '/usr/bin/zsh -lc "ls -la"',
  state: 'pending',
  target_id: 'call-m1',
  data: {},
}
it('keeps live reasoning packs closed and does not mount their expensive body', () => {
  const view = render(<MobileTranscript messages={[row]} busy hasOlder={false} loadingOlder={false} loadOlder={vi.fn()} onMessageAction={vi.fn()} onImage={vi.fn()} onSwipe={vi.fn()} />)
  expect(view.container.querySelector('.m-think-pack')).not.toHaveAttribute('open')
  expect(screen.queryByText('large hidden reasoning')).not.toBeInTheDocument()
})
it('loads once per upward wheel gesture even when two messages fit the viewport', () => {
  const load = vi.fn(() => new Promise<void>(() => {}))
  render(<MobileTranscript messages={[]} busy={false} hasOlder loadingOlder={false} loadOlder={load} onMessageAction={vi.fn()} onImage={vi.fn()} onSwipe={vi.fn()} />)
  expect(load).not.toHaveBeenCalled()
  fireEvent.wheel(screen.getByRole('log'), { deltaY: -100 })
  fireEvent.wheel(screen.getByRole('log'), { deltaY: -100 })
  expect(load).toHaveBeenCalledTimes(1)
})
it('keeps diagonal inner swipes available for open-session navigation', () => {
  const onSwipe = vi.fn()
  render(<MobileTranscript messages={[]} busy={false} hasOlder={false} loadingOlder={false} loadOlder={vi.fn()} onMessageAction={vi.fn()} onImage={vi.fn()} onSwipe={onSwipe} />)
  const log = screen.getByRole('log')
  fireEvent.touchStart(log, { touches: [{ clientX: 260, clientY: 300 }] })
  fireEvent.touchEnd(log, { changedTouches: [{ clientX: 140, clientY: 360 }] })
  expect(onSwipe).toHaveBeenCalledWith({ direction: 1, vertical: 1, cut: 'next-down' })
})
it('leaves a right swipe that starts at the bezel for the session drawer', () => {
  const onSwipe = vi.fn()
  render(<MobileTranscript messages={[]} busy={false} hasOlder={false} loadingOlder={false} loadOlder={vi.fn()} onMessageAction={vi.fn()} onImage={vi.fn()} onSwipe={onSwipe} />)
  const log = screen.getByRole('log')
  fireEvent.touchStart(log, { touches: [{ clientX: 8, clientY: 300 }] })
  fireEvent.touchEnd(log, { changedTouches: [{ clientX: 120, clientY: 306 }] })
  expect(onSwipe).not.toHaveBeenCalled()
})

it('does not let a diagonal bezel gesture switch sessions', () => {
  const onSwipe = vi.fn()
  render(<MobileTranscript messages={[]} busy={false} hasOlder={false} loadingOlder={false} loadOlder={vi.fn()} onMessageAction={vi.fn()} onImage={vi.fn()} onSwipe={onSwipe} />)
  const log = screen.getByRole('log')
  fireEvent.touchStart(log, { touches: [{ clientX: 32, clientY: 300 }] })
  fireEvent.touchEnd(log, { changedTouches: [{ clientX: 180, clientY: 380 }] })
  expect(onSwipe).not.toHaveBeenCalled()
})

it('renders pending approvals inline and invokes approval callbacks', () => {
  const onApproval = vi.fn()
  render(
    <MobileTranscript
      messages={[row]}
      approvals={[pendingApproval]}
      onApproval={onApproval}
      busy={false}
      hasOlder={false}
      loadingOlder={false}
      loadOlder={vi.fn()}
      onMessageAction={vi.fn()}
      onImage={vi.fn()}
      onSwipe={vi.fn()}
    />,
  )
  expect(screen.getByRole('region', { name: '对话待处理审批' })).toBeInTheDocument()
  expect(screen.getByText('Codex 请求执行授权')).toBeInTheDocument()
  expect(screen.getByText(/ls -la/)).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '允许本次' }))
  expect(onApproval).toHaveBeenCalledWith(pendingApproval, 'approve')
  fireEvent.click(screen.getByRole('button', { name: '拒绝' }))
  expect(onApproval).toHaveBeenCalledWith(pendingApproval, 'cancel')
})

it('renders a native image reference once when the same image is already attached', () => {
  render(
    <MobileTranscript
      messages={[
        {
          ...row,
          kind: 'message',
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
      busy={false}
      hasOlder={false}
      loadingOlder={false}
      loadOlder={vi.fn()}
      onMessageAction={vi.fn()}
      onImage={vi.fn()}
      onSwipe={vi.fn()}
    />,
  )

  expect(screen.getAllByAltText('upload.png')).toHaveLength(1)
  expect(screen.queryByText(/@image:/)).not.toBeInTheDocument()
})
