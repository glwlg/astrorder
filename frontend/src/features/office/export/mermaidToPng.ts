import { renderMermaid } from '../../../components/renderMermaid'

export interface RasterizedDiagram {
  index: number
  png_base64: string
  width: number
  height: number
}

const MERMAID_FENCE_RE = /^([`~]{3,})\s*mermaid(?:\s.*)?$/i

/**
 * Extracts all fenced ```mermaid blocks in document order matching the backend
 * markdown_blocks.go parser's diagram index sequence (0, 1, 2, ...).
 */
export function extractMermaidBlocks(markdown: string): Array<{ index: number; code: string }> {
  const lines = markdown.replace(/\r\n/g, '\n').replace(/\r/g, '\n').split('\n')
  const blocks: Array<{ index: number; code: string }> = []
  let i = 0
  let diagramIndex = 0

  while (i < lines.length) {
    const trimmed = lines[i].trim()
    const match = trimmed.match(/^([`~]{3,})(.*)$/)
    if (!match) {
      i++
      continue
    }
    const fence = match[1]
    const fenceChar = fence[0]
    const fenceLen = fence.length
    const isMermaid = MERMAID_FENCE_RE.test(trimmed)
    i++
    const body: string[] = []
    while (i < lines.length) {
      const closeTrim = lines[i].trim()
      if (closeTrim.length >= fenceLen && [...closeTrim].every((ch) => ch === fenceChar)) {
        i++
        break
      }
      body.push(lines[i])
      i++
    }
    if (isMermaid) {
      blocks.push({ index: diagramIndex, code: body.join('\n').trim() })
      diagramIndex++
    }
  }

  return blocks
}

function parseSvgDimensions(svgText: string): { width: number; height: number } {
  const viewBoxMatch = svgText.match(/viewBox=["']\s*[\d.-]+\s+[\d.-]+\s+([\d.]+)\s+([\d.]+)\s*["']/i)
  if (viewBoxMatch) {
    const w = Math.ceil(Number(viewBoxMatch[1]))
    const h = Math.ceil(Number(viewBoxMatch[2]))
    if (Number.isFinite(w) && Number.isFinite(h) && w > 0 && h > 0) {
      return { width: w, height: h }
    }
  }
  const wMatch = svgText.match(/\bwidth=["']([\d.]+)(?:px)?["']/i)
  const hMatch = svgText.match(/\bheight=["']([\d.]+)(?:px)?["']/i)
  const w = wMatch ? Math.ceil(Number(wMatch[1])) : 800
  const h = hMatch ? Math.ceil(Number(hMatch[1])) : 450
  return {
    width: Number.isFinite(w) && w > 0 ? w : 800,
    height: Number.isFinite(h) && h > 0 ? h : 450,
  }
}

async function svgToPngBase64(svgText: string, scale = 2): Promise<{ pngBase64: string; width: number; height: number } | null> {
  if (typeof document === 'undefined' || typeof Image === 'undefined') return null
  const { width, height } = parseSvgDimensions(svgText)
  const paddedW = Math.min(Math.max(width + 24, 160), 2200)
  const paddedH = Math.min(Math.max(height + 24, 100), 2600)

  // Ensure explicit xmlns & pixel dimensions so Chromium/WebView2 paints crisp vectors
  let preparedSvg = svgText
  if (!/xmlns=/.test(preparedSvg)) {
    preparedSvg = preparedSvg.replace('<svg', '<svg xmlns="http://www.w3.org/2000/svg"')
  }
  preparedSvg = preparedSvg.replace(/<svg\b([^>]*)>/i, (_full, attrs: string) => {
    const cleaned = attrs
      .replace(/\bwidth=["'][^"']*["']/gi, '')
      .replace(/\bheight=["'][^"']*["']/gi, '')
    return `<svg${cleaned} width="${width}" height="${height}">`
  })

  const dataUrl = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(preparedSvg)}`
  return new Promise((resolve) => {
    const img = new Image()
    img.crossOrigin = 'anonymous'
    img.onload = () => {
      try {
        const canvas = document.createElement('canvas')
        canvas.width = Math.round(paddedW * scale)
        canvas.height = Math.round(paddedH * scale)
        const ctx = canvas.getContext('2d')
        if (!ctx) {
          resolve(null)
          return
        }
        ctx.fillStyle = '#ffffff'
        ctx.fillRect(0, 0, canvas.width, canvas.height)
        ctx.scale(scale, scale)
        ctx.drawImage(img, 12, 12, width, height)
        const pngDataUrl = canvas.toDataURL('image/png')
        resolve({
          pngBase64: pngDataUrl,
          width: canvas.width,
          height: canvas.height,
        })
      } catch {
        resolve(null)
      }
    }
    img.onerror = () => resolve(null)
    img.src = dataUrl
  })
}

/**
 * Renders every Mermaid diagram inside Markdown into a 2x DPI PNG base64 image
 * ready for embedding inside Word (.docx). Failures are gracefully skipped.
 */
export async function rasterizeMarkdownMermaidDiagrams(markdown: string): Promise<RasterizedDiagram[]> {
  const blocks = extractMermaidBlocks(markdown)
  if (blocks.length === 0) return []
  const results: RasterizedDiagram[] = []
  for (const blk of blocks) {
    if (!blk.code) continue
    try {
      const rendered = await renderMermaid(blk.code)
      if (!rendered?.svg) continue
      const png = await svgToPngBase64(rendered.svg, 2)
      if (png) {
        results.push({
          index: blk.index,
          png_base64: png.pngBase64,
          width: png.width,
          height: png.height,
        })
      }
    } catch {
      // Skip unrenderable mermaid blocks; backend falls back to styled code block
    }
  }
  return results
}

/**
 * Renders all Mermaid blocks to inline SVG strings keyed by diagram index
 * (for crisp vector rendering in PDF print & standalone HTML export).
 */
export async function renderMarkdownMermaidSvgs(markdown: string): Promise<Map<number, string>> {
  const blocks = extractMermaidBlocks(markdown)
  const map = new Map<number, string>()
  for (const blk of blocks) {
    if (!blk.code) continue
    try {
      const rendered = await renderMermaid(blk.code)
      if (rendered?.svg) {
        map.set(blk.index, rendered.svg)
      }
    } catch {
      // Ignore failures; HTML builder falls back to code block
    }
  }
  return map
}
