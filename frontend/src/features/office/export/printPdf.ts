import { renderMarkdownMermaidSvgs } from './mermaidToPng'
import { markdownToStyledHtml } from './richClipboard'

function escapeHtmlTitle(value: string): string {
  return value.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
}

/**
 * Generates a clean, self-contained A4 HTML document with Chinese typography,
 * crisp tables, and inline vector Mermaid SVGs — free of any application branding,
 * browser URLs, or print header/footer chrome.
 */
export async function buildStandaloneHtmlDocument(params: {
  title?: string
  subtitle?: string
  markdown: string
}): Promise<string> {
  const mermaidSvgs = await renderMarkdownMermaidSvgs(params.markdown)
  const bodyHtml = markdownToStyledHtml(params.markdown, {
    title: params.title,
    subtitle: params.subtitle,
    mermaidSvgs,
  })
  const pageTitle = escapeHtmlTitle(params.title?.trim() || '文档')

  return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8" />
<meta name="viewport" content="width=device-width, initial-scale=1.0" />
<title>${pageTitle}</title>
<style>
  /* margin: 0 suppresses Chromium/WebView2 default header (app title/date) and footer (URL/page).
     Consistent per-page margins are provided by the repeating print-frame header/footer below. */
  @page {
    size: A4;
    margin: 0;
  }
  * { box-sizing: border-box; }
  html, body {
    margin: 0;
    padding: 0;
  }
  body {
    padding: 28px 20px;
    background: #f1f5f9;
    color: #1e293b;
    font-family: "Microsoft YaHei", "PingFang SC", "Noto Sans SC", -apple-system, BlinkMacSystemFont, sans-serif;
    -webkit-font-smoothing: antialiased;
  }
  .print-frame {
    width: 100%;
    border-collapse: collapse;
    border: none;
  }
  .print-frame > thead,
  .print-frame > tfoot {
    display: none;
  }
  .print-frame > tbody > tr > td {
    padding: 0;
    border: none;
  }
  .doc-print-sheet {
    max-width: 820px;
    margin: 0 auto;
    background: #ffffff;
    padding: 40px 48px;
    border-radius: 10px;
    box-shadow: 0 4px 20px rgba(15, 23, 42, 0.08);
  }
  .doc-print-sheet svg {
    max-width: 100%;
    height: auto;
  }
  .doc-print-sheet table {
    page-break-inside: auto;
  }
  .doc-print-sheet tr,
  .doc-print-sheet blockquote,
  .doc-print-sheet pre {
    break-inside: avoid;
    page-break-inside: avoid;
  }
  .doc-print-sheet h1,
  .doc-print-sheet h2,
  .doc-print-sheet h3,
  .doc-print-sheet h4 {
    break-after: avoid;
    page-break-after: avoid;
  }
  @media print {
    body {
      background: #ffffff;
      padding: 0;
    }
    .print-frame > thead {
      display: table-header-group;
    }
    .print-frame > tfoot {
      display: table-footer-group;
    }
    .print-margin-spacer {
      height: 16mm;
      border: none;
      padding: 0;
    }
    .print-frame > tbody > tr > td {
      padding: 0 16mm;
    }
    .doc-print-sheet {
      max-width: none;
      padding: 0;
      border-radius: 0;
      box-shadow: none;
    }
  }
</style>
</head>
<body>
  <table class="print-frame" role="presentation">
    <thead><tr><td class="print-margin-spacer"></td></tr></thead>
    <tbody>
      <tr>
        <td>
          <article class="doc-print-sheet">
            ${bodyHtml}
          </article>
        </td>
      </tr>
    </tbody>
    <tfoot><tr><td class="print-margin-spacer"></td></tr></tfoot>
  </table>
</body>
</html>`
}

/**
 * Triggers native browser / WebView2 Print-to-PDF using a hidden print-styled iframe,
 * preserving searchable vector text and crisp SVG diagrams with zero app chrome.
 */
export async function printMarkdownToPdf(params: {
  title?: string
  subtitle?: string
  markdown: string
}): Promise<void> {
  const html = await buildStandaloneHtmlDocument(params)
  await printHtmlDocument(html, params.title?.trim() || '文档')
}

export async function printHtmlDocument(html: string, printTitle?: string): Promise<void> {
  if (typeof document === 'undefined') return
  const iframe = document.createElement('iframe')
  iframe.style.position = 'fixed'
  iframe.style.right = '0'
  iframe.style.bottom = '0'
  iframe.style.width = '0'
  iframe.style.height = '0'
  iframe.style.border = '0'
  iframe.setAttribute('aria-hidden', 'true')
  document.body.appendChild(iframe)

  const prevDocTitle = document.title
  const restoreTitle = () => {
    if (printTitle) {
      document.title = prevDocTitle
    }
  }

  const cleanup = () => {
    window.setTimeout(() => {
      restoreTitle()
      if (iframe.parentNode) iframe.parentNode.removeChild(iframe)
    }, 1000)
  }

  await new Promise<void>((resolve) => {
    iframe.onload = () => {
      try {
        const win = iframe.contentWindow
        if (win) {
          if (printTitle) {
            document.title = printTitle
          }
          win.addEventListener('afterprint', restoreTitle, { once: true })
          win.focus()
          win.print()
        }
      } finally {
        cleanup()
        resolve()
      }
    }
    iframe.srcdoc = html
  })
}

export async function downloadStandaloneHtml(params: {
  title?: string
  subtitle?: string
  markdown: string
  fileName?: string
}): Promise<void> {
  const html = await buildStandaloneHtmlDocument(params)
  const blob = new Blob([html], { type: 'text/html;charset=utf-8' })
  const baseName = (params.fileName || params.title || 'document').replace(/[\\/:*?"<>|]/g, '_').trim() || 'document'
  const finalName = baseName.toLowerCase().endsWith('.html') ? baseName : `${baseName}.html`
  triggerBlobDownload(blob, finalName)
}

export function triggerBlobDownload(blob: Blob, fileName: string): void {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = fileName
  a.style.display = 'none'
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  window.setTimeout(() => URL.revokeObjectURL(url), 10_000)
}
