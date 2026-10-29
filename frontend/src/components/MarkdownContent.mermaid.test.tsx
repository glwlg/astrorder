import { MantineProvider } from '@mantine/core'
import { render, screen, waitFor } from '@testing-library/react'
import { StrictMode } from 'react'
import { describe, expect, it, vi } from 'vitest'

vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn(async () => ({ svg: '<svg aria-label="rendered-flowchart"></svg>' })),
  },
}))

import { MarkdownContent } from './MarkdownContent'

describe('MarkdownContent Mermaid diagrams', () => {
  it('preserves the diagram while surrounding text updates', async () => {
    const diagram = '```mermaid\ngraph LR\nA --> B\n```'
    const view = (suffix: string) => <MantineProvider><MarkdownContent value={`${diagram}\n\n${suffix}`} /></MantineProvider>
    const { rerender } = render(view('first'))
    const node = await screen.findByLabelText('rendered-flowchart')
    for (let i = 0; i < 20; i++) {
      rerender(view(`updated ${i}`))
      expect(screen.queryByText('正在渲染图表…')).toBeNull()
      expect(screen.getByLabelText('rendered-flowchart')).toBe(node)
    }
  })
  it('renders an unlabelled flowchart code block as a diagram', async () => {
    render(
      <StrictMode>
        <MantineProvider>
          <MarkdownContent value={'```\nflowchart TB\n  A[监控对象] --> B[数据采集]\n```'} />
        </MantineProvider>
      </StrictMode>,
    )

    await waitFor(() => expect(screen.getByLabelText('rendered-flowchart')).toBeInTheDocument())
    expect(screen.queryByText('flowchart TB')).not.toBeInTheDocument()
  })
})
