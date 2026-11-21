import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { StrictMode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useSidecarStore } from '../features/sidecar/sidecarStore'

vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn(async () => ({ svg: '<svg aria-label="rendered-flowchart"></svg>' })),
  },
}))

import { MarkdownContent } from './MarkdownContent'

describe('MarkdownContent Mermaid diagrams', () => {
  afterEach(() => cleanup())

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

  it('opens Mermaid diagrams and multi-line code blocks directly in the Sidecar canvas', async () => {
    useSidecarStore.setState({ tabs: [], activeTabId: '', isOpen: false })
    const code = '```ts\nconst a = 1\nconst b = 2\nconst c = 3\nconst d = 4\nconst e = 5\nconst f = 6\n```'
    const { container } = render(
      <MantineProvider>
        <MarkdownContent value={`\`\`\`mermaid\ngraph LR\nA --> B\n\`\`\`\n\n${code}`} />
      </MantineProvider>,
    )

    await waitFor(() => expect(screen.getByLabelText('rendered-flowchart')).toBeInTheDocument())
    fireEvent.click(container.querySelector('.markdown-mermaid-canvas-btn') as HTMLButtonElement)
    fireEvent.click(screen.getByRole('button', { name: '在侧边画布查看代码' }))

    const state = useSidecarStore.getState()
    expect(state.isOpen).toBe(true)
    expect(state.tabs.some(t => t.type === 'artifact' && t.viewerId === 'mermaid-viewer' && t.artifact?.metadata?.inlineContent === 'graph LR\nA --> B')).toBe(true)
    expect(state.tabs.some(t => t.type === 'artifact' && t.viewerId === 'monaco-viewer' && String(t.artifact?.metadata?.inlineContent || '').includes('const f = 6'))).toBe(true)
  })
})
