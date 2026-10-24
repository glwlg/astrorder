import { render, screen, fireEvent } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { MobileMarkdown } from './MobileMarkdown'

it('keeps the image DOM node while typing updates callbacks and surrounding text', () => {
  const first = vi.fn(), next = vi.fn()
  const { rerender } = render(<MobileMarkdown value="![图](/api/v1/files/raw?path=test.png)" onImageClick={first} sessionId="s" />)
  const image = screen.getByRole('img')
  for (let i = 0; i < 20; i++) {
    rerender(<MobileMarkdown value={`![图](/api/v1/files/raw?path=test.png)\n\n${'字'.repeat(i)}`} onImageClick={url => next(url)} sessionId="s" />)
    expect(screen.getByRole('img')).toBe(image)
  }
  fireEvent.click(image)
  expect(next).toHaveBeenCalledWith('/api/v1/files/raw?path=test.png')
  expect(first).not.toHaveBeenCalled()
})
