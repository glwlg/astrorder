import { isValidElement, memo, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import ReactMarkdown, { type Components } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import {
  IconAlertCircle,
  IconAlertTriangle,
  IconBulb,
  IconCheck,
  IconCode,
  IconCopy,
  IconFlame,
  IconInfoCircle,
  IconMaximize,
  IconMovie,
  IconSitemap,
} from '@tabler/icons-react'
import { DiagramLightboxModal } from '../../components/DiagramLightboxModal'
import { highlightCode, parseCalloutBlockquote, type CalloutKind } from '../../components/markdownHighlight'
import { extractSvgIntrinsicSize } from '../../components/PanZoomCanvas'
import { renderMermaid } from '../../components/renderMermaid'
import '../../components/panZoomCanvas.css'
import { isSqlLanguage, useHasDatabaseTool } from '../sidecar/databaseTool'
import { extractMobileMarkdownImageUrls, isMobileImageFilePath, isMobileLocalFilePath, isMobileVideoFilePath, mobileFileUrl, resolveMobileFilePath } from './mobileFilePath'

const CALLOUT_ICONS: Record<CalloutKind, typeof IconInfoCircle> = {
  note: IconInfoCircle,
  tip: IconBulb,
  important: IconAlertCircle,
  warning: IconAlertTriangle,
  caution: IconFlame,
}

type MermaidColorScheme = 'light' | 'dark'

const MERMAID_MAX_CACHE = 64
const mobileMermaidSvgCache = new Map<string, string>()
const mobileMermaidPending = new Map<string, Promise<string>>()

export function clearMobileMermaidCache(): void {
  mobileMermaidSvgCache.clear()
  mobileMermaidPending.clear()
}

function readDocumentColorScheme(): MermaidColorScheme {
  if (typeof document === 'undefined') return 'light'
  return document.documentElement.getAttribute('data-mantine-color-scheme') === 'dark' ? 'dark' : 'light'
}

function useDocumentColorScheme(): MermaidColorScheme {
  const [scheme, setScheme] = useState<MermaidColorScheme>(readDocumentColorScheme)
  useEffect(() => {
    if (typeof document === 'undefined' || typeof MutationObserver === 'undefined') return
    const root = document.documentElement
    const sync = () => setScheme(readDocumentColorScheme())
    sync()
    const observer = new MutationObserver(sync)
    observer.observe(root, { attributes: true, attributeFilter: ['data-mantine-color-scheme'] })
    return () => observer.disconnect()
  }, [])
  return scheme
}

function withMermaidTheme(code: string, scheme: MermaidColorScheme): string {
  if (scheme !== 'dark') return code
  if (/^\s*(?:%%\{init:|---)/.test(code)) return code
  return `%%{init: {"theme": "dark"}}%%\n${code}`
}

function cacheMobileMermaidSvg(cacheKey: string, svg: string): void {
  if (mobileMermaidSvgCache.has(cacheKey)) {
    mobileMermaidSvgCache.delete(cacheKey)
  } else if (mobileMermaidSvgCache.size >= MERMAID_MAX_CACHE) {
    const oldestKey = mobileMermaidSvgCache.keys().next().value
    if (oldestKey !== undefined) mobileMermaidSvgCache.delete(oldestKey)
  }
  mobileMermaidSvgCache.set(cacheKey, svg)
}

function renderMobileMermaid(code: string, scheme: MermaidColorScheme): Promise<string> {
  const cacheKey = `${scheme}:${code}`
  const cached = mobileMermaidSvgCache.get(cacheKey)
  if (cached) return Promise.resolve(cached)

  const pending = mobileMermaidPending.get(cacheKey)
  if (pending) return pending

  const themedCode = withMermaidTheme(code, scheme)
  const task = renderMermaid(themedCode)
    .catch((err) => {
      if (themedCode !== code) {
        return renderMermaid(code)
      }
      throw err
    })
    .then((result) => {
      const rendered = result?.svg?.trim() || ''
      if (!rendered) {
        throw new Error('Mermaid 返回空图表')
      }
      cacheMobileMermaidSvg(cacheKey, rendered)
      return rendered
    })
    .finally(() => {
      mobileMermaidPending.delete(cacheKey)
    })

  mobileMermaidPending.set(cacheKey, task)
  return task
}

const MERMAID_BLOCK_START_RE = /^(?:flowchart|graph|sequenceDiagram|classDiagram|stateDiagram(?:-v2)?|erDiagram|gantt|pie|mindmap|timeline|gitGraph|journey)\b/

function extractMermaidSource(children: ReactNode): string | null {
  const child = isValidElement<{ className?: string; children?: ReactNode }>(children) ? children : null
  if (!child) return null
  const code = String(child.props.children || '').replace(/\n$/, '').trim()
  if (!code) return null
  const className = child.props.className || ''
  if (/(?:^|\s)language-(?:mermaid|mmd)(?:\s|$)/i.test(className)) {
    return code
  }
  const hasOtherLanguage = /(?:^|\s)language-[a-zA-Z0-9_-]+(?:\s|$)/.test(className)
  if (!hasOtherLanguage && MERMAID_BLOCK_START_RE.test(code)) {
    return code
  }
  return null
}

const MobileMermaidCard = memo(function MobileMermaidCard({ code }: { code: string }) {
  const colorScheme = useDocumentColorScheme()
  const cacheKey = `${colorScheme}:${code}`
  const [svg, setSvg] = useState<string>(() => mobileMermaidSvgCache.get(cacheKey) ?? '')
  const [failed, setFailed] = useState(false)
  const [showSource, setShowSource] = useState(false)
  const [lightboxOpen, setLightboxOpen] = useState(false)
  const [copied, setCopied] = useState(false)
  const copiedTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => () => {
    if (copiedTimer.current !== null) clearTimeout(copiedTimer.current)
  }, [])

  useEffect(() => {
    let active = true
    const cached = mobileMermaidSvgCache.get(cacheKey)
    if (cached) {
      setSvg(cached)
      setFailed(false)
      return () => { active = false }
    }
    setFailed(false)
    void renderMobileMermaid(code, colorScheme)
      .then((rendered) => {
        if (!active) return
        setSvg(rendered)
        setFailed(false)
      })
      .catch(() => {
        if (!active) return
        setSvg('')
        setFailed(true)
      })
    return () => { active = false }
  }, [cacheKey, code, colorScheme])

  const intrinsicSize = useMemo(() => extractSvgIntrinsicSize(svg), [svg])

  const handleCopySource = async () => {
    try {
      await navigator.clipboard.writeText(code)
      setCopied(true)
      if (copiedTimer.current !== null) clearTimeout(copiedTimer.current)
      copiedTimer.current = setTimeout(() => {
        copiedTimer.current = null
        setCopied(false)
      }, 1800)
    } catch {
      setCopied(false)
    }
  }

  return (
    <>
      <div className="m-mermaid-card" aria-label="Mermaid 图表">
        <div className="m-mermaid-toolbar" role="toolbar" aria-label="图表操作">
          <span className="m-mermaid-badge">
            <IconSitemap size={13} aria-hidden="true" />
            <span>Mermaid</span>
          </span>
          <div className="m-mermaid-actions">
            {!failed && (
              <button
                type="button"
                className="m-mermaid-btn is-primary"
                disabled={!svg}
                title="全屏放大查看（支持双指缩放与拖拽）"
                onClick={() => {
                  if (svg) setLightboxOpen(true)
                }}
              >
                <IconMaximize size={13} aria-hidden="true" />
                <span>全屏放大</span>
              </button>
            )}
            {!failed && (
              <button
                type="button"
                className={`m-mermaid-btn${showSource ? ' is-active' : ''}`}
                aria-pressed={showSource}
                title={showSource ? '切换为图表视图' : '切换为源码视图'}
                onClick={() => setShowSource((prev) => !prev)}
              >
                {showSource ? <IconSitemap size={13} aria-hidden="true" /> : <IconCode size={13} aria-hidden="true" />}
                <span>{showSource ? '图表' : '源码'}</span>
              </button>
            )}
            <button
              type="button"
              className="m-mermaid-btn"
              title="复制 Mermaid 源码"
              onClick={() => void handleCopySource()}
            >
              {copied ? <IconCheck size={13} aria-hidden="true" /> : <IconCopy size={13} aria-hidden="true" />}
              <span>{copied ? '已复制' : '复制源码'}</span>
            </button>
          </div>
        </div>

        {failed ? (
          <>
            <div className="m-mermaid-error" role="status">图表渲染失败，已回退显示源码</div>
            <pre className="m-mermaid-source"><code>{code}</code></pre>
          </>
        ) : showSource ? (
          <pre className="m-mermaid-source"><code>{code}</code></pre>
        ) : svg ? (
          <div
            className="m-mermaid-stage"
            role="button"
            tabIndex={0}
            aria-label="点击全屏放大查看 Mermaid 图表"
            title="点击全屏放大查看（支持双指缩放与拖拽）"
            data-intrinsic-width={intrinsicSize?.width}
            data-intrinsic-height={intrinsicSize?.height}
            onClick={() => setLightboxOpen(true)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault()
                setLightboxOpen(true)
              }
            }}
          >
            <div className="m-mermaid-svg" dangerouslySetInnerHTML={{ __html: svg }} />
            <span className="m-mermaid-zoom-hint" aria-hidden="true">
              <IconMaximize size={11} />
              <span>轻点全屏缩放</span>
            </span>
          </div>
        ) : (
          <div className="m-mermaid-loading" role="status">正在渲染图表…</div>
        )}
      </div>

      {lightboxOpen && svg && (
        <DiagramLightboxModal
          opened={lightboxOpen}
          onClose={() => setLightboxOpen(false)}
          title="Mermaid 架构图"
          svg={svg}
        />
      )}
    </>
  )
})

