import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../../../api/client'
import { SheetViewer } from './SheetViewer'

describe('SheetViewer', () => {
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  it('switches sheets, filters, computes column stats, and sends rows to chat', async () => {
    vi.spyOn(api, 'previewSheet').mockResolvedValue({
      path: '/workspace/sales.xlsx',
      name: 'sales.xlsx',
      format: 'xlsx',
      encoding: 'UTF-8',
      sheets: [
        {
          name: 'Q4汇总',
          columns: ['区域', '金额'],
          rows: [
            ['华东', 100],
            ['华南', 250],
            ['华北', 150],
          ],
          total_rows: 3,
          total_cols: 2,
          truncated: false,
        },
        {
          name: '备注',
          columns: ['说明'],
          rows: [['已复核']],
          total_rows: 1,
          total_cols: 1,
          truncated: false,
        },
      ],
    })

    const quotedEvents: CustomEvent[] = []
    const listener = (e: Event) => quotedEvents.push(e as CustomEvent)
    window.addEventListener('astrorder:quote-to-composer', listener)

    render(
      <MantineProvider>
        <SheetViewer
          artifact={{
            id: 'sheet-1',
            sessionId: 'sess-1',
            agentId: 'codex',
            name: 'sales.xlsx',
            path: '/workspace/sales.xlsx',
            kind: 'workspace_file',
            readUrl: '/api/v1/files/raw?path=%2Fworkspace%2Fsales.xlsx',
            mediaType: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
            writable: false,
          }}
        />
      </MantineProvider>,
    )

    await waitFor(() => {
      expect(screen.getByText('华东')).toBeTruthy()
      expect(screen.getByText('华南')).toBeTruthy()
    })

    // Click numeric column header "金额" -> displays sum = 500
    fireEvent.click(screen.getByText('金额'))
    await waitFor(() => {
      expect(screen.getByText('500')).toBeTruthy()
    })

    // Search filter narrows to 华南
    fireEvent.change(screen.getByPlaceholderText('全文筛选…'), { target: { value: '华南' } })
    await waitFor(() => {
      expect(screen.queryByText('华东')).toBeNull()
      expect(screen.getByText('华南')).toBeTruthy()
    })

    // Send filtered rows to chat
    fireEvent.click(screen.getByRole('button', { name: /发送给对话分析/ }))
    expect(quotedEvents).toHaveLength(1)
    expect(quotedEvents[0].detail.text).toContain('| 区域 | 金额 |')
    expect(quotedEvents[0].detail.text).toContain('| 华南 | 250 |')

    // Switch to second sheet tab
    fireEvent.click(screen.getByRole('tab', { name: /备注/ }))
    await waitFor(() => {
      expect(screen.getByText('已复核')).toBeTruthy()
    })

    window.removeEventListener('astrorder:quote-to-composer', listener)
  })
})
