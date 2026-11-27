import { MantineProvider } from '@mantine/core'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

const { renderMermaidMock } = vi.hoisted(() => ({
  renderMermaidMock: vi.fn(async (code: string) => ({
    svg: `<svg aria-label="mobile-mermaid-svg" viewBox="0 0 800 400" data-code-len="${code.length}"><rect width="800" height="400" /></svg>`,
  })),
}))

vi.mock('../../components/renderMermaid', () => ({
  renderMermaid: renderMermaidMock,
}))

import { clearMobileMermaidCache, MobileMarkdown } from './MobileMarkdown'

afterEach(() => {
  cleanup()
  clearMobileMermaidCache()
  document.documentElement.removeAttribute('data-mantine-color-scheme')
  localStorage.clear()
  vi.useRealTimers()
  vi.clearAllMocks()
})

it('opens Windows file links without navigating and serves local images through the session', () => {
  const onFileClick = vi.fn()
  render(
    <MobileMarkdown
      value={'[周报](C:/Users/fixture/output/周报_20260929.html)\n\n![图](output/chart.png)'}
      workspace="C:/Users/fixture"
      sessionId="session-1"
      onFileClick={onFileClick}
    />,
  )
  const link = screen.getByRole('link', { name: '周报' })
  expect(link).toHaveAttribute('href', expect.stringContaining('/api/v1/files/raw?'))
  fireEvent.click(link)
  expect(onFileClick).toHaveBeenCalledWith('C:/Users/fixture/output/周报_20260929.html')
  expect(screen.getByRole('img', { name: '图' })).toHaveAttribute(
    'src',
    '/api/v1/files/raw?path=C%3A%2FUsers%2Ffixture%2Foutput%2Fchart.png&session_id=session-1',
  )
})

it('opens local image links in the popup image preview when onImageClick is provided, while keeping code/html links in workbench', () => {
  const onFileClick = vi.fn()
  const onImageClick = vi.fn()
  render(
    <MobileMarkdown
      value={
        '[03-drill-arena.png](file:///home/luwei/workspace/OpsCore/docs/designs/security-drill/03-drill-arena.png)\n\n'
        + '[03-drill-arena.html](file:///home/luwei/workspace/OpsCore/docs/designs/security-drill/03-drill-arena.html)\n\n'
        + '[arch.drawio.svg](file:///home/luwei/workspace/OpsCore/docs/arch.drawio.svg)\n\n'
        + '[main.go](file:///home/luwei/workspace/OpsCore/main.go#L12-L20)\n\n'
        + '![preview](file:///home/luwei/workspace/OpsCore/docs/designs/01%20chart.png)'
      }
      workspace="/home/luwei/workspace/OpsCore"
      sessionId="session-posix"
      connectionId="ssh-1"
      onFileClick={onFileClick}
      onImageClick={onImageClick}
    />,
  )

  const pngLink = screen.getByRole('link', { name: '03-drill-arena.png' })
  const expectedPngUrl = '/api/v1/files/raw?path=%2Fhome%2Fluwei%2Fworkspace%2FOpsCore%2Fdocs%2Fdesigns%2Fsecurity-drill%2F03-drill-arena.png&session_id=session-posix&connection_id=ssh-1'
  const expectedInlineUrl = '/api/v1/files/raw?path=%2Fhome%2Fluwei%2Fworkspace%2FOpsCore%2Fdocs%2Fdesigns%2F01+chart.png&session_id=session-posix&connection_id=ssh-1'
  expect(pngLink).toHaveAttribute('href', expectedPngUrl)
  fireEvent.click(pngLink)
  expect(onImageClick).toHaveBeenCalledWith(expectedPngUrl, [expectedPngUrl, expectedInlineUrl])
  expect(onFileClick).not.toHaveBeenCalled()

  fireEvent.click(screen.getByRole('link', { name: '03-drill-arena.html' }))
  expect(onFileClick).toHaveBeenCalledWith('/home/luwei/workspace/OpsCore/docs/designs/security-drill/03-drill-arena.html')

  fireEvent.click(screen.getByRole('link', { name: 'arch.drawio.svg' }))
  expect(onFileClick).toHaveBeenCalledWith('/home/luwei/workspace/OpsCore/docs/arch.drawio.svg')

  fireEvent.click(screen.getByRole('link', { name: 'main.go' }))
  expect(onFileClick).toHaveBeenCalledWith('/home/luwei/workspace/OpsCore/main.go')

  expect(screen.getByRole('img', { name: 'preview' })).toHaveAttribute(
    'src',
    '/api/v1/files/raw?path=%2Fhome%2Fluwei%2Fworkspace%2FOpsCore%2Fdocs%2Fdesigns%2F01+chart.png&session_id=session-posix&connection_id=ssh-1',
  )
})

