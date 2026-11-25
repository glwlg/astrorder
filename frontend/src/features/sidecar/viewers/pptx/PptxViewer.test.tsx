import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../../../api/client'
import { artifactViewerRegistry } from '../../registry'
import { PptxViewer } from './PptxViewer'

describe('PptxViewer & registry integration', () => {
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  it('registers pptx-viewer for .pptx files', () => {
    const viewer = artifactViewerRegistry.findViewer({
      id: 'deck-1',
      sessionId: 's-1',
      agentId: 'codex',
      name: 'Q4汇报.pptx',
      path: '/docs/Q4汇报.pptx',
      kind: 'workspace_file',
      readUrl: '/api/v1/files/read?path=%2Fdocs%2FQ4.pptx',
      mediaType: 'application/vnd.openxmlformats-officedocument.presentationml.presentation',
      writable: false,
    })
    expect(viewer?.id).toBe('pptx-viewer')
  })

  it('renders slides, navigates between pages, shows notes, and quotes current slide to chat', async () => {
    vi.spyOn(api, 'previewPptx').mockResolvedValue({
      path: '/docs/Q4汇报.pptx',
      name: 'Q4汇报.pptx',
      title: 'Q4 季度总结',
      slide_count: 2,
      stats: { slide_count: 2, words: 45, tables: 1, images: 0 },
      slides: [
        {
          index: 1,
          title: 'Q4 季度总结',
          paragraphs: [{ text: '2026 年第四季度汇报' }],
          notes: '开场致谢并介绍背景',
        },
        {
          index: 2,
          title: '核心交付指标',
          paragraphs: [{ text: '办公套件三期上线' }],
          tables: [{ rows: [['模块', '状态'], ['PPT 导出', '已完成']] }],
          notes: '重点展示 PPT 与会议闭环',
        },
      ],
    })

    const quoted: string[] = []
    const onQuote = (e: Event) => {
      quoted.push((e as CustomEvent<{ text: string }>).detail.text)
    }
    window.addEventListener('astrorder:quote-to-composer', onQuote)

    render(
      <MantineProvider>
        <PptxViewer
          artifact={{
            id: 'deck-1',
            sessionId: 's-1',
            agentId: 'codex',
            name: 'Q4汇报.pptx',
            path: '/docs/Q4汇报.pptx',
            kind: 'workspace_file',
            readUrl: '/api/v1/files/read?path=%2Fdocs%2FQ4.pptx',
            mediaType: 'application/vnd.openxmlformats-officedocument.presentationml.presentation',
            writable: false,
          }}
        />
      </MantineProvider>,
    )

    await waitFor(() => {
      expect(screen.getByText('2026 年第四季度汇报')).toBeTruthy()
    })
    expect(screen.getByText('开场致谢并介绍背景')).toBeTruthy()

    // Switch to slide 2
    fireEvent.click(screen.getByRole('button', { name: '下一页' }))
    await waitFor(() => {
      expect(screen.getByText('办公套件三期上线')).toBeTruthy()
    })
    expect(screen.getByText('PPT 导出')).toBeTruthy()
    expect(screen.getByText('重点展示 PPT 与会议闭环')).toBeTruthy()

    // Quote slide 2 to chat
    fireEvent.click(screen.getByRole('button', { name: '引用当前页' }))
    expect(quoted.length).toBe(1)
    expect(quoted[0]).toContain('第 2 页')
    expect(quoted[0]).toContain('办公套件三期上线')

    window.removeEventListener('astrorder:quote-to-composer', onQuote)
  })
})
