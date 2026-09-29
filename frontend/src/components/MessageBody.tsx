import type { ReactNode } from 'react'
import { IconPlayerPause } from '@tabler/icons-react'
import { LazyDetails } from './LazyDetails'
import { CodeCommentsBlock, extractCodeComments } from './CodeCommentsBlock'

/** Presentation only: preserve native text, parse file mentions, and never turn filesystem references into arbitrary external URLs. */
export function MessageBody({
  value,
  user = false,
  onImageClick,
  onGalleryClick,
  attachmentNames = [],
  renderMarkdown,
  sessionId,
  connectionId,
}: {
  value: string
  user?: boolean
  onImageClick?: (url: string) => void
  onGalleryClick?: (url: string, allImages: string[]) => void
  attachmentNames?: string[]
  renderMarkdown: (value: string) => ReactNode
  sessionId?: string
  connectionId?: string
}) {
  const nativeSummary = value.startsWith('[CONTEXT COMPACTION — REFERENCE ONLY]')
  const modelNotice = value.startsWith('[System: The active model for this chat has changed to ')
  if (nativeSummary || modelNotice) return <LazyDetails summary={nativeSummary ? '上下文压缩摘要' : '模型已切换'}>
    {renderMarkdown(value)}
  </LazyDetails>
  if (!user && /^(正在压缩上下文|上下文压缩完成|上下文压缩失败|上下文压缩状态未确认)$/.test(value)) {
    return <div role="status" aria-live="polite">{value === '正在压缩上下文' ? '正在压缩上下文，请稍候…' : value}</div>
  }
  if (!user && value.trim() === 'Operation interrupted.') return <div className="message-interrupted">
    <span><IconPlayerPause size={16} />本轮已中断</span>
    <LazyDetails summary="查看原始记录"><code>{value}</code></LazyDetails>
  </div>
  let text = value
  const extractedImages: Array<{ name: string; path: string }> = []
  const hasAttachedImage = (name: string) => attachmentNames.some(
    (attachmentName) => attachmentName.toLowerCase() === name.toLowerCase(),
  )

  if (!user) {
    const { comments, cleanText } = extractCodeComments(value)
    let processedText = comments.length ? cleanText : value

    // 识别 Hermes/Codex 在 assistant 回复中发出的 MEDIA: 媒体标记并提取为缩略图卡片
    const mediaLines: string[] = []
    const remainingLines: string[] = []
    let isFenced = false
    for (const line of processedText.split('\n')) {
      if (/^\s*(```|~~~)/.test(line)) isFenced = !isFenced
      const mediaMatch = !isFenced && line.trim().match(/^MEDIA:\s*(.+)$/i)
      if (mediaMatch) {
        mediaLines.push(mediaMatch[1].trim())
      } else {
        remainingLines.push(line)
      }
    }

    for (const rawMedia of mediaLines) {
      const cleanPath = rawMedia.replace(/^[<`"']|[>`"']$/g, '').trim()
      const name = cleanPath.split(/[/\\]/).pop() || 'media.png'
      if (!hasAttachedImage(name) && !extractedImages.some((img) => img.path === cleanPath)) {
        extractedImages.push({ name, path: cleanPath })
      }
    }
    processedText = remainingLines.join('\n')

    return (
      <>
        {extractedImages.length > 0 && (
          <div className="message-attachments message-extracted-images">
            {extractedImages.map((img, idx) => {
              const query = new URLSearchParams({ path: img.path })
              if (sessionId) query.set('session_id', sessionId)
              if (connectionId) query.set('connection_id', connectionId)
              const rawUrl = `/api/v1/files/raw?${query.toString()}`
              const allUrls = extractedImages.map((i) => {
                const q = new URLSearchParams({ path: i.path })
                if (sessionId) q.set('session_id', sessionId)
                if (connectionId) q.set('connection_id', connectionId)
                return `/api/v1/files/raw?${q.toString()}`
              })
              return (
                <button
                  type="button"
                  className="attachment-image-link"
                  key={`${idx}:${img.path}`}
                  onClick={() => onGalleryClick ? onGalleryClick(rawUrl, allUrls) : onImageClick?.(rawUrl)}
                  aria-label={`查看图片：${img.name}`}
                >
                  <img className="attachment-image" src={rawUrl} alt={img.name} loading="lazy" />
                </button>
              )
            })}
          </div>
        )}
        {processedText.trim() && renderMarkdown(processedText)}
        {comments.length > 0 && <CodeCommentsBlock comments={comments} />}
      </>
    )
  }

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

  const { comments: userComments, cleanText: userText } = extractCodeComments(lines.join('\n'))
  return (
    <>
      {extractedImages.length > 0 && (
        <div className="message-attachments message-extracted-images">
          {extractedImages.map((img, idx) => {
            const query = new URLSearchParams({ path: img.path })
            if (sessionId) query.set('session_id', sessionId)
            if (connectionId) query.set('connection_id', connectionId)
            const rawUrl = `/api/v1/files/raw?${query.toString()}`
            const allUrls = extractedImages.map((i) => {
              const q = new URLSearchParams({ path: i.path })
              if (sessionId) q.set('session_id', sessionId)
              if (connectionId) q.set('connection_id', connectionId)
              return `/api/v1/files/raw?${q.toString()}`
            })
            return (
              <button
                type="button"
                className="attachment-image-link"
                key={`${idx}:${img.path}`}
                onClick={() => onGalleryClick ? onGalleryClick(rawUrl, allUrls) : onImageClick?.(rawUrl)}
                aria-label={`查看图片：${img.name}`}
              >
                <img className="attachment-image" src={rawUrl} alt={img.name} loading="lazy" />
              </button>
            )
          })}
        </div>
      )}
      {(userComments.length ? userText : lines.join('\n')).trim() && renderMarkdown(userComments.length ? userText : lines.join('\n'))}
      {userComments.length > 0 && <CodeCommentsBlock comments={userComments} />}
    </>
  )
}
