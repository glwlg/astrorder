import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { MobileTranscript } from './MobileTranscript'
import type { Message } from '../../domain/types'

afterEach(cleanup)
const row: Message = { id: 'm', agent_id: 'a', session_id: 's', role: 'assistant', kind: 'thinking', text: 'large hidden reasoning', attachments: [], command_id: null, tool: null, created_at: '2026-01-01T00:00:00Z' }
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
