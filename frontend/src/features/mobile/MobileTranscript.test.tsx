import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { MobileTranscript } from './MobileTranscript'
import type { Approval, Message } from '../../domain/types'

afterEach(cleanup)
it('splits 430 mobile activities into complete independent packs of at most 100', () => {
  const messages: Message[] = Array.from({ length: 430 }, (_, i) => ({ ...row, id: `tool-${i}`, role: 'tool', kind: 'tool', text: '', tool: { name: 'commandExecution', status: 'completed', arguments: { command: `echo ${i}` } } }))
  const props = { busy: true, hasOlder: false, loadingOlder: false, loadOlder: vi.fn(), onMessageAction: vi.fn(), onImage: vi.fn(), onSwipe: vi.fn() }
  const view = render(<MobileTranscript {...props} messages={messages} />)
  const packs = view.container.querySelectorAll('.m-think-pack')
  expect(packs).toHaveLength(5)
  expect(Array.from(packs).map(pack => pack.classList.contains('is-loading-active'))).toEqual([false, false, false, false, true])
  fireEvent.click(packs[0].querySelector('summary')!)
  expect(packs[0].querySelectorAll('.m-fold')).toHaveLength(100)
  expect(packs[1].querySelectorAll('.m-fold')).toHaveLength(0)
  view.rerender(<MobileTranscript {...props} messages={[...messages, { ...messages[0], id: 'tool-430' }]} />)
  expect(view.container.querySelector('.m-think-pack')).toHaveAttribute('open')
  expect(view.container.querySelectorAll('.m-think-pack')).toHaveLength(5)
})
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
it('hides empty user echoes on mobile and keeps attachment-only messages', () => {
  const input: Message = { ...row, id: 'optimistic-send', role: 'user', kind: 'message', text: '这个要怎么弄？', command_id: 'send' }
  const empty: Message = { ...input, id: 'native-user', text: '' }
  const attachment: Message = { ...input, id: 'attachment-user', text: '', attachments: [{ id: 'image', name: 'image.png', media_type: 'image/png', url: '/image.png' }] }
  const props = { busy: false, hasOlder: false, loadingOlder: false, loadOlder: vi.fn(), onMessageAction: vi.fn(), onImage: vi.fn(), onSwipe: vi.fn() }
  const view = render(<MobileTranscript {...props} messages={[input, empty, { ...empty, id: 'whitespace-user', text: ' \n ' }, attachment]} />)
  expect(view.container.querySelector('[data-message-id="native-user"]')).toBeNull()
  expect(view.container.querySelector('[data-message-id="whitespace-user"]')).toBeNull()
  expect(screen.getByText('这个要怎么弄？')).toBeInTheDocument()
  expect(screen.getByAltText('image.png')).toBeInTheDocument()
  view.rerender(<MobileTranscript {...props} messages={[{ ...empty, text: '这个要怎么弄？' }]} />)
  expect(view.container.querySelector('[data-message-id="native-user"]')).toHaveTextContent('这个要怎么弄？')
})

it('normalizes old native commentary and hides empty placeholders on mobile', () => {
  const view = render(<MobileTranscript messages={[
    { ...row, id: 'comment', kind: 'agentMessage' as Message['kind'], text: '我会只提交本次改动。' },
    { ...row, id: 'empty', kind: 'agentMessage' as Message['kind'], text: '' },
    { ...row, id: 'reason', kind: 'reasoning' as Message['kind'], text: '核对范围' },
    { ...row, id: 'empty-reason', kind: 'reasoning' as Message['kind'], text: '' },
  ]} busy={false} hasOlder={false} loadingOlder={false} loadOlder={vi.fn()} onMessageAction={vi.fn()} onImage={vi.fn()} onSwipe={vi.fn()} />)
  expect(screen.getByText('我会只提交本次改动。').closest('article')).toHaveClass('m-msg')
  expect(view.container.querySelectorAll('.m-msg')).toHaveLength(1)
  const pack = view.container.querySelector('.m-think-pack')!
  fireEvent.click(pack.querySelector('summary')!)
  expect(pack.querySelectorAll('.m-fold')).toHaveLength(1)
  fireEvent.click(pack.querySelector('.m-fold summary')!)
  expect(pack.querySelector('.m-thinking-content')).toHaveTextContent('核对范围')
  expect(screen.queryByText(/^tool$/)).not.toBeInTheDocument()
})

it('keeps each mobile tool pack between its surrounding commentary', () => {
  const text = { ...row, kind: 'message' as const }
  const tool: Message = { ...row, role: 'tool', kind: 'tool', tool: { name: 'commandExecution', status: 'completed', arguments: { command: 'git status' } } }
  const view = render(<MobileTranscript messages={[
    { ...text, id: 'intro', text: '第一段说明' },
    { ...tool, id: 'read', created_at: '2026-01-01T00:01:00Z' },
    { ...text, id: 'update', text: '第二段说明' },
    { ...tool, id: 'edit', created_at: '2026-01-01T00:02:00Z' },
    { ...text, id: 'answer', text: '最终答复' },
  ]} busy={false} hasOlder={false} loadingOlder={false} loadOlder={vi.fn()} onMessageAction={vi.fn()} onImage={vi.fn()} onSwipe={vi.fn()} />)
  const children = Array.from(view.container.querySelector('.m-transcript')!.children)
    .filter(node => node.classList.contains('m-msg') || node.classList.contains('m-think-pack'))
  expect(children.map(node => node.classList.contains('m-think-pack') ? 'tool-pack' : node.getAttribute('data-message-id')))
    .toEqual(['intro', 'tool-pack', 'update', 'tool-pack', 'answer'])
})

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

it('renders single tick when received and double ticks when accepted or replied', () => {
  const userMsgReceived: Message = { ...row, id: 'm-recv', role: 'user', kind: 'message', text: '文本一', command_id: 'cmd-recv' }
  const userMsgAccepted: Message = { ...row, id: 'm-acc', role: 'user', kind: 'message', text: '文本二', command_id: 'cmd-acc' }
  const commands: import('../../domain/types').Command[] = [
    { id: 'cmd-recv', session_id: 's', agent_id: 'a', action: 'send', state: 'received', text: '文本一', attachments: [], created_at: '2026-01-01T00:00:00Z', error: null },
    { id: 'cmd-acc', session_id: 's', agent_id: 'a', action: 'send', state: 'accepted', text: '文本二', attachments: [], created_at: '2026-01-01T00:00:00Z', error: null },
  ]
  render(
    <MobileTranscript
      messages={[userMsgReceived, userMsgAccepted]}
      commands={commands}
      busy={false}
      hasOlder={false}
      loadingOlder={false}
      loadOlder={vi.fn()}
      onMessageAction={vi.fn()}
      onImage={vi.fn()}
      onSwipe={vi.fn()}
    />,
  )
  expect(screen.getByText('文本一').closest('article')).toHaveTextContent('已发送')
  expect(screen.getByText('文本二').closest('article')).toHaveTextContent('已接收')
})
