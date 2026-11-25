function escapeHtml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

function isSafeHref(href: string): boolean {
  const trimmed = href.trim()
  return /^https?:\/\//i.test(trimmed) || /^mailto:/i.test(trimmed) || trimmed.startsWith('/') || trimmed.startsWith('#')
}

function renderInlineMarkdown(raw: string): string {
  const placeholders: string[] = []
  const stash = (html: string) => {
    const idx = placeholders.length
    placeholders.push(html)
    return `@@ASTRINLINE${idx}@@`
  }

  let s = raw
  // Stash inline code first so formatting syntax inside backticks is untouched
  s = s.replace(/`([^`]+)`/g, (_m, code: string) => stash(`<code>${escapeHtml(code)}</code>`))
  // Allow explicit <u>...</u> underlines cleanly
  s = s.replace(/<u>([\s\S]*?)<\/u>/gi, (_m, inner: string) => stash(`<u>${renderInlineMarkdown(inner)}</u>`))
  // Links [label](url)
  s = s.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (_m, label: string, url: string) => {
    if (!isSafeHref(url)) return escapeHtml(label)
    return stash(`<a href="${escapeHtml(url)}" target="_blank" rel="noopener noreferrer">${escapeHtml(label)}</a>`)
  })

  s = escapeHtml(s)
  s = s.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
  s = s.replace(/__([^_]+)__/g, '<strong>$1</strong>')
  s = s.replace(/~~([^~]+)~~/g, '<del>$1</del>')
  s = s.replace(/(?<!\*)\*([^*]+)\*(?!\*)/g, '<em>$1</em>')

  return s.replace(/@@ASTRINLINE(\d+)@@/g, (_m, idx: string) => placeholders[Number(idx)] ?? '')
}

function parseTableRow(line: string): string[] {
  const trimmed = line.trim().replace(/^\|/, '').replace(/\|$/, '')
  return trimmed.split(/(?<!\\)\|/).map((cell) => cell.replace(/\\\|/g, '|').trim())
}

function isTableSeparator(line: string): boolean {
  const trimmed = line.trim()
  if (!trimmed.includes('|') && !trimmed.includes('-')) return false
  const cells = parseTableRow(trimmed)
  return cells.length > 0 && cells.every((c) => /^:?-{3,}:?$/.test(c))
}

/**
 * Converts Markdown into clean semantic HTML suitable for WYSIWYG contentEditable
 * editing (headings, inline styles, lists, quotes, horizontal rules, and tables).
 */
export function markdownToRichHtml(markdown: string): string {
  const lines = markdown.replace(/\r\n/g, '\n').split('\n')
  const out: string[] = []
  let i = 0

  while (i < lines.length) {
    const line = lines[i]
    const trimmed = line.trim()

    if (!trimmed) {
      i++
      continue
    }

    // Fenced code block
    const fenceMatch = trimmed.match(/^(`{3,}|~{3,})(.*)$/)
    if (fenceMatch) {
      const fence = fenceMatch[1]
      const codeLines: string[] = []
      i++
      while (i < lines.length && !lines[i].trim().startsWith(fence)) {
        codeLines.push(lines[i])
        i++
      }
      if (i < lines.length) i++
      out.push(`<pre><code>${escapeHtml(codeLines.join('\n'))}</code></pre>`)
      continue
    }

    // Horizontal rule
    if (/^(-{3,}|\*{3,}|_{3,})$/.test(trimmed)) {
      out.push('<hr />')
      i++
      continue
    }

    // Headings
    const headingMatch = trimmed.match(/^(#{1,4})\s+(.+)$/)
    if (headingMatch) {
      const lvl = headingMatch[1].length
      out.push(`<h${lvl}>${renderInlineMarkdown(headingMatch[2].trim())}</h${lvl}>`)
      i++
      continue
    }

    // Table
    if (trimmed.includes('|') && i + 1 < lines.length && isTableSeparator(lines[i + 1])) {
      const headers = parseTableRow(trimmed)
      i += 2
      const bodyRows: string[][] = []
      while (i < lines.length && lines[i].trim().includes('|')) {
        bodyRows.push(parseTableRow(lines[i]))
        i++
      }
      const thead = `<thead><tr>${headers.map((h) => `<th>${renderInlineMarkdown(h)}</th>`).join('')}</tr></thead>`
      const tbody = `<tbody>${bodyRows
        .map(
          (row) =>
            `<tr>${headers.map((_h, ci) => `<td>${renderInlineMarkdown(row[ci] ?? '')}</td>`).join('')}</tr>`,
        )
        .join('')}</tbody>`
      out.push(`<table>${thead}${tbody}</table>`)
      continue
    }

    // Blockquote
    if (trimmed.startsWith('>')) {
      const quoteLines: string[] = []
      while (i < lines.length && lines[i].trim().startsWith('>')) {
        quoteLines.push(lines[i].trim().replace(/^>\s?/, ''))
        i++
      }
      out.push(`<blockquote><p>${quoteLines.map(renderInlineMarkdown).join('<br />')}</p></blockquote>`)
      continue
    }

    // Unordered list
    if (/^[-*+]\s+/.test(trimmed)) {
      const items: string[] = []
      while (i < lines.length && /^[-*+]\s+/.test(lines[i].trim())) {
        items.push(lines[i].trim().replace(/^[-*+]\s+/, ''))
        i++
      }
      out.push(`<ul>${items.map((it) => `<li>${renderInlineMarkdown(it)}</li>`).join('')}</ul>`)
      continue
    }

    // Ordered list
    if (/^\d+\.\s+/.test(trimmed)) {
      const items: string[] = []
      while (i < lines.length && /^\d+\.\s+/.test(lines[i].trim())) {
        items.push(lines[i].trim().replace(/^\d+\.\s+/, ''))
        i++
      }
      out.push(`<ol>${items.map((it) => `<li>${renderInlineMarkdown(it)}</li>`).join('')}</ol>`)
      continue
    }

    // Regular paragraph (merge consecutive non-special lines)
    const paraLines: string[] = []
    while (
      i < lines.length &&
      lines[i].trim() !== '' &&
      !/^(#{1,4}\s+|[-*+]\s+|\d+\.\s+|>|```|~~~|-{3,}$|\*{3,}$)/.test(lines[i].trim()) &&
      !(lines[i].trim().includes('|') && i + 1 < lines.length && isTableSeparator(lines[i + 1]))
    ) {
      paraLines.push(lines[i].trim())
      i++
    }
    out.push(`<p>${paraLines.map(renderInlineMarkdown).join('<br />')}</p>`)
  }

  return out.length > 0 ? out.join('\n') : '<p><br /></p>'
}

function serializeInlineNode(node: Node): string {
  if (node.nodeType === Node.TEXT_NODE) {
    return (node.textContent || '').replace(/\u00A0/g, ' ')
  }
  if (node.nodeType !== Node.ELEMENT_NODE) return ''
  const el = node as HTMLElement
  const tag = el.tagName.toLowerCase()
  if (tag === 'br') return '\n'

  const inner = Array.from(el.childNodes).map(serializeInlineNode).join('')
  switch (tag) {
    case 'strong':
    case 'b':
      return inner.trim() ? `**${inner}**` : ''
    case 'em':
    case 'i':
      return inner.trim() ? `*${inner}*` : ''
    case 'u':
      return inner.trim() ? `<u>${inner}</u>` : ''
    case 'del':
    case 's':
    case 'strike':
      return inner.trim() ? `~~${inner}~~` : ''
    case 'code':
      return inner ? `\`${inner}\`` : ''
    case 'a': {
      const href = el.getAttribute('href') || ''
      return href ? `[${inner || href}](${href})` : inner
    }
    default:
      return inner
  }
}

function serializeTable(table: HTMLElement): string {
  const rows = Array.from(table.querySelectorAll('tr'))
  if (rows.length === 0) return ''
  const matrix = rows.map((tr) =>
    Array.from(tr.children).map((cell) =>
      serializeInlineNode(cell).replace(/\n+/g, ' ').replace(/\|/g, '\\|').trim(),
    ),
  )
  const colCount = Math.max(...matrix.map((r) => r.length), 1)
  const padRow = (r: string[]) => Array.from({ length: colCount }, (_v, idx) => r[idx] || '')
  const header = padRow(matrix[0])
  const lines = [
    `| ${header.join(' | ')} |`,
    `| ${header.map(() => '---').join(' | ')} |`,
    ...matrix.slice(1).map((r) => `| ${padRow(r).join(' | ')} |`),
  ]
  return lines.join('\n')
}

function serializeBlockNode(node: Node): string {
  if (node.nodeType === Node.TEXT_NODE) {
    return (node.textContent || '').trim()
  }
  if (node.nodeType !== Node.ELEMENT_NODE) return ''
  const el = node as HTMLElement
  const tag = el.tagName.toLowerCase()

  switch (tag) {
    case 'h1':
    case 'h2':
    case 'h3':
    case 'h4':
    case 'h5':
    case 'h6': {
      const lvl = Math.min(Number(tag[1]) || 1, 4)
      const txt = Array.from(el.childNodes).map(serializeInlineNode).join('').trim()
      return txt ? `${'#'.repeat(lvl)} ${txt}` : ''
    }
    case 'hr':
      return '---'
    case 'pre': {
      const code = el.textContent || ''
      return `\`\`\`\n${code.replace(/\n$/, '')}\n\`\`\``
    }
    case 'blockquote': {
      const text = Array.from(el.childNodes)
        .map(serializeBlockNode)
        .filter(Boolean)
        .join('\n')
      return text
        .split('\n')
        .map((l) => `> ${l}`)
        .join('\n')
    }
    case 'ul': {
      const items = Array.from(el.children)
        .filter((c) => c.tagName.toLowerCase() === 'li')
        .map((li) => `- ${Array.from(li.childNodes).map(serializeInlineNode).join('').trim()}`)
      return items.join('\n')
    }
    case 'ol': {
      const items = Array.from(el.children)
        .filter((c) => c.tagName.toLowerCase() === 'li')
        .map((li, idx) => `${idx + 1}. ${Array.from(li.childNodes).map(serializeInlineNode).join('').trim()}`)
      return items.join('\n')
    }
    case 'table':
      return serializeTable(el)
    case 'p':
    case 'div': {
      const hasBlockChildren = Array.from(el.children).some((c) =>
        ['p', 'div', 'h1', 'h2', 'h3', 'h4', 'ul', 'ol', 'table', 'blockquote', 'pre', 'hr'].includes(
          c.tagName.toLowerCase(),
        ),
      )
      if (hasBlockChildren) {
        return Array.from(el.childNodes).map(serializeBlockNode).filter(Boolean).join('\n\n')
      }
      return Array.from(el.childNodes).map(serializeInlineNode).join('').trim()
    }
    default:
      return Array.from(el.childNodes).map(serializeInlineNode).join('').trim()
  }
}

/**
 * Serializes WYSIWYG HTML back into clean, normalized Markdown.
 */
export function richHtmlToMarkdown(html: string): string {
  if (typeof DOMParser === 'undefined') return html
  const parser = new DOMParser()
  const doc = parser.parseFromString(html, 'text/html')
  const blocks = Array.from(doc.body.childNodes)
    .map(serializeBlockNode)
    .map((s) => s.trim())
    .filter(Boolean)
  return blocks.join('\n\n')
}

export function countDocumentStats(markdown: string): { words: number; minutes: number } {
  const plain = markdown
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/[#>*_`~|\-[\]()]/g, ' ')
    .trim()
  if (!plain) return { words: 0, minutes: 0 }
  const cjk = (plain.match(/[\u4e00-\u9fff]/g) || []).length
  const latin = (plain.replace(/[\u4e00-\u9fff]/g, ' ').match(/[A-Za-z0-9]+/g) || []).length
  const words = cjk + latin
  return { words, minutes: Math.max(1, Math.ceil(words / 350)) }
}
