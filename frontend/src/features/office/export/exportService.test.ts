import { afterEach, describe, expect, it, vi } from 'vitest'
import { extractMermaidBlocks } from './mermaidToPng'
import { buildStandaloneHtmlDocument } from './printPdf'
import { markdownToStyledHtml } from './richClipboard'
import {
  buildSheetsFromSqlResults,
  exportMarkdownToDocx,
  exportMarkdownToPptx,
  exportSheetsToXlsx,
  hasMarkdownTable,
} from './exportService'

describe('office export toolkit', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('extracts mermaid blocks in exact document order', () => {
    const md = [
      '# Title',
      '```ts',
      'const a = 1',
      '```',
      '```mermaid',
      'graph TD',
      '  A --> B',
      '```',
      '```mermaid',
      'sequenceDiagram',
      '  Alice->>Bob: Hi',
      '```',
    ].join('\n')
    const blocks = extractMermaidBlocks(md)
    expect(blocks).toHaveLength(2)
    expect(blocks[0].index).toBe(0)
    expect(blocks[0].code).toContain('A --> B')
    expect(blocks[1].index).toBe(1)
    expect(blocks[1].code).toContain('Alice->>Bob')
  })

  it('detects markdown tables while ignoring pipes inside code fences', () => {
    expect(hasMarkdownTable('```bash\necho a | b\n```')).toBe(false)
    expect(hasMarkdownTable('| 项目 | 状态 |\n|---|---|\n| 星序 | 完成 |')).toBe(true)
  })

  it('renders styled rich HTML with tables, headings, and inline code', () => {
    const html = markdownToStyledHtml(
      '## 核心成果\n\n完成了 **星序办公套件** 和 `export.docx`。\n\n| 模块 | 耗时 |\n| --- | ---: |\n| Word | 42 |',
      { title: '周报' },
    )
    expect(html).toContain('<h1')
    expect(html).toContain('<h2')
    expect(html).toContain('星序办公套件')
    expect(html).toContain('<table')
    expect(html).toContain('border-collapse:collapse')
    expect(html).toContain('text-align:right')
  })

  it('converts multi-statement SQL results into multi-sheet Excel inputs', () => {
    const sheets = buildSheetsFromSqlResults(
      [
        {
          index: 0,
          columns: [{ name: 'version' }, { name: 'checksum' }],
          rows: [['007', 'sha256:abc']],
          row_count: 1,
          elapsed_ms: 12,
        },
        {
          index: 1,
          columns: [{ name: 'total' }],
          rows: [[42]],
          row_count: 1,
          elapsed_ms: 5,
        },
      ],
      'migration.sql',
    )
    expect(sheets).toHaveLength(2)
    expect(sheets[0].name).toBe('migration_1')
    expect(sheets[0].columns).toEqual(['version', 'checksum'])
    expect(sheets[1].name).toBe('migration_2')
    expect(sheets[1].rows).toEqual([[42]])
  })

  it('posts to /api/v1/office/export/docx and /export/xlsx and triggers blob download', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockImplementation(
      async () =>
        new Response(new Uint8Array([80, 75, 3, 4]), {
          status: 200,
          headers: { 'Content-Type': 'application/octet-stream' },
        }),
    )
    const createUrlSpy = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:mock-download')
    const revokeSpy = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

    await exportMarkdownToDocx({ markdown: '# Hello\n\nWorld', title: 'Hello' })
    expect(fetchSpy).toHaveBeenCalledWith(
      '/api/v1/office/export/docx',
      expect.objectContaining({ method: 'POST' }),
    )

    await exportSheetsToXlsx({
      fileName: 'metrics.xlsx',
      sheets: [{ name: 'Data', columns: ['A'], rows: [[1]] }],
    })
    expect(fetchSpy).toHaveBeenCalledWith(
      '/api/v1/office/export/xlsx',
      expect.objectContaining({ method: 'POST' }),
    )

    await exportMarkdownToPptx({ markdown: '# Deck\n\n## Slide 1\n- Point A', title: 'Deck' })
    expect(fetchSpy).toHaveBeenCalledWith(
      '/api/v1/office/export/pptx',
      expect.objectContaining({ method: 'POST' }),
    )

    expect(createUrlSpy).toHaveBeenCalledTimes(3)
    expect(clickSpy).toHaveBeenCalledTimes(3)
    revokeSpy.mockRestore()
  })

  it('omits app branding and suppresses browser print chrome in standalone HTML/PDF', async () => {
    const html = await buildStandaloneHtmlDocument({
      title: 'walkthrough',
      markdown: '# 项目方案\n\n正文内容。',
    })
    expect(html).toContain('margin: 0;')
    expect(html).not.toContain('星序')
    expect(html).not.toContain('Astrorder')
    expect(html).not.toContain('astrorder')
    // Must not inject a redundant "walkthrough" <h1> above the existing "# 项目方案"
    expect(html).not.toContain('>walkthrough</h1>')
  })
})
