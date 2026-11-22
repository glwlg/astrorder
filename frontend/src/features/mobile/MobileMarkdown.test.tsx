import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { MobileMarkdown } from './MobileMarkdown'

afterEach(() => {
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