it('handles Windows file:/// drive URIs cleanly', () => {
  const onFileClick = vi.fn()
  render(
    <MobileMarkdown
      value={'[图](file:///C:/Users/fixture/pic.png)'}
      workspace="C:/Users/fixture"
      sessionId="session-win"
      onFileClick={onFileClick}
    />,
  )
  fireEvent.click(screen.getByRole('link', { name: '图' }))
  expect(onFileClick).toHaveBeenCalledWith('C:/Users/fixture/pic.png')
})

it('resets code block copied feedback back to copy after a short delay', async () => {
  vi.useFakeTimers()
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
  render(<MobileMarkdown value={'```ts\nconst ok = true\n```'} />)
  const btn = screen.getByRole('button', { name: '复制' })
  await act(async () => {
    fireEvent.click(btn)
  })
  expect(writeText).toHaveBeenCalledWith('const ok = true\n')
  expect(screen.getByRole('button', { name: '已复制' })).toBeInTheDocument()
  act(() => {
    vi.advanceTimersByTime(1900)
  })
  expect(screen.getByRole('button', { name: '复制' })).toBeInTheDocument()
})

it('opens local video markdown links in the mobile video player callback', () => {
  const onVideoClick = vi.fn()
  const onFileClick = vi.fn()
  render(
    <MobileMarkdown
      value={'成片：[ai-ops-platform.mp4](films/ai-ops-platform/ai-ops-platform.mp4)'}
      workspace="/workspace/OpsCore"
      sessionId="session-vid"
      onVideoClick={onVideoClick}
      onFileClick={onFileClick}
    />,
  )
  fireEvent.click(screen.getByRole('link', { name: 'ai-ops-platform.mp4' }))
  expect(onVideoClick).toHaveBeenCalledWith(
    expect.stringContaining('/api/v1/files/raw?path=%2Fworkspace%2FOpsCore%2Ffilms%2Fai-ops-platform%2Fai-ops-platform.mp4'),
    'ai-ops-platform.mp4',
  )
  expect(onFileClick).not.toHaveBeenCalled()
})

it('renders Mermaid code blocks as interactive SVG cards with source toggle and copy source', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })

  const { container } = render(
    <MobileMarkdown value={'```mermaid\ngraph LR\n  A[客户端] --> B[网关]\n```'} />,
  )

  const svg = await screen.findByLabelText('mobile-mermaid-svg')
  expect(svg).toBeInTheDocument()
  const card = container.querySelector('.m-mermaid-card')
  expect(card).toBeTruthy()
  const stage = screen.getByRole('button', { name: '点击全屏放大查看 Mermaid 图表' })
  expect(stage).toHaveAttribute('data-intrinsic-width', '800')
  expect(stage).toHaveAttribute('data-intrinsic-height', '400')

  // Toggle source view and back to diagram without losing SVG
  fireEvent.click(screen.getByRole('button', { name: '源码' }))
  expect(screen.getByText(/A\[客户端\] --> B\[网关\]/)).toBeInTheDocument()
  expect(screen.queryByLabelText('mobile-mermaid-svg')).not.toBeInTheDocument()

  fireEvent.click(screen.getByRole('button', { name: '图表' }))
  expect(screen.getByLabelText('mobile-mermaid-svg')).toBeInTheDocument()

  // Copy raw Mermaid source
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: '复制源码' }))
  })
  expect(writeText).toHaveBeenCalledWith('graph LR\n  A[客户端] --> B[网关]')
  expect(screen.getByRole('button', { name: '已复制' })).toBeInTheDocument()
})

