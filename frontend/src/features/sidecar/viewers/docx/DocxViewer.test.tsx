import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../../../api/client'
import { DocxViewer } from './DocxViewer'
import { diffDocxParagraphs } from './docxDiff'

describe('DocxViewer and docxDiff', () => {
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  it('computes accurate paragraph-level LCS redline diff', () => {
    const res = diffDocxParagraphs(
      ['第一条：项目交付日期为 10 月 1 日。', '第二条：双方签署生效。'],
      ['第一条：项目交付日期调整为 10 月 15 日。', '第二条：双方签署生效。', '第三条：增加补充条款。'],
    )
    expect(res.unchangedCount).toBe(1)
    expect(res.removedCount).toBe(1)
    expect(res.addedCount).toBe(2)
    expect(res.entries.some((e) => e.kind === 'removed' && e.text.includes('10 月 1 日'))).toBe(true)
    expect(res.entries.some((e) => e.kind === 'added' && e.text.includes('补充条款'))).toBe(true)
  })

  it('renders outline, structured content, and dispatches quote-to-composer on selection', async () => {
    vi.spyOn(api, 'previewDocx').mockResolvedValue({
      path: '/workspace/proposal.docx',
      name: 'proposal.docx',
      title: '合作方案',
      outline: [{ id: 'blk-1', level: 1, text: '第一章 背景目标' }],
      blocks: [
        { id: 'blk-1', kind: 'heading', level: 1, runs: [{ text: '第一章 背景目标', bold: true }] },
        { id: 'blk-2', kind: 'paragraph', runs: [{ text: '需要重点审核付款节点与违约条款。' }] },
      ],
      plain_paragraphs: ['第一章 背景目标', '需要重点审核付款节点与违约条款。'],
      stats: { words: 24, paragraphs: 1, tables: 0, images: 0, headings: 1 },
    })

    const quotedEvents: CustomEvent[] = []
    const listener = (e: Event) => quotedEvents.push(e as CustomEvent)
    window.addEventListener('astrorder:quote-to-composer', listener)

    render(
      <MantineProvider>
        <DocxViewer
          artifact={{
            id: 'docx-1',
            sessionId: 'sess-1',
            agentId: 'codex',
            name: 'proposal.docx',
            path: '/workspace/proposal.docx',
            kind: 'workspace_file',
            readUrl: '/api/v1/files/raw?path=%2Fworkspace%2Fproposal.docx',
            mediaType: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
            writable: false,
          }}
        />
      </MantineProvider>,
    )

    await waitFor(() => {
      expect(screen.getByText('目录大纲 (1)')).toBeTruthy()
      expect(screen.getByText('需要重点审核付款节点与违约条款。')).toBeTruthy()
    })

    // Simulate user text selection inside paper
    vi.spyOn(window, 'getSelection').mockReturnValue({
      isCollapsed: false,
      toString: () => '付款节点与违约条款',
    } as unknown as Selection)

    fireEvent.mouseUp(screen.getByText('需要重点审核付款节点与违约条款。'))
    const quoteBtn = await screen.findByRole('button', { name: '引用到当前对话' })
    fireEvent.click(quoteBtn)

    expect(quotedEvents).toHaveLength(1)
    expect(quotedEvents[0].detail.text).toContain('付款节点与违约条款')
    expect(quotedEvents[0].detail.text).toContain('/workspace/proposal.docx')

    window.removeEventListener('astrorder:quote-to-composer', listener)
  })
})
