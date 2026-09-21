import { MantineProvider } from '@mantine/core'
import { render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn(async () => ({ svg: '<svg aria-label="rendered-flowchart"></svg>' })),
  },
}))

import { MarkdownContent } from './MarkdownContent'

describe('MarkdownContent Mermaid diagrams', () => {
  it('renders an unlabelled flowchart code block as a diagram', async () => {
    render(
      <MantineProvider>
        <MarkdownContent value={'```\nflowchart TB\n  A[监控对象] --> B[数据采集]\n```'} />
      </MantineProvider>,
    )

    await waitFor(() => expect(screen.getByLabelText('rendered-flowchart')).toBeInTheDocument())
    expect(screen.queryByText('flowchart TB')).not.toBeInTheDocument()
  })
})
