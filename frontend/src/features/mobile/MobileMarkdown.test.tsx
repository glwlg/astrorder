import { fireEvent, render, screen } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { MobileMarkdown } from './MobileMarkdown'
import { MobileArtifactSheet } from './MobileArtifactSheet'

it('opens Windows file links without navigating and serves local images through the session', () => {
  const onFileClick = vi.fn()
  render(
    <MobileMarkdown
      value={'[周报](C:/Users/luwei/output/周报_20260929.html)\n\n![图](output/chart.png)'}
      workspace="C:/Users/luwei"
      sessionId="session-1"
      onFileClick={onFileClick}
    />,
  )
  const link = screen.getByRole('link', { name: '周报' })
  expect(link).toHaveAttribute('href', expect.stringContaining('/api/v1/files/raw?'))
  fireEvent.click(link)
  expect(onFileClick).toHaveBeenCalledWith('C:/Users/luwei/output/周报_20260929.html')
  expect(screen.getByRole('img', { name: '图' })).toHaveAttribute(
    'src',
    '/api/v1/files/raw?path=C%3A%2FUsers%2Fluwei%2Foutput%2Fchart.png&session_id=session-1',
  )
})

it('previews an HTML artifact inside the mobile sheet', () => {
  render(<MobileArtifactSheet path="C:/Users/luwei/output/周报_20260929.html" onClose={vi.fn()} />)
  const frame = screen.getByTitle('周报_20260929.html')
  expect(frame).toHaveAttribute('sandbox', 'allow-scripts')
  expect(frame).toHaveAttribute('src', expect.stringContaining('/api/v1/files/raw?path='))
})
