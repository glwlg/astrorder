import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { PdfViewer } from './PdfViewer'

describe('PdfViewer', () => {
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  it('loads inline PDF blob iframe and dispatches quote-to-composer with page note', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(new Uint8Array([0x25, 0x50, 0x44, 0x46]), {
        status: 200,
        headers: { 'Content-Type': 'application/pdf' },
      }),
    )
    vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:mock-pdf')
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})

    const quotedEvents: CustomEvent[] = []
    const listener = (e: Event) => quotedEvents.push(e as CustomEvent)
    window.addEventListener('astrorder:quote-to-composer', listener)

    render(
      <MantineProvider>
        <PdfViewer
          artifact={{
            id: 'pdf-1',
            sessionId: 'sess-1',
            agentId: 'codex',
            name: 'spec.pdf',
            path: '/workspace/spec.pdf',
            kind: 'workspace_file',
            readUrl: '/api/v1/files/raw?path=%2Fworkspace%2Fspec.pdf',
            mediaType: 'application/pdf',
            writable: false,
          }}
        />
      </MantineProvider>,
    )

    const iframe = await screen.findByTitle('PDF 预览：spec.pdf')
    expect(iframe.getAttribute('src')).toBe('blob:mock-pdf')

    fireEvent.click(screen.getByRole('button', { name: '引用到对话' }))
    fireEvent.change(await screen.findByPlaceholderText('如 3 或 5-8'), { target: { value: '4' } })
    fireEvent.change(screen.getByPlaceholderText('如：提炼核心结论与风险项'), { target: { value: '核对交付里程碑' } })
    fireEvent.click(screen.getByRole('button', { name: '填入输入框' }))

    await waitFor(() => {
      expect(quotedEvents).toHaveLength(1)
    })
    expect(quotedEvents[0].detail.text).toContain('/workspace/spec.pdf')
    expect(quotedEvents[0].detail.text).toContain('第 4 页')
    expect(quotedEvents[0].detail.text).toContain('核对交付里程碑')

    window.removeEventListener('astrorder:quote-to-composer', listener)
  })
})
