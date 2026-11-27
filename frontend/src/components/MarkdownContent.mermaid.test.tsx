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
import { extractSvgIntrinsicSize } from './PanZoomCanvas'

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

  it('opens Mermaid diagrams in the Sidecar canvas without adding canvas buttons to plain code blocks', async () => {
    useSidecarStore.setState({ tabs: [], activeTabId: '', isOpen: false })
    const code = '```ts\nconst a = 1\nconst b = 2\nconst c = 3\nconst d = 4\nconst e = 5\nconst f = 6\n```'
    const { container } = render(
      <MantineProvider>
        <MarkdownContent value={`\`\`\`mermaid\ngraph LR\nA --> B\n\`\`\`\n\n${code}`} />
      </MantineProvider>,
    )

    await waitFor(() => expect(screen.getByLabelText('rendered-flowchart')).toBeInTheDocument())
    fireEvent.click(container.querySelector('.markdown-mermaid-canvas-btn') as HTMLButtonElement)
    expect(screen.queryByRole('button', { name: '在侧边画布查看代码' })).not.toBeInTheDocument()

    const state = useSidecarStore.getState()
    expect(state.isOpen).toBe(true)
    expect(state.tabs.some(t => t.type === 'artifact' && t.viewerId === 'mermaid-viewer' && t.artifact?.metadata?.inlineContent === 'graph LR\nA --> B')).toBe(true)
  })

  it('opens an interactive zoom/pan lightbox modal when clicking the Mermaid diagram itself', async () => {
    render(
      <MantineProvider>
        <MarkdownContent value={'```mermaid\ngraph LR\nA --> B\n```'} />
      </MantineProvider>,
    )

    const stageBtn = await screen.findByRole('button', { name: '点击放大查看 Mermaid 图表' })
    fireEvent.click(stageBtn)

    const canvas = await screen.findByLabelText('Mermaid 架构图缩放画布')
    expect(canvas).toBeInTheDocument()

    // Wheel zoom in
    fireEvent.wheel(canvas, { deltaY: -120, clientX: 200, clientY: 200 })
    expect(Number(canvas.getAttribute('data-zoom-scale'))).toBeGreaterThan(1)

    // Pointer drag pan
    fireEvent.pointerDown(canvas, { pointerId: 1, pointerType: 'mouse', button: 0, clientX: 100, clientY: 100 })
    fireEvent.pointerMove(canvas, { pointerId: 1, pointerType: 'mouse', clientX: 165, clientY: 140 })
    fireEvent.pointerUp(canvas, { pointerId: 1, pointerType: 'mouse', clientX: 165, clientY: 140 })
    expect(Number(canvas.getAttribute('data-pan-x'))).not.toBe(0)
    expect(Number(canvas.getAttribute('data-pan-y'))).not.toBe(0)
  })

  it('extracts SVG intrinsic viewBox dimensions for crisp vector layout scaling', () => {
    const size = extractSvgIntrinsicSize(
      '<svg id="mermaid-1" width="100%" style="max-width: 1942.5px;" viewBox="0 0 1942.5 2418"></svg>',
    )
    expect(size).toEqual({ width: 1942.5, height: 2418 })
  })
})
