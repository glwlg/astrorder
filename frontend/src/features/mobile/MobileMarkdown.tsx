import { isValidElement, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import ReactMarkdown, { type Components } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import {
  IconAlertCircle,
  IconAlertTriangle,
  IconBulb,
  IconFlame,
  IconInfoCircle,
} from '@tabler/icons-react'
import { highlightCode, parseCalloutBlockquote, type CalloutKind } from '../../components/markdownHighlight'
import { extractMobileMarkdownImageUrls, isMobileImageFilePath, isMobileLocalFilePath, mobileFileUrl, resolveMobileFilePath } from './mobileFilePath'

const CALLOUT_ICONS: Record<CalloutKind, typeof IconInfoCircle> = {
  note: IconInfoCircle,
  tip: IconBulb,
  important: IconAlertCircle,
  warning: IconAlertTriangle,
  caution: IconFlame,
}

function CodeBlock({ children }: { children?: ReactNode }) {
  const element = useRef<HTMLPreElement>(null)
  const copiedTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const [wrap, setWrap] = useState(false)
  const [copied, setCopied] = useState(false)

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

  return (
    <div className="m-code-block">
      <div className="m-code-actions">
        {language ? <span className="m-code-lang">{language.toUpperCase()}</span> : <span />}
        <div className="m-code-btns">
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

export function MobileMarkdown({ value, onFileClick, onImageClick, workspace, sessionId, connectionId }: {
  value: string
  onFileClick?: (path: string) => void
  onImageClick?: (url: string, gallery?: string[]) => void
  workspace?: string | null
  sessionId?: string
  connectionId?: string | null
}) {
  const galleryRef = useRef<string[]>([])
  galleryRef.current = useMemo(
    () => extractMobileMarkdownImageUrls(value, workspace, sessionId, connectionId),
    [value, workspace, sessionId, connectionId],
  )
  const callbacks = useRef({ onFileClick, onImageClick })
  callbacks.current = { onFileClick, onImageClick }
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
    pre: ({ children }) => <CodeBlock>{children}</CodeBlock>,
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
      if (isLocal && (onImageClick || onFileClick)) {
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
  }), [workspace, sessionId, connectionId, Boolean(onFileClick), Boolean(onImageClick)])
  return <div className="markdown-content"><ReactMarkdown remarkPlugins={[remarkGfm]} skipHtml urlTransform={url => url} components={components}>{value}</ReactMarkdown></div>
}