function CodeBlock({ children, onSqlOpen }: { children?: ReactNode; onSqlOpen?: (sql: string) => void }) {
  const element = useRef<HTMLPreElement>(null)
  const copiedTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const [wrap, setWrap] = useState(false)
  const [copied, setCopied] = useState(false)
  const hasDatabaseTool = useHasDatabaseTool()

  useEffect(() => () => {
    if (copiedTimer.current !== null) clearTimeout(copiedTimer.current)
  }, [])

  let language = ''
  let rawText = ''
  if (isValidElement<{ className?: string; children?: ReactNode }>(children)) {
    const match = (children.props.className || '').match(/language-([a-zA-Z0-9_-]+)/)
    if (match) language = match[1]
    if (typeof children.props.children === 'string') rawText = children.props.children
  }
  const highlighted = rawText && language ? highlightCode(rawText, language) : null
  const isSqlBlock = Boolean(language && isSqlLanguage(language))

  return (
    <div className="m-code-block">
      <div className="m-code-actions">
        {language ? <span className="m-code-lang">{language.toUpperCase()}</span> : <span />}
        <div className="m-code-btns">
          {isSqlBlock && hasDatabaseTool && onSqlOpen && (
            <button
              type="button"
              onClick={() => {
                const sql = element.current?.textContent || rawText
                if (sql.trim()) onSqlOpen(sql)
              }}
            >
              数据库打开
            </button>
          )}
          <button type="button" onClick={() => setWrap(!wrap)}>{wrap ? '取消换行' : '换行'}</button>
          <button
            type="button"
            onClick={async () => {
              try {
                await navigator.clipboard.writeText(element.current?.textContent || rawText)
                setCopied(true)
                if (copiedTimer.current !== null) clearTimeout(copiedTimer.current)
                copiedTimer.current = setTimeout(() => {
                  copiedTimer.current = null
                  setCopied(false)
                }, 1800)
              } catch {
                setCopied(false)
              }
            }}
          >
            {copied ? '已复制' : '复制'}
          </button>
        </div>
      </div>
      <pre ref={element} style={{ whiteSpace: wrap ? 'pre-wrap' : 'pre', wordBreak: wrap ? 'break-all' : 'normal' }}>
        {isValidElement<{ children?: ReactNode }>(children) ? (
          <code {...children.props}>{highlighted ?? children.props.children}</code>
        ) : (
          children
        )}
      </pre>
    </div>
  )
}

