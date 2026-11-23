import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import {
  IconArrowUpRight,
  IconCheck,
  IconCopy,
  IconFile,
  IconFileCode,
  IconFileDescription,
  IconPhoto,
} from '@tabler/icons-react'
import { authenticatedApiFetch } from '../api/client'
import { highlightCode } from './markdownHighlight'

export interface FileHoverTarget {
  rect: { left: number; right: number; top: number; bottom: number; width: number; height: number }
  path: string
  displayName: string
  readUrl: string
  isImage: boolean
  isNonPreviewable: boolean
  line?: number
  endLine?: number
}

interface CacheEntry {
  expires: number
  text?: string
  binary?: boolean
  truncatedBytes?: boolean
  error?: string
}

const PREVIEW_CACHE_MAX = 40
const PREVIEW_CACHE_TTL_MS = 30_000
const MAX_PREVIEW_BYTES = 384 * 1024
const previewCache = new Map<string, CacheEntry>()

const EXT_TO_HIGHLIGHT_LANG: Record<string, string> = {
  go: 'go',
  ts: 'ts',
  tsx: 'tsx',
  js: 'js',
  jsx: 'jsx',
  mjs: 'js',
  cjs: 'js',
  py: 'py',
  json: 'json',
  jsonc: 'json',
  yaml: 'yaml',
  yml: 'yaml',
  toml: 'toml',
  ini: 'ini',
  env: 'env',
  conf: 'conf',
  cfg: 'conf',
  sh: 'sh',
  bash: 'bash',
  zsh: 'zsh',
  ps1: 'ps1',
  bat: 'bat',
  sql: 'sql',
  css: 'css',
  scss: 'scss',
  less: 'css',
  html: 'html',
  xml: 'xml',
  svg: 'xml',
  rs: 'rust',
  c: 'c',
  cpp: 'cpp',
  h: 'c',
  java: 'java',
  proto: 'proto',
  diff: 'diff',
  patch: 'diff',
}

