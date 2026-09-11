import {
  IconExternalLink,
  IconEye,
  IconFileText,
} from '@tabler/icons-react'
import { Anchor } from '@mantine/core'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import type { Session } from '../domain/types'
import { artifactViewerRegistry } from '../features/sidecar/registry'
import { resolveArtifactFromPath } from '../features/sidecar/resolver'
import { useSidecarStore } from '../features/sidecar/sidecarStore'
import { useAstrorderStore } from '../state/store'

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

const IMAGE_EXTENSIONS = new Set(['.png', '.jpg', '.jpeg', '.webp', '.svg', '.gif', '.bmp', '.ico'])

function isImagePath(path: string): boolean {
  const dot = path.lastIndexOf('.')
  if (dot === -1) return false
  const ext = path.slice(dot).toLowerCase().split(/[?#]/)[0]
  return IMAGE_EXTENSIONS.has(ext)
}

function isLocalPath(path: string): boolean {
  if (/^[a-zA-Z]:[/\\\\]/.test(path)) return true
  if (path.startsWith('file://')) return true
  if (/^\/(?:Users|home|tmp|var|private|opt|etc|mnt)\b/i.test(path)) return true
  // 委托注册表：只要是任何已注册插件声明支持的后缀，统一视为工件路径
  return artifactViewerRegistry.isSupportedExtension(path)
}

function safeUrl(value: string | undefined): string | undefined {
  if (!value) return undefined
  const cleaned = cleanHref(value)
  if (/^(?:https?:|mailto:)/i.test(cleaned)) return cleaned
  if (cleaned.startsWith('//')) return undefined
  if (cleaned.startsWith('/') || cleaned.startsWith('#')) return cleaned
  return undefined
}

/**
 * 自动感知纯文本中属于已注册插件的工件文件名，并转译为 Markdown 链接
 */
function autoLinkArtifacts(text: string): string {
  const extRegex = artifactViewerRegistry.getExtensionPattern()
  const lines = text.split('\n')
  let inCodeBlock = false

  const processed = lines.map((line) => {
    if (/^\s*(```|~~~)/.test(line)) {
      inCodeBlock = !inCodeBlock
      return line
    }
    if (inCodeBlock) return line

    return line.replace(extRegex, (match, fileName, offset, fullStr) => {
      const before = fullStr.slice(Math.max(0, offset - 2), offset)
      const after = fullStr.slice(offset + match.length, offset + match.length + 2)
      if (
        before.endsWith('](') ||
        before.endsWith('(<') ||
        before.endsWith('(') ||
        before.endsWith('`') ||
        after.startsWith('`') ||
        after.startsWith('>)') ||
        after.startsWith(')') ||
        before.endsWith('[')
      ) {
        return match
      }
      const charBefore = offset > 0 ? fullStr[offset - 1] : ''
      const charAfter = offset + match.length < fullStr.length ? fullStr[offset + match.length] : ''
      if (charBefore === '`' || charAfter === '`') return match

      return `[${fileName}](${fileName})`
    })
  })

  return processed.join('\n')
}

export function MarkdownContent({
  value,
  onImageClick,
  session,
}: {
  value: string
  onImageClick?: (url: string) => void
  session?: Session | null
}) {
  const defaultSession = useAstrorderStore((state) => Object.values(state.sessions)[0] as Session | undefined)
  const currentSession = session || defaultSession
  const openArtifact = useSidecarStore((state) => state.openArtifact)
  const normalizedValue = autoLinkArtifacts(value)

  const resolvePathWithWorkspace = (raw: string): string => {
    const unquoted = decodeURIComponent(raw.trim().replace(/^<|>$/g, ''))
    if (/^https?:\/\//i.test(unquoted)) {
      try {
        const u = new URL(unquoted)
        return decodeURIComponent(u.pathname.replace(/^\/abs\/path\//, ''))
      } catch {
        return unquoted
      }
    }
    if (currentSession?.workspace && !/^[a-zA-Z]:[/\\]/.test(unquoted) && !unquoted.startsWith('/')) {
      const sep = currentSession.workspace.includes('\\') ? '\\' : '/'
      return `${currentSession.workspace}${sep}${unquoted}`
    }
    return unquoted
  }

  const handleOpenLocalFile = async (path: string) => {
    const fullPath = resolvePathWithWorkspace(path)
    if (currentSession) {
      const artifact = resolveArtifactFromPath(fullPath, currentSession)
      const viewer = artifactViewerRegistry.findViewer(artifact)
      if (viewer) {
        openArtifact(artifact, viewer.id)
        return
      }
    }

    try {
      const res = await fetch('/api/v1/system/open-file', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: fullPath }),
      })
      if (!res.ok) throw new Error('打开文件失败')
    } catch {
      window.open(`/api/v1/files/raw?path=${encodeURIComponent(fullPath)}&download=1`, '_blank')
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

            if (isLocalPath(cleaned)) {
              if (isImagePath(cleaned)) {
                const fullPath = resolvePathWithWorkspace(cleaned)
                const rawUrl = `/api/v1/files/raw?path=${encodeURIComponent(fullPath)}`
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

              // 委托注册表：动态获取匹配的 Viewer 及其自声明图标与标签
              const dummyArtifact = currentSession ? resolveArtifactFromPath(cleaned, currentSession) : null
              const matchedViewer = dummyArtifact ? artifactViewerRegistry.findViewer(dummyArtifact) : null

              const Icon = matchedViewer ? matchedViewer.icon : IconFileText
              const badgeText = matchedViewer ? matchedViewer.badgeLabel || '[工件]' : '[打开]'
              const actionTitle = matchedViewer ? `在右侧打开${matchedViewer.title}：${cleaned}` : `在本地打开：${cleaned}`
              const linkClass = matchedViewer
                ? `markdown-local-link markdown-file-link markdown-${matchedViewer.id.replace('-viewer', '')}-link`
                : 'markdown-local-link markdown-file-link'

              return (
                <button
                  type="button"
                  className={linkClass}
                  onClick={() => void handleOpenLocalFile(cleaned)}
                  title={actionTitle}
                >
                  <Icon size={13} aria-hidden="true" />
                  <span>{children}</span>
                  <span style={{ fontSize: 11, opacity: 0.75, marginLeft: 4 }}>{badgeText}</span>
                </button>
              )
            }

            const safe = safeUrl(href)
            if (!safe) return <>{children}</>

            // 如果链接是 http/https 但指向注册插件所支持的扩展名，由注册表动态决策拦截
            const isArtifactUrl = artifactViewerRegistry.isSupportedExtension(safe)
            if (isArtifactUrl && currentSession) {
              const dummy = resolveArtifactFromPath(safe, currentSession)
              const matched = artifactViewerRegistry.findViewer(dummy)
              const Icon = matched ? matched.icon : IconFileText
              const badge = matched ? matched.badgeLabel || '[工件]' : '[工件]'

              return (
                <button
                  type="button"
                  className="markdown-local-link markdown-file-link"
                  onClick={() => void handleOpenLocalFile(safe)}
                  title={`在工作台打开：${safe}`}
                >
                  <Icon size={13} aria-hidden="true" />
                  <span>{children}</span>
                  <span style={{ fontSize: 11, opacity: 0.75, marginLeft: 4 }}>{badge}</span>
                </button>
              )
            }

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
              const fullPath = resolvePathWithWorkspace(cleaned)
              src = `/api/v1/files/raw?path=${encodeURIComponent(fullPath)}`
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
        {normalizedValue}
      </ReactMarkdown>
    </div>
  )
}
