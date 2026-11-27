import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

vi.mock('../../components/renderMermaid', () => ({
  renderMermaid: vi.fn(async () => ({
    svg: '<svg aria-label="stable-mobile-mermaid" viewBox="0 0 600 300"><g /></svg>',
  })),
}))

import { clearMobileMermaidCache, MobileMarkdown } from './MobileMarkdown'

afterEach(() => {
  cleanup()
  clearMobileMermaidCache()
})

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

it('keeps the rendered Mermaid SVG DOM node without flicker while surrounding text streams', async () => {
  const diagram = '```mermaid\nflowchart LR\n  A[入口] --> B[网关]\n```'
  const { rerender } = render(<MobileMarkdown value={`${diagram}\n\n第一段`} sessionId="s" />)
  const svgNode = await screen.findByLabelText('stable-mobile-mermaid')
  for (let i = 0; i < 20; i++) {
    rerender(<MobileMarkdown value={`${diagram}\n\n${'流式输出'.repeat(i + 1)}`} sessionId="s" />)
    expect(screen.queryByText('正在渲染图表…')).not.toBeInTheDocument()
    expect(screen.getByLabelText('stable-mobile-mermaid')).toBe(svgNode)
  }
})
