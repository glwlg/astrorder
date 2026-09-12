import type { ReactNode } from 'react'
import { IconPlayerPause } from '@tabler/icons-react'
import { LazyDetails } from './LazyDetails'

/** Presentation only: preserve native text, parse file mentions, and never turn filesystem references into arbitrary external URLs. */
export function MessageBody({
  value,
  user = false,
  onImageClick,
  attachmentNames = [],
  renderMarkdown,
}: {
  value: string
  user?: boolean
  onImageClick?: (url: string) => void
  attachmentNames?: string[]
  renderMarkdown: (value: string) => ReactNode
}) {
  if (!user && value.trim() === 'Operation interrupted.') return <div className="message-interrupted">
    <span><IconPlayerPause size={16} />本轮已中断</span>
    <LazyDetails summary="查看原始记录"><code>{value}</code></LazyDetails>
  </div>
  if (!user) return renderMarkdown(value)

  let text = value
  const extractedImages: Array<{ name: string; path: string }> = []
  const hasAttachedImage = (name: string) => attachmentNames.some(
    (attachmentName) => attachmentName.toLowerCase() === name.toLowerCase(),
  )

  // Parse Codex-style mentioned files wrapper
  if (text.includes('# Files mentioned by the user:')) {
    const fileMatches = [...text.matchAll(/##\s*([\w\.-]+\.(?:png|jpe?g|gif|webp|svg)):\s*([^\r\n]+)/gi)]
    for (const m of fileMatches) {
      if (!hasAttachedImage(m[1])) extractedImages.push({ name: m[1], path: m[2].trim() })
    }
    const imgTagMatches = [...text.matchAll(/<image\s+[^>]*path=["']([^"']+)["']/gi)]
    for (const m of imgTagMatches) {
      const p = m[1].trim()
      const n = p.split(/[/\\]/).pop() || 'image.png'
      if (!hasAttachedImage(n) && !extractedImages.some(x => x.path === p)) {
        extractedImages.push({ name: n, path: p })
      }
    }
    if (text.includes('## My request:')) {
      const parts = text.split('## My request:')
      if (parts[1]?.trim()) {
        text = parts[1].trim()
      }
    }
  }

  let fenced = false
  const references: string[] = []
  const lines: string[] = []
  for (const line of text.split('\n')) {
    if (/^\s*(```|~~~)/.test(line)) fenced = !fenced
    if (!fenced && /^@image:(?:[A-Za-z]:[\\/]|\/|\\\\)\S.*$/.test(line.trim())) {
      references.push(line.trim())
    } else {
      lines.push(line)
    }
  }
  for (const reference of references) {
    const path = reference.replace(/^@image:/, '').trim()
    const name = path.split(/[/\\]/).pop() || 'image.png'
    if (!hasAttachedImage(name) && !extractedImages.some((img) => img.path === path)) {
      extractedImages.push({ name, path })
    }
  }

  return (
    <>
      {extractedImages.length > 0 && (
        <div className="message-attachments message-extracted-images">
          {extractedImages.map((img, idx) => {
            const rawUrl = `/api/v1/files/raw?path=${encodeURIComponent(img.path)}`
            return (
              <button
                type="button"
                className="attachment-image-link"
                key={`${idx}:${img.path}`}
                onClick={() => onImageClick?.(rawUrl)}
                aria-label={`查看图片：${img.name}`}
              >
                <img className="attachment-image" src={rawUrl} alt={img.name} loading="lazy" />
              </button>
            )
          })}
        </div>
      )}
      {lines.join('\n').trim() && renderMarkdown(lines.join('\n'))}
    </>
  )
}