export function MobileMarkdown({ value, onFileClick, onImageClick, onVideoClick, onSqlOpen, workspace, sessionId, connectionId }: {
  value: string
  onFileClick?: (path: string) => void
  onImageClick?: (url: string, gallery?: string[]) => void
  onVideoClick?: (url: string, name: string) => void
  onSqlOpen?: (sql: string) => void
  workspace?: string | null
  sessionId?: string
  connectionId?: string | null
}) {
  const galleryRef = useRef<string[]>([])
  galleryRef.current = useMemo(
    () => extractMobileMarkdownImageUrls(value, workspace, sessionId, connectionId),
    [value, workspace, sessionId, connectionId],
  )
  const callbacks = useRef({ onFileClick, onImageClick, onVideoClick, onSqlOpen })
  callbacks.current = { onFileClick, onImageClick, onVideoClick, onSqlOpen }
  const openImage = (targetUrl: string) => {
    const gallery = galleryRef.current
    if (gallery.length > 1) {
      const normalized = gallery.includes(targetUrl) ? gallery : [targetUrl, ...gallery]
      callbacks.current.onImageClick?.(targetUrl, normalized)
    } else {
      callbacks.current.onImageClick?.(targetUrl)
    }
  }
  const components = useMemo<Components>(() => ({
    pre: ({ children }) => {
      const mermaidCode = extractMermaidSource(children)
      if (mermaidCode) {
        return <MobileMermaidCard code={mermaidCode} />
      }
      return (
        <CodeBlock onSqlOpen={onSqlOpen ? (sql) => callbacks.current.onSqlOpen?.(sql) : undefined}>
          {children}
        </CodeBlock>
      )
    },
    blockquote: ({ children }) => {
      const callout = parseCalloutBlockquote(children)
      if (callout) {
        const Icon = CALLOUT_ICONS[callout.kind]
        return (
          <aside className={`markdown-callout is-${callout.kind}`} role="note">
            <div className="markdown-callout-title">
              <Icon size={14} aria-hidden="true" />
              <span>{callout.label}</span>
            </div>
            <div className="markdown-callout-body">{callout.children}</div>
          </aside>
        )
      }
      return <blockquote>{children}</blockquote>
    },
    a: ({ href, children }) => {
      const url = href || ''
      const isLocal = isMobileLocalFilePath(url)
      if (isLocal && (onImageClick || onVideoClick || onFileClick)) {
        const resolvedPath = resolveMobileFilePath(url, workspace)
        const rawUrl = mobileFileUrl(url, workspace, sessionId, connectionId)
        if (onImageClick && isMobileImageFilePath(resolvedPath)) {
          return (
            <a
              className="m-file-pill is-image"
              href={rawUrl}
              onClick={(e) => {
                e.preventDefault()
                openImage(rawUrl)
              }}
            >
              {children}
            </a>
          )
        }
        if (onVideoClick && isMobileVideoFilePath(resolvedPath)) {
          const videoName = resolvedPath.split(/[\\/]/).pop() || 'video.mp4'
          return (
            <a
              className="m-file-pill is-video"
              href={rawUrl}
              onClick={(e) => {
                e.preventDefault()
                callbacks.current.onVideoClick?.(rawUrl, videoName)
              }}
            >
              <IconMovie size={13} aria-hidden="true" style={{ flexShrink: 0 }} />
              <span>{children}</span>
            </a>
          )
        }
        if (onFileClick) {
          return (
            <a
              className="m-file-pill"
              href={rawUrl}
              onClick={(e) => {
                e.preventDefault()
                callbacks.current.onFileClick?.(resolvedPath)
              }}
            >
              {children}
            </a>
          )
        }
      }
      return /^(?:https?:\/\/|mailto:|\/api\/v1\/)/i.test(url)
        ? <a href={url} target="_blank" rel="noopener noreferrer">{children}</a>
        : <>{children}</>
    },
    img: ({ src, alt }) => {
      const url = src && isMobileLocalFilePath(src)
        ? mobileFileUrl(src, workspace, sessionId, connectionId)
        : src
      if (!url || !/^(?:https?:\/\/|\/api\/v1\/)/i.test(url)) return null
      if (onImageClick) {
        return <button type="button" style={{ border: 'none', background: 'none', padding: 0, display: 'block' }} onClick={() => openImage(url)}><img src={url} alt={alt || ''} loading="lazy" style={{ maxWidth: '100%', borderRadius: 8, marginTop: 8, marginBottom: 8, display: 'block' }} /></button>
      }
      return <img src={url} alt={alt || ''} loading="lazy" style={{ maxWidth: '100%', borderRadius: 8, marginTop: 8, marginBottom: 8, display: 'block' }} />
    },
    table: ({ children }) => <div className="m-table-wrap"><table>{children}</table></div>,
  }), [workspace, sessionId, connectionId, Boolean(onFileClick), Boolean(onImageClick), Boolean(onVideoClick), Boolean(onSqlOpen)])
  return <div className="markdown-content"><ReactMarkdown remarkPlugins={[remarkGfm]} skipHtml urlTransform={url => url} components={components}>{value}</ReactMarkdown></div>
}
