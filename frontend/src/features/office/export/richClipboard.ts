/**
 * Converts GFM Markdown into clean, inline-styled HTML suitable for pasting into
 * Microsoft Word, WPS, Outlook, Feishu/Lark, WeCom, and DingTalk without losing formatting.
 */

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

function safeHref(url: string): string | null {
  const trimmed = url.trim().replace(/^<|>$/g, '')
  if (/^(https?:|mailto:)/i.test(trimmed)) return trimmed
  return null
}

function renderInlineHtml(raw: string): string {
  // Tokenize inline code first to protect contents
  const tokens: string[] = []
  let s = raw.replace(/`([^`\n]+)`/g, (_m, code: string) => {
    const idx = tokens.length
    tokens.push(
      `<code style="font-family:Consolas,'Courier New',monospace;font-size:0.9em;background:#f1f5f9;color:#0f172a;padding:1px 5px;border-radius:4px;">${escapeHtml(code)}</code>`,
    )
    return `@@ASTR_CODE_TOK_${idx}@@`
  })

  s = escapeHtml(s)

  // Markdown links [label](url)
  s = s.replace(/\[([^\]]+)\]\(([^)\s]+)(?:\s+&quot;[^&]*&quot;)?\)/g, (_m, label: string, url: string) => {
    const clean = safeHref(url.replace(/&amp;/g, '&'))
    if (!clean) return label
    return `<a href="${escapeHtml(clean)}" style="color:#2563eb;text-decoration:underline;">${label}</a>`
  })

  // Bold + Italic, Bold, Strike, Italic
  s = s.replace(/\*\*\*([^*\n]+)\*\*\*/g, '<strong><em>$1</em></strong>')
  s = s.replace(/\*\*([^*\n]+)\*\*/g, '<strong style="font-weight:600;color:#0f172a;">$1</strong>')
  s = s.replace(/__([^_\n]+)__/g, '<strong style="font-weight:600;color:#0f172a;">$1</strong>')
  s = s.replace(/~~([^~\n]+)~~/g, '<del style="color:#64748b;">$1</del>')
  s = s.replace(/(^|[\s(（])\*([^*\n]+)\*(?=[\s).,，。；：!！?？)）]|$)/g, '$1<em>$2</em>')

  return s.replace(/@@ASTR_CODE_TOK_(\d+)@@/g, (_m, idx: string) => tokens[Number(idx)] || '')
}

function splitPipeRow(line: string): string[] {
  const s = line.trim().replace(/^\|/, '').replace(/\|$/, '')
  return s.split(/(?<!\\)\|/).map((c) => c.replace(/\\\|/g, '|').trim())
}

const TABLE_SEP_RE = /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$/

export interface RichHtmlOptions {
  title?: string
  subtitle?: string
  mermaidSvgs?: Map<number, string>
}

export function markdownToStyledHtml(markdown: string, options: RichHtmlOptions = {}): string {
  const lines = markdown.replace(/\r\n/g, '\n').replace(/\r/g, '\n').split('\n')
  const out: string[] = []
  let i = 0
  let diagramIdx = 0

  if (options.title?.trim()) {
    const firstNonEmpty = lines.find((l) => l.trim().length > 0)?.trim() || ''
    const hasLeadingH1 = /^#\s+/.test(firstNonEmpty)
    if (!hasLeadingH1) {
      out.push(
        `<h1 style="margin:0 0 8px;padding-bottom:8px;border-bottom:2px solid #1d4ed8;font-size:22px;font-weight:700;color:#0f172a;">${escapeHtml(options.title.trim())}</h1>`,
      )
    }
    if (options.subtitle?.trim()) {
      out.push(`<p style="margin:0 0 16px;font-size:12px;color:#64748b;">${escapeHtml(options.subtitle.trim())}</p>`)
    }
  }

  while (i < lines.length) {
    const line = lines[i]
    const trimmed = line.trim()
    if (!trimmed) {
      i++
      continue
    }

    // Fenced code / mermaid
    const fenceMatch = trimmed.match(/^([`~]{3,})(.*)$/)
    if (fenceMatch) {
      const fenceChar = fenceMatch[1][0]
      const fenceLen = fenceMatch[1].length
      const lang = fenceMatch[2].trim().split(/\s+/)[0] || ''
      i++
      const codeLines: string[] = []
      while (i < lines.length) {
        const ct = lines[i].trim()
        if (ct.length >= fenceLen && [...ct].every((c) => c === fenceChar)) {
          i++
          break
        }
        codeLines.push(lines[i])
        i++
      }
      if (lang.toLowerCase() === 'mermaid') {
        const svg = options.mermaidSvgs?.get(diagramIdx)
        diagramIdx++
        if (svg) {
          out.push(
            `<div style="margin:14px 0;padding:12px;background:#ffffff;border:1px solid #e2e8f0;border-radius:8px;text-align:center;break-inside:avoid;">${svg}</div>`,
          )
          continue
        }
      }
      const langBadge = lang
        ? `<div style="font-size:11px;font-weight:600;color:#64748b;text-transform:uppercase;margin-bottom:6px;font-family:Consolas,monospace;">${escapeHtml(lang)}</div>`
        : ''
      out.push(
        `<div style="margin:12px 0;padding:12px 14px;background:#f8fafc;border:1px solid #e2e8f0;border-left:3px solid #94a3b8;border-radius:6px;break-inside:avoid;">${langBadge}<pre style="margin:0;white-space:pre-wrap;word-break:break-word;font-family:Consolas,'Courier New',monospace;font-size:12.5px;line-height:1.55;color:#1e293b;">${escapeHtml(codeLines.join('\n'))}</pre></div>`,
      )
      continue
    }

    // Heading
    const headingMatch = trimmed.match(/^(#{1,6})\s+(.*)$/)
    if (headingMatch) {
      const lvl = headingMatch[1].length
      const text = headingMatch[2].replace(/\s+#+$/, '').trim()
      const sizes = [22, 18, 15.5, 14, 13.5, 13]
      const border = lvl === 1 ? 'padding-bottom:6px;border-bottom:1px solid #e2e8f0;' : ''
      out.push(
        `<h${lvl} style="margin:${lvl <= 2 ? '18px 0 8px' : '14px 0 6px'};font-size:${sizes[lvl - 1]}px;font-weight:700;color:#0f172a;line-height:1.35;${border}">${renderInlineHtml(text)}</h${lvl}>`,
      )
      i++
      continue
    }

    // Horizontal rule
    if (/^\s*([-*_])(\s*[-*_]){2,}\s*$/.test(line) && !trimmed.includes('|')) {
      out.push('<hr style="border:none;border-top:1px solid #cbd5e1;margin:16px 0;" />')
      i++
      continue
    }

    // GFM Table
    if (i + 1 < lines.length && line.includes('|') && TABLE_SEP_RE.test(lines[i + 1])) {
      const headers = splitPipeRow(line)
      const sepCells = splitPipeRow(lines[i + 1])
      const aligns = headers.map((_h, idx) => {
        const s = sepCells[idx] || ''
        if (s.startsWith(':') && s.endsWith(':')) return 'center'
        if (s.endsWith(':')) return 'right'
        return 'left'
      })
      i += 2
      const bodyRows: string[][] = []
      while (i < lines.length && lines[i].trim() && lines[i].includes('|')) {
        if (TABLE_SEP_RE.test(lines[i])) {
          i++
          continue
        }
        bodyRows.push(splitPipeRow(lines[i]))
        i++
      }
      const thead = headers
        .map(
          (h, idx) =>
            `<th style="border:1px solid #cbd5e1;background:#eef2f7;padding:7px 10px;text-align:${aligns[idx]};font-weight:600;color:#0f172a;font-size:13px;">${renderInlineHtml(h)}</th>`,
        )
        .join('')
      const tbody = bodyRows
        .map((row, rIdx) => {
          const bg = rIdx % 2 === 1 ? '#f8fafc' : '#ffffff'
          const tds = headers
            .map(
              (_h, idx) =>
                `<td style="border:1px solid #e2e8f0;background:${bg};padding:6px 10px;text-align:${aligns[idx]};color:#1e293b;font-size:13px;">${renderInlineHtml(row[idx] || '')}</td>`,
            )
            .join('')
          return `<tr>${tds}</tr>`
        })
        .join('')
      out.push(
        `<table style="border-collapse:collapse;width:100%;margin:12px 0;font-size:13px;break-inside:avoid;"><thead><tr>${thead}</tr></thead><tbody>${tbody}</tbody></table>`,
      )
      continue
    }

    // Blockquote
    if (trimmed.startsWith('>')) {
      const qLines: string[] = []
      while (i < lines.length && lines[i].trim().startsWith('>')) {
        qLines.push(lines[i].trim().replace(/^>\s?/, ''))
        i++
      }
      out.push(
        `<blockquote style="margin:10px 0;padding:8px 14px;border-left:3px solid #3b82f6;background:#f8fafc;color:#475569;border-radius:0 6px 6px 0;">${qLines
          .map((l) => renderInlineHtml(l))
          .join('<br/>')}</blockquote>`,
      )
      continue
    }

    // Unordered list
    if (/^\s*[-*+]\s+/.test(line)) {
      const items: string[] = []
      while (i < lines.length && /^\s*[-*+]\s+/.test(lines[i])) {
        const text = lines[i].replace(/^\s*[-*+]\s+/, '').replace(/^\[ \]\s+/, '☐ ').replace(/^\[[xX]\]\s+/, '☑ ')
        items.push(`<li style="margin:3px 0;line-height:1.65;">${renderInlineHtml(text)}</li>`)
        i++
      }
      out.push(`<ul style="margin:8px 0;padding-left:22px;color:#1e293b;">${items.join('')}</ul>`)
      continue
    }

    // Ordered list
    if (/^\s*\d+[.)]\s+/.test(line)) {
      const items: string[] = []
      while (i < lines.length && /^\s*\d+[.)]\s+/.test(lines[i])) {
        const text = lines[i].replace(/^\s*\d+[.)]\s+/, '')
        items.push(`<li style="margin:3px 0;line-height:1.65;">${renderInlineHtml(text)}</li>`)
        i++
      }
      out.push(`<ol style="margin:8px 0;padding-left:22px;color:#1e293b;">${items.join('')}</ol>`)
      continue
    }

    // Paragraph
    const para: string[] = []
    while (i < lines.length) {
      const cur = lines[i].trim()
      if (!cur) break
      if (
        para.length > 0 &&
        (/^([`~]{3,}|#{1,6}\s|>|\s*[-*+]\s+|\s*\d+[.)]\s+)/.test(lines[i]) ||
          (i + 1 < lines.length && lines[i].includes('|') && TABLE_SEP_RE.test(lines[i + 1])))
      ) {
        break
      }
      para.push(cur)
      i++
    }
    if (para.length > 0) {
      out.push(`<p style="margin:8px 0;line-height:1.7;color:#1e293b;">${renderInlineHtml(para.join(' '))}</p>`)
    }
  }

  return `<div style="font-family:'Microsoft YaHei','PingFang SC','Noto Sans SC',-apple-system,BlinkMacSystemFont,sans-serif;font-size:14px;color:#1e293b;line-height:1.7;">${out.join('\n')}</div>`
}

/**
 * Copies Markdown to the clipboard as both styled HTML (for Word / Outlook / Feishu / WeCom)
 * and plain Markdown fallback text.
 */
export async function copyMarkdownAsRichText(markdown: string, options: RichHtmlOptions = {}): Promise<void> {
  const html = markdownToStyledHtml(markdown, options)

  if (typeof navigator !== 'undefined' && navigator.clipboard?.write && typeof ClipboardItem !== 'undefined') {
    const item = new ClipboardItem({
      'text/html': new Blob([html], { type: 'text/html' }),
      'text/plain': new Blob([markdown], { type: 'text/plain' }),
    })
    await navigator.clipboard.write([item])
    return
  }

  // Fallback for environments without ClipboardItem
  if (typeof document !== 'undefined') {
    const container = document.createElement('div')
    container.innerHTML = html
    container.style.position = 'fixed'
    container.style.left = '-99999px'
    container.style.top = '0'
    document.body.appendChild(container)
    try {
      const selection = window.getSelection()
      const range = document.createRange()
      range.selectNodeContents(container)
      selection?.removeAllRanges()
      selection?.addRange(range)
      const ok = document.execCommand('copy')
      selection?.removeAllRanges()
      if (ok) return
    } finally {
      document.body.removeChild(container)
    }
  }

  if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(markdown)
    return
  }
  throw new Error('当前环境不支持剪贴板写入')
}
