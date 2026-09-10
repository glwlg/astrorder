import { IconExternalLink, IconEye, IconFileText } from '@tabler/icons-react'
import { Anchor } from '@mantine/core'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

function cleanHref(raw: string | undefined): string {
  if (!raw) return ''
  let cleaned = raw.trim()
  if (cleaned.startsWith('<') && cleaned.endsWith('>')) {
    cleaned = cleaned.slice(1, -1).trim()
  }
  if (cleaned.startsWith('file:///')) {
    cleaned = cleaned.slice(8)
  } else if (cleaned.startsWith('file://')) {
    cleaned = cleaned.slice(7)
  }
  return cleaned
}

function isLocalPath(path: string): boolean {
  if (/^[a-zA-Z]:[/\\]/.test(path)) return true
  if (path.startsWith('file://')) return true
  if (/^\/(?:Users|home|tmp|var|private|opt|etc|mnt)\b/i.test(path)) return true
  return false
}

const IMAGE_EXTENSIONS = new Set(['.png', '.jpg', '.jpeg', '.webp', '.svg', '.gif', '.bmp', '.ico'])

function isImagePath(path: string): boolean {
  const dot = path.lastIndexOf('.')
  if (dot === -1) return false
  const ext = path.slice(dot).toLowerCase().split(/[?#]/)[0]
  return IMAGE_EXTENSIONS.has(ext)
}

function safeUrl(value: string | undefined): string | undefined {
  if (!value) return undefined
  const cleaned = cleanHref(value)
  if (/^(?:https?:|mailto:)/i.test(cleaned)) return cleaned
  if (cleaned.startsWith('//')) return undefined
  if (cleaned.startsWith('/') || cleaned.startsWith('#')) return cleaned
  return undefined
}

export function MarkdownContent({ value, onImageClick }: { value: string; onImageClick?: (url: string) => void }) {
  const handleOpenLocalFile = async (path: string) => {
    try {
      const res = await fetch('/api/v1/system/open-file', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path }),
      })
      if (!res.ok) throw new Error('打开文件失败')
    } catch {
      // Fallback: trigger download
      window.open(`/api/v1/files/raw?path=${encodeURIComponent(path)}&download=1`, '_blank')
    }
  }

  return (
    <div className="markdown-content">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        skipHtml
        urlTransform={(url) => url}
        components={{
          a: (props) => {
            const { href, children, ...rest } = props
            const cleaned = cleanHref(href)

            // Local file links e.g. C:/... or /home/... or <C:/...>
            if (isLocalPath(cleaned)) {
              if (isImagePath(cleaned)) {
                const rawUrl = `/api/v1/files/raw?path=${encodeURIComponent(cleaned)}`
                return (
                  <button
                    type="button"
                    className="markdown-local-link markdown-image-link"
                    onClick={() => {
                      if (onImageClick) onImageClick(rawUrl)
                      else window.open(rawUrl, '_blank')
                    }}
                    title={`预览图片：${cleaned}`}
                  >
                    <IconEye size={13} aria-hidden="true" />
                    <span>{children}</span>
                  </button>
                )
              }

              // Local document / code / drawio file
              return (
                <button
                  type="button"
                  className="markdown-local-link markdown-file-link"
                  onClick={() => void handleOpenLocalFile(cleaned)}
                  title={`在本地打开：${cleaned}`}
                >
                  <IconFileText size={13} aria-hidden="true" />
                  <span>{children}</span>
                </button>
              )
            }

            const safe = safeUrl(href)
            if (!safe) return <>{children}</>
            const external = /^(?:https?:|mailto:)/i.test(safe)
            return (
              <Anchor
                {...rest}
                href={safe}
                target={external ? '_blank' : undefined}
                rel={external ? 'noreferrer' : undefined}
              >
                {children}
                {external && <IconExternalLink className="markdown-external-icon" size={13} aria-hidden="true" />}
              </Anchor>
            )
          },
          img: (props) => {
            const cleaned = cleanHref(props.src)
            let src = safeUrl(cleaned)
            if (!src && isLocalPath(cleaned)) {
              src = `/api/v1/files/raw?path=${encodeURIComponent(cleaned)}`
            }
            if (!src) return null
            return onImageClick ? (
              <button
                type="button"
                className="markdown-image-button"
                onClick={() => onImageClick(src)}
                aria-label={`放大图片：${props.alt || ''}`}
              >
                <img src={src} alt={props.alt || ''} loading="lazy" />
              </button>
            ) : (
              <img src={src} alt={props.alt || ''} loading="lazy" />
            )
          },
          table: (props) => <div className="markdown-table-wrap"><table>{props.children}</table></div>,
          pre: (props) => <pre className="markdown-code-block">{props.children}</pre>,
        }}
      >
        {value}
      </ReactMarkdown>
    </div>
  )
}
