import { authenticatedApiFetch, type DbxStatementResult } from '../../../api/client'
import { rasterizeMarkdownMermaidDiagrams } from './mermaidToPng'
import { triggerBlobDownload } from './printPdf'

export interface ExportSheetInput {
  name: string
  columns: string[]
  rows: unknown[][]
}

const TABLE_SEP_RE = /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$/

/** Returns true when Markdown contains at least one GFM table. */
export function hasMarkdownTable(markdown: string): boolean {
  const lines = markdown.replace(/\r\n/g, '\n').split('\n')
  let inFence = false
  for (let i = 0; i < lines.length - 1; i++) {
    const trimmed = lines[i].trim()
    if (/^(`{3,}|~{3,})/.test(trimmed)) {
      inFence = !inFence
      continue
    }
    if (inFence) continue
    if (lines[i].includes('|') && TABLE_SEP_RE.test(lines[i + 1])) {
      return true
    }
  }
  return false
}

function sanitizeFileName(raw: string | undefined, fallback: string, ext: string): string {
  const cleaned = (raw || fallback)
    .replace(/[\\/:*?"<>|\r\n\t]/g, '_')
    .replace(/^\.+|\.+$/g, '')
    .trim()
  const base = cleaned || fallback
  return base.toLowerCase().endsWith(ext) ? base : `${base}${ext}`
}

async function readErrorDetail(response: Response, fallback: string): Promise<string> {
  try {
    const text = await response.text()
    if (!text) return `${fallback} (${response.status})`
    try {
      const parsed = JSON.parse(text) as { detail?: string }
      if (parsed.detail) return parsed.detail
    } catch {
      // plain text error
    }
    return text.slice(0, 200)
  } catch {
    return `${fallback} (${response.status})`
  }
}

/**
 * Exports Markdown (including rasterized 2x DPI Mermaid flowcharts) into a native Word (.docx) file.
 */
export async function exportMarkdownToDocx(params: {
  markdown: string
  title?: string
  subtitle?: string
  fileName?: string
}): Promise<void> {
  const diagrams = await rasterizeMarkdownMermaidDiagrams(params.markdown)
  const fileName = sanitizeFileName(params.fileName || params.title, 'document', '.docx')
  const response = await authenticatedApiFetch('/api/v1/office/export/docx', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      title: params.title || '',
      subtitle: params.subtitle || '',
      markdown: params.markdown,
      diagrams,
      file_name: fileName,
    }),
  })
  if (!response.ok) {
    throw new Error(await readErrorDetail(response, '导出 Word 文档失败'))
  }
  const blob = await response.blob()
  triggerBlobDownload(blob, fileName)
}

/**
 * Exports Markdown (including rasterized Mermaid PNGs) into a native 16:9 PowerPoint (.pptx) deck.
 */
export async function exportMarkdownToPptx(params: {
  markdown: string
  title?: string
  subtitle?: string
  fileName?: string
}): Promise<void> {
  const diagrams = await rasterizeMarkdownMermaidDiagrams(params.markdown)
  const fileName = sanitizeFileName(params.fileName || params.title, 'presentation', '.pptx')
  const response = await authenticatedApiFetch('/api/v1/office/export/pptx', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      title: params.title || '',
      subtitle: params.subtitle || '',
      markdown: params.markdown,
      diagrams,
      file_name: fileName,
    }),
  })
  if (!response.ok) {
    throw new Error(await readErrorDetail(response, '导出 PPT 失败'))
  }
  const blob = await response.blob()
  triggerBlobDownload(blob, fileName)
}

/**
 * Extracts all GFM tables from Markdown on the backend and downloads a multi-sheet .xlsx workbook.
 */
export async function exportMarkdownTablesToXlsx(params: {
  markdown: string
  fileName?: string
}): Promise<void> {
  const fileName = sanitizeFileName(params.fileName, 'tables', '.xlsx')
  const response = await authenticatedApiFetch('/api/v1/office/export/xlsx', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      markdown: params.markdown,
      file_name: fileName,
    }),
  })
  if (!response.ok) {
    throw new Error(await readErrorDetail(response, '导出 Excel 失败'))
  }
  const blob = await response.blob()
  triggerBlobDownload(blob, fileName)
}

/**
 * Exports explicit multi-sheet tabular data (SQL query results, filtered SheetViewer views) to .xlsx.
 */
export async function exportSheetsToXlsx(params: {
  sheets: ExportSheetInput[]
  fileName?: string
}): Promise<void> {
  if (!params.sheets.length) {
    throw new Error('没有可导出的表格数据')
  }
  const fileName = sanitizeFileName(params.fileName, 'export', '.xlsx')
  const response = await authenticatedApiFetch('/api/v1/office/export/xlsx', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      sheets: params.sheets,
      file_name: fileName,
    }),
  })
  if (!response.ok) {
    throw new Error(await readErrorDetail(response, '导出 Excel 失败'))
  }
  const blob = await response.blob()
  triggerBlobDownload(blob, fileName)
}

/**
 * Converts DBX inline SQL execution statements into multi-sheet Excel export inputs.
 */
export function buildSheetsFromSqlResults(
  results: DbxStatementResult[],
  sourceLabel?: string,
): ExportSheetInput[] {
  const sheets: ExportSheetInput[] = []
  results.forEach((stmt, idx) => {
    if (!stmt.columns || stmt.columns.length === 0) return
    const baseName = sourceLabel
      ? results.length === 1
        ? sourceLabel.replace(/\.sql$/i, '')
        : `${sourceLabel.replace(/\.sql$/i, '')}_${idx + 1}`
      : results.length === 1
        ? '查询结果'
        : `结果集_${idx + 1}`
    sheets.push({
      name: baseName,
      columns: stmt.columns.map((c) => c.name),
      rows: (stmt.rows || []).map((row) => row.map((cell) => (cell === undefined ? null : cell))),
    })
  })
  return sheets
}

/**
 * Downloads tabular rows as a UTF-8 BOM CSV file directly in the browser.
 */
export function exportRowsToCsv(params: {
  columns: string[]
  rows: unknown[][]
  fileName?: string
}): void {
  const escapeCsv = (val: unknown): string => {
    if (val === null || val === undefined) return ''
    const str = typeof val === 'string' ? val : String(val)
    if (/[",\r\n]/.test(str)) {
      return `"${str.replace(/"/g, '""')}"`
    }
    return str
  }
  const lines = [
    params.columns.map(escapeCsv).join(','),
    ...params.rows.map((row) => params.columns.map((_c, idx) => escapeCsv(row[idx])).join(',')),
  ]
  const blob = new Blob(['\ufeff' + lines.join('\r\n')], { type: 'text/csv;charset=utf-8' })
  triggerBlobDownload(blob, sanitizeFileName(params.fileName, 'export', '.csv'))
}
