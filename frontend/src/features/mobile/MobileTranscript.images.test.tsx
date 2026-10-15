import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { MobileTranscript } from './MobileTranscript'
import type { Message } from '../../domain/types'

afterEach(cleanup)
const image = 'data:image/png;base64,iVBORw0KGgo='
const message: Message = { id: 'native-user', agent_id: 'codex', session_id: 'thread', role: 'user', kind: 'message', text: '', attachments: [{ id: 'image', name: 'image.png', media_type: 'image/png', url: image }], created_at: '2026-10-14T00:00:00Z', command_id: null, tool: null }
const props = { busy: false, hasOlder: false, loadingOlder: false, loadOlder: vi.fn(), onMessageAction: vi.fn(), onSwipe: vi.fn() }

it('opens native inline images on mobile', () => {
  const onImage = vi.fn()
  render(<MobileTranscript {...props} messages={[message]} onImage={onImage} />)
  expect(screen.getByAltText('image.png')).toHaveAttribute('src', image)
  fireEvent.click(screen.getByAltText('image.png'))
  expect(onImage).toHaveBeenCalledWith(image)
})

it.each(['data:text/html;base64,PHNjcmlwdD4=', 'data:image/svg+xml;base64,PHN2Zz4=', 'javascript:alert(1)', 'file:///C:/private.png', 'https://outside.example/private.png'])('does not load unsafe native image URL %s on mobile', (url) => {
  render(<MobileTranscript {...props} messages={[{ ...message, attachments: [{ ...message.attachments[0], url }] }]} onImage={vi.fn()} />)
  expect(screen.queryByRole('img')).not.toBeInTheDocument()
})
