import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { MarkdownContent } from './MarkdownContent'

describe('MarkdownContent Auto-Link Artifacts', () => {
  it('automatically detects plain text .drawio filename and renders previewable link', () => {
    const text = '方案一交付物：集团对接_方案一_直连中台结果库.drawio'
    render(
      <MantineProvider>
        <MarkdownContent value={text} />
      </MantineProvider>,
    )

    const btn = screen.getByRole('button', { name: /集团对接_方案一_直连中台结果库\.drawio/ })
    expect(btn).toBeInTheDocument()
    expect(btn).toHaveClass('markdown-file-link')
  })

  it('renders markdown local image link as preview button', () => {
    const text = '高清预览图：[集团对接_方案一.png](C:/Users/test/集团对接_方案一.png)'
    render(
      <MantineProvider>
        <MarkdownContent value={text} />
      </MantineProvider>,
    )

    const btn = screen.getByRole('button', { name: /集团对接_方案一\.png/ })
    expect(btn).toBeInTheDocument()
    expect(btn).toHaveClass('markdown-image-link')
  })
})
