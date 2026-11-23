import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { MobileMarkdown } from './MobileMarkdown'

afterEach(() => {
  cleanup()
  localStorage.clear()
  vi.useRealTimers()
  vi.restoreAllMocks()
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
