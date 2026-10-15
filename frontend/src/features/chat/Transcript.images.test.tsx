import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { Transcript } from './Transcript'
import type { Message } from '../../domain/types'

afterEach(cleanup)
const image = 'data:image/png;base64,iVBORw0KGgo='
const message: Message = { id: 'native-user', agent_id: 'codex', session_id: 'thread', role: 'user', kind: 'message', text: '', attachments: [{ id: 'image', name: 'image.png', media_type: 'image/png', url: image }], created_at: '2026-10-14T00:00:00Z', command_id: null, tool: null }

it('renders native inline image-only messages and opens the image gallery', () => {
  const onGalleryClick = vi.fn()
  render(<MantineProvider><Transcript messages={[message]} outbox={[]} onGalleryClick={onGalleryClick} /></MantineProvider>)
  expect(screen.getByAltText('image.png')).toHaveAttribute('src', image)
  fireEvent.click(screen.getByRole('button', { name: '查看图片：image.png' }))
  expect(onGalleryClick).toHaveBeenCalledWith(image, [image])
})

it('keeps local native images scoped to their session', () => {
  const url = '/api/v1/files/raw?path=C%3A%5Cshots%5Cone.png&session_id=thread'
  render(<MantineProvider><Transcript messages={[{ ...message, attachments: [{ ...message.attachments[0], url }] }]} outbox={[]} /></MantineProvider>)
  expect(screen.getByAltText('image.png')).toHaveAttribute('src', url)
})

it.each(['data:text/html;base64,PHNjcmlwdD4=', 'data:image/svg+xml;base64,PHN2Zz4=', 'javascript:alert(1)', 'file:///C:/private.png', 'https://outside.example/private.png'])('does not load unsafe attachment URL %s', (url) => {
  render(<MantineProvider><Transcript messages={[{ ...message, attachments: [{ ...message.attachments[0], url }] }]} outbox={[]} /></MantineProvider>)
  expect(screen.queryByRole('img')).not.toBeInTheDocument()
})