it('opens DiagramLightboxModal on stage tap and supports lossless vector touch pan, pinch, double-tap, zoom buttons, and close', async () => {
  render(
    <MantineProvider>
      <MobileMarkdown value={'```mermaid\nflowchart TD\n  UI --> API\n```'} />
    </MantineProvider>,
  )

  const stage = await screen.findByRole('button', { name: '点击全屏放大查看 Mermaid 图表' })
  fireEvent.click(stage)

  const canvas = await screen.findByLabelText('Mermaid 架构图缩放画布')
  expect(canvas).toBeInTheDocument()

  // Verify lossless layout-dimension vector scaling (no CSS scale() transform on stage when viewBox is known)
  const panStage = canvas.querySelector('.astr-panzoom-stage') as HTMLElement
  expect(panStage).toBeTruthy()
  expect(panStage.style.width).toBe('800px')
  expect(panStage.style.height).toBe('400px')
  expect(panStage.style.transform).not.toContain('scale(')

  // Zoom button controls
  fireEvent.click(screen.getByRole('button', { name: '放大画布' }))
  const zoomedInScale = Number(canvas.getAttribute('data-zoom-scale'))
  expect(zoomedInScale).toBeGreaterThan(1)
  expect(panStage.style.width).toBe(`${Math.round(800 * zoomedInScale)}px`)
  expect(panStage.style.transform).not.toContain('scale(')

  // Reset back to 100%
  fireEvent.click(screen.getByRole('button', { name: '复位视角' }))
  expect(Number(canvas.getAttribute('data-zoom-scale'))).toBeCloseTo(1, 2)

  // Single-finger touch drag pan
  fireEvent.pointerDown(canvas, { pointerId: 1, pointerType: 'touch', clientX: 120, clientY: 160 })
  fireEvent.pointerMove(canvas, { pointerId: 1, pointerType: 'touch', clientX: 185, clientY: 210 })
  fireEvent.pointerUp(canvas, { pointerId: 1, pointerType: 'touch', clientX: 185, clientY: 210 })
  expect(Number(canvas.getAttribute('data-pan-x'))).toBe(65)
  expect(Number(canvas.getAttribute('data-pan-y'))).toBe(50)

  // Two-finger pinch zoom
  fireEvent.click(screen.getByRole('button', { name: '复位视角' }))
  fireEvent.pointerDown(canvas, { pointerId: 10, pointerType: 'touch', clientX: 100, clientY: 200 })
  fireEvent.pointerDown(canvas, { pointerId: 11, pointerType: 'touch', clientX: 200, clientY: 200 })
  fireEvent.pointerMove(canvas, { pointerId: 10, pointerType: 'touch', clientX: 50, clientY: 200 })
  fireEvent.pointerMove(canvas, { pointerId: 11, pointerType: 'touch', clientX: 250, clientY: 200 })
  fireEvent.pointerUp(canvas, { pointerId: 10, pointerType: 'touch', clientX: 50, clientY: 200 })
  fireEvent.pointerUp(canvas, { pointerId: 11, pointerType: 'touch', clientX: 250, clientY: 200 })
  expect(Number(canvas.getAttribute('data-zoom-scale'))).toBeCloseTo(2, 1)

  // Touch double-tap toggles between fit (1.0 in JSDOM) and 2.2x zoom
  fireEvent.click(screen.getByRole('button', { name: '适应画布' }))
  expect(Number(canvas.getAttribute('data-zoom-scale'))).toBeCloseTo(1, 2)
  fireEvent.pointerDown(canvas, { pointerId: 20, pointerType: 'touch', clientX: 150, clientY: 150 })
  fireEvent.pointerUp(canvas, { pointerId: 20, pointerType: 'touch', clientX: 150, clientY: 150 })
  fireEvent.pointerDown(canvas, { pointerId: 21, pointerType: 'touch', clientX: 152, clientY: 151 })
  fireEvent.pointerUp(canvas, { pointerId: 21, pointerType: 'touch', clientX: 152, clientY: 151 })
  expect(Number(canvas.getAttribute('data-zoom-scale'))).toBeCloseTo(2.2, 1)

  // Second double-tap returns cleanly to fit-to-screen
  fireEvent.pointerDown(canvas, { pointerId: 22, pointerType: 'touch', clientX: 150, clientY: 150 })
  fireEvent.pointerUp(canvas, { pointerId: 22, pointerType: 'touch', clientX: 150, clientY: 150 })
  fireEvent.pointerDown(canvas, { pointerId: 23, pointerType: 'touch', clientX: 150, clientY: 150 })
  fireEvent.pointerUp(canvas, { pointerId: 23, pointerType: 'touch', clientX: 150, clientY: 150 })
  expect(Number(canvas.getAttribute('data-zoom-scale'))).toBeCloseTo(1, 2)

  // Close lightbox
  fireEvent.click(screen.getByRole('button', { name: '关闭图表预览' }))
  await waitFor(() => {
    expect(screen.queryByLabelText('Mermaid 架构图缩放画布')).not.toBeInTheDocument()
  })

  // Also verify opening via the toolbar 全屏放大 button
  fireEvent.click(screen.getByRole('button', { name: '全屏放大' }))
  expect(await screen.findByLabelText('Mermaid 架构图缩放画布')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '关闭图表预览' }))
  await waitFor(() => {
    expect(screen.queryByLabelText('Mermaid 架构图缩放画布')).not.toBeInTheDocument()
  })
})

it('falls back to raw Mermaid source code when rendering fails and respects dark color scheme', async () => {
  document.documentElement.setAttribute('data-mantine-color-scheme', 'dark')
  render(<MobileMarkdown value={'```mermaid\ngraph TD\n  DarkA --> DarkB\n```'} />)
  await screen.findByLabelText('mobile-mermaid-svg')
  expect(renderMermaidMock).toHaveBeenCalledWith(expect.stringContaining('"theme": "dark"'))

  renderMermaidMock.mockRejectedValueOnce(new Error('syntax error')).mockRejectedValueOnce(new Error('syntax error'))
  cleanup()
  clearMobileMermaidCache()
  render(<MobileMarkdown value={'```mermaid\ninvalid syntax %%%\n```'} />)
  await screen.findByText('图表渲染失败，已回退显示源码')
  expect(screen.getByText('invalid syntax %%%')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '全屏放大' })).not.toBeInTheDocument()
})