function fileExt(path: string): string {
  const clean = path.toLowerCase().split(/[?#]/)[0]
  const base = clean.split(/[\\/]/).pop() || clean
  if (base === '.env' || base.startsWith('.env.')) return 'env'
  const dot = base.lastIndexOf('.')
  return dot === -1 ? '' : base.slice(dot + 1)
}

function isMarkdownPath(path: string): boolean {
  const ext = fileExt(path)
  return ext === 'md' || ext === 'markdown'
}

function setCachedPreview(key: string, entry: CacheEntry) {
  if (previewCache.size >= PREVIEW_CACHE_MAX) {
    const oldestKey = previewCache.keys().next().value
    if (oldestKey !== undefined) previewCache.delete(oldestKey)
  }
  previewCache.set(key, entry)
}

function decodePreviewBytes(bytes: Uint8Array): { text?: string; binary?: boolean } {
  const utf16 =
    bytes.length >= 2 && bytes[0] === 255 && bytes[1] === 254
      ? 'utf-16le'
      : bytes.length >= 2 && bytes[0] === 254 && bytes[1] === 255
        ? 'utf-16be'
        : null
  try {
    const text = new TextDecoder(utf16 || 'utf-8', { fatal: false }).decode(bytes)
    // Detect binary control characters in sample window
    if (/[\x00-\x08\x0e-\x1f]/.test(text.slice(0, 4096))) {
      return { binary: true }
    }
    return { text }
  } catch {
    return { binary: true }
  }
}

async function readCappedBytes(response: Response, maxBytes: number): Promise<{ bytes: Uint8Array; truncated: boolean }> {
  if (!response.body || typeof response.body.getReader !== 'function') {
    const buf = new Uint8Array(await response.arrayBuffer())
    if (buf.byteLength > maxBytes) {
      return { bytes: buf.subarray(0, maxBytes), truncated: true }
    }
    return { bytes: buf, truncated: false }
  }
  const reader = response.body.getReader()
  const chunks: Uint8Array[] = []
  let total = 0
  let truncated = false
  try {
    while (true) {
      const { done, value } = await reader.read()
      if (done || !value) break
      if (total + value.byteLength > maxBytes) {
        chunks.push(value.subarray(0, Math.max(0, maxBytes - total)))
        total = maxBytes
        truncated = true
        try {
          await reader.cancel()
        } catch {}
        break
      }
      chunks.push(value)
      total += value.byteLength
    }
  } finally {
    try {
      reader.releaseLock()
    } catch {}
  }
  const merged = new Uint8Array(total)
  let offset = 0
  for (const chunk of chunks) {
    merged.set(chunk, offset)
    offset += chunk.byteLength
  }
  return { bytes: merged, truncated }
}

function shortParentPath(fullPath: string): string {
  const norm = fullPath.replace(/\\/g, '/')
  const parts = norm.split('/').filter(Boolean)
  if (parts.length <= 1) return ''
  const dirParts = parts.slice(0, -1)
  if (dirParts.length <= 3) return dirParts.join('/')
  return `…/${dirParts.slice(-3).join('/')}`
}

export function FileLinkHoverPreview({
  target,
  onMouseEnter,
  onMouseLeave,
  onOpen,
  onClose,
}: {
  target: FileHoverTarget | null
  onMouseEnter: () => void
  onMouseLeave: () => void
  onOpen: () => void
  onClose: () => void
}) {
  const cardRef = useRef<HTMLDivElement>(null)
  const [coords, setCoords] = useState<{
    left: number
    top?: number
    bottom?: number
    maxHeight: number
    placement: 'below' | 'above'
  } | null>(null)
  const [state, setState] = useState<{
    loading: boolean
    text?: string
    binary?: boolean
    truncatedBytes?: boolean
    error?: string
  }>({ loading: false })
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    setCopied(false)
    if (!target || target.isImage || target.isNonPreviewable || !target.readUrl) {
      setState({ loading: false })
      return
    }

    const cached = previewCache.get(target.readUrl)
    if (cached && cached.expires > Date.now()) {
      setState({
        loading: false,
        text: cached.text,
        binary: cached.binary,
        truncatedBytes: cached.truncatedBytes,
        error: cached.error,
      })
      return
    }

    const controller = new AbortController()
    let active = true
    setState({ loading: true })

    void authenticatedApiFetch(target.readUrl, { signal: controller.signal })
      .then(async (res) => {
        if (!res.ok) throw new Error(`无法读取文件 (${res.status})`)
        const { bytes, truncated } = await readCappedBytes(res, MAX_PREVIEW_BYTES)
        const decoded = decodePreviewBytes(bytes)
        const entry: CacheEntry = {
          expires: Date.now() + PREVIEW_CACHE_TTL_MS,
          text: decoded.text,
          binary: decoded.binary,
          truncatedBytes: truncated,
        }
        setCachedPreview(target.readUrl, entry)
        if (active) {
          setState({
            loading: false,
            text: entry.text,
            binary: entry.binary,
            truncatedBytes: entry.truncatedBytes,
          })
        }
      })
      .catch((err: unknown) => {
        if (!active || (err instanceof Error && err.name === 'AbortError')) return
        const msg = err instanceof Error ? err.message : '读取预览失败'
        setState({ loading: false, error: msg })
      })

    return () => {
      active = false
      controller.abort()
    }
  }, [target])

  useEffect(() => {
    if (!target) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [onClose, target])

  useLayoutEffect(() => {
    if (!target) {
      setCoords(null)
      return
    }
    const vw = window.innerWidth
    const vh = window.innerHeight
    const edgeMargin = 12
    const gap = 8
    const desiredHeight = 360
    const cardWidth = Math.min(540, vw - edgeMargin * 2)
    const spaceBelow = Math.max(0, vh - target.rect.bottom - edgeMargin - gap)
    const spaceAbove = Math.max(0, target.rect.top - edgeMargin - gap)
    const placement: 'below' | 'above' =
      spaceBelow >= desiredHeight || spaceBelow >= spaceAbove ? 'below' : 'above'

    const left = Math.max(edgeMargin, Math.min(target.rect.left, vw - cardWidth - edgeMargin))
    if (placement === 'above') {
      setCoords({
        left,
        bottom: Math.max(edgeMargin, vh - target.rect.top + gap),
        maxHeight: Math.max(160, Math.min(desiredHeight, spaceAbove)),
        placement,
      })
    } else {
      setCoords({
        left,
        top: Math.max(edgeMargin, target.rect.bottom + gap),
        maxHeight: Math.max(160, Math.min(desiredHeight, spaceBelow)),
        placement,
      })
    }
  }, [target])

  const computedSlice = useMemo(() => {
    if (!target || !state.text) return null
    const isMd = isMarkdownPath(target.path) && !target.line
    if (isMd) {
      const maxChars = 6000
      const truncated = state.text.length > maxChars || Boolean(state.truncatedBytes)
      return {
        mode: 'markdown' as const,
        markdown: state.text.slice(0, maxChars),
        rawCopy: state.text.slice(0, maxChars),
        truncated,
        totalLines: state.text.split(/\r?\n/).length,
      }
    }

    const allLines = state.text.split(/\r?\n/)
    const totalLines = allLines.length
    const ext = fileExt(target.path)
    const lang = EXT_TO_HIGHLIGHT_LANG[ext] || ''

    let startLine = 1
    let endLine = Math.min(totalLines, 50)
    if (target.line && target.line > 0) {
      const targetEnd = target.endLine && target.endLine >= target.line ? target.endLine : target.line
      startLine = Math.max(1, target.line - 4)
      endLine = Math.min(totalLines, Math.max(targetEnd + 10, startLine + 34))
    }

    const lines = allLines.slice(startLine - 1, endLine)
    return {
      mode: 'code' as const,
      lang,
      startLine,
      endLine,
      totalLines,
      lines,
      rawCopy: lines.join('\n'),
      truncated: endLine < totalLines || startLine > 1 || Boolean(state.truncatedBytes),
    }
  }, [state.text, state.truncatedBytes, target])

  if (!target || typeof document === 'undefined') return null

  const lineBadge = target.line
    ? target.endLine && target.endLine !== target.line
      ? `L${target.line}-${target.endLine}`
      : `L${target.line}`
    : ''
  const parentDir = shortParentPath(target.path)
  const HeadIcon = target.isImage
    ? IconPhoto
    : isMarkdownPath(target.path)
      ? IconFileDescription
      : target.isNonPreviewable
        ? IconFile
        : IconFileCode

  const handleCopySnippet = () => {
    const text = computedSlice?.rawCopy
    if (!text) return
    void navigator.clipboard.writeText(text)
    setCopied(true)
    setTimeout(() => setCopied(false), 1400)
  }

  return createPortal(
    <div
      ref={cardRef}
      className={`file-hover-card is-${coords?.placement || 'below'}`}
      role="dialog"
      aria-label={`文件预览：${target.displayName}`}
      style={{
        left: coords ? `${coords.left}px` : '-9999px',
        top: coords?.top !== undefined ? `${coords.top}px` : undefined,
        bottom: coords?.bottom !== undefined ? `${coords.bottom}px` : undefined,
        maxHeight: coords ? `${coords.maxHeight}px` : undefined,
        visibility: coords ? 'visible' : 'hidden',
      }}
      onMouseEnter={onMouseEnter}
      onMouseLeave={onMouseLeave}
    >
      <header className="file-hover-head">
        <div className="file-hover-ident" title={target.path}>
          <HeadIcon size={14} aria-hidden="true" />
          <strong className="file-hover-name">{target.displayName}</strong>
          {lineBadge && <span className="file-hover-line-badge">{lineBadge}</span>}
          {parentDir && <span className="file-hover-dir">{parentDir}</span>}
        </div>
        <div className="file-hover-actions">
          {computedSlice?.rawCopy && (
            <button
              type="button"
              className="file-hover-btn"
              onClick={handleCopySnippet}
              aria-label="复制预览内容"
              title="复制当前预览片段"
            >
              {copied ? <IconCheck size={12} /> : <IconCopy size={12} />}
              <span>{copied ? '已复制' : '复制'}</span>
            </button>
          )}
          <button
            type="button"
            className="file-hover-btn is-primary"
            onClick={() => {
              onClose()
              onOpen()
            }}
            aria-label="在侧边栏打开文件"
          >
            <span>打开</span>
            <IconArrowUpRight size={12} aria-hidden="true" />
          </button>
        </div>
      </header>

      <div className="file-hover-body">
        {target.isImage ? (
          <div className="file-hover-img-wrap">
            <img
              src={target.readUrl}
              alt={target.displayName}
              className="file-hover-img"
              onClick={() => {
                onClose()
                onOpen()
              }}
            />
          </div>
        ) : target.isNonPreviewable || state.binary ? (
          <div className="file-hover-empty">
            <p>该文件为二进制或非纯文本格式，无法直接悬浮预览。</p>
            <small>{target.path}</small>
          </div>
        ) : state.loading ? (
          <div className="file-hover-empty">正在读取文件内容…</div>
        ) : state.error ? (
          <div className="file-hover-empty is-error">{state.error}</div>
        ) : computedSlice?.mode === 'markdown' ? (
          <div className="file-hover-md markdown-content">
            <ReactMarkdown remarkPlugins={[remarkGfm]} skipHtml>
              {computedSlice.markdown}
            </ReactMarkdown>
          </div>
        ) : computedSlice?.mode === 'code' ? (
          <div className="file-hover-code">
            {computedSlice.lines.map((lineText, idx) => {
              const lineNo = computedSlice.startLine + idx
              const targetEnd = target.endLine && target.endLine >= (target.line || 0) ? target.endLine : target.line
              const isTarget = Boolean(target.line && lineNo >= target.line && lineNo <= (targetEnd || target.line))
              return (
                <div
                  key={lineNo}
                  className={`file-hover-code-row${isTarget ? ' is-target' : ''}`}
                >
                  <span className="file-hover-gutter" aria-hidden="true">{lineNo}</span>
                  <span className="file-hover-code-text">
                    {lineText ? (computedSlice.lang ? highlightCode(lineText, computedSlice.lang) : lineText) : ' '}
                  </span>
                </div>
              )
            })}
          </div>
        ) : (
          <div className="file-hover-empty">空文件</div>
        )}
      </div>

      {computedSlice?.truncated && (
        <footer className="file-hover-foot">
          <span>
            {computedSlice.mode === 'code'
              ? `第 ${computedSlice.startLine}–${computedSlice.endLine} 行 / 共 ${computedSlice.totalLines}${state.truncatedBytes ? '+' : ''} 行`
              : `已展示前部内容（共 ${computedSlice.totalLines}${state.truncatedBytes ? '+' : ''} 行）`}
          </span>
          <button
            type="button"
            className="file-hover-more"
            onClick={() => {
              onClose()
              onOpen()
            }}
          >
            在侧边栏查看完整文件 →
          </button>
        </footer>
      )}
    </div>,
    document.body,
  )
}
