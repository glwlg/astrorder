import {
  IconAlertCircle,
  IconAlertTriangle,
  IconBraces,
  IconBulb,
  IconCheck,
  IconCopy,
  IconExternalLink,
  IconEye,
  IconFile,
  IconFileCode,
  IconFileDescription,
  IconFlame,
  IconInfoCircle,
  IconPhoto,
  IconTextWrap,
} from '@tabler/icons-react'
import { Anchor } from '@mantine/core'
import { isValidElement, memo, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import type { Session } from '../domain/types'
import { api } from '../api/client'
import { artifactViewerRegistry, isNonPreviewableFile } from '../features/sidecar/registry'
import { videoMediaType } from '../features/sidecar/viewers/video/formats'
import { resolveArtifactFromPath } from '../features/sidecar/resolver'
import { useSidecarStore } from '../features/sidecar/sidecarStore'
import { useAstrorderStore } from '../state/store'
import { renderMermaid } from './renderMermaid'
import { CodexVisualization, extractVisualizations } from './CodexVisualization'
import { MarkdownLinkMenu, type MarkdownLinkMenuTarget } from './MarkdownLinkMenu'
import { FileSourceContext } from '../features/sidecar/FileSourceContext'
import { GroupFileLink } from './GroupFileLink'
import { findGroupFile } from '../features/chat/groupFileSources'
import { highlightCode, parseCalloutBlockquote, type CalloutKind } from './markdownHighlight'
import { FileLinkHoverPreview, type FileHoverTarget } from './FileLinkHoverPreview'

function hashSnippet(value: string): string {
  let hash = 2166136261
  for (let i = 0; i < value.length; i++) {
    hash ^= value.charCodeAt(i)
    hash = Math.imul(hash, 16777619)
  }
  return (hash >>> 0).toString(36)
}

function MermaidDiagram({ code }: { code: string }) {
  const [svg, setSvg] = useState('')
  const [failed, setFailed] = useState(false)
  const openArtifact = useSidecarStore((state) => state.openArtifact)

  useEffect(() => {
    let active = true
    setSvg('')
    setFailed(false)
    void renderMermaid(code).then((result) => {
      if (active) setSvg(result.svg)
    }).catch(() => {
      if (active) setFailed(true)
    })
    return () => { active = false }
  }, [code])

  const openInSidecar = () => {
    const id = `inline-mermaid:${hashSnippet(code)}`
    openArtifact(
      {
        id,
        sessionId: 'inline',
        agentId: '',
        name: 'Mermaid 图表.mmd',
        kind: 'workspace_file',
        readUrl: '',
        mediaType: 'text/vnd.mermaid',
        writable: false,
        metadata: { inlineContent: code },
      },
      'mermaid-viewer',
    )
  }

  if (failed) return <MarkdownCodeBlock><code>{code}</code></MarkdownCodeBlock>
  return (
    <div className="markdown-mermaid" aria-label="Mermaid 图表">
      <button
        type="button"
        className="markdown-canvas-btn markdown-mermaid-canvas-btn"
        onClick={openInSidecar}
        aria-label="在侧边画布查看 Mermaid 图表"
        title="在右侧画布展开与缩放查看"
      >
        <IconEye size={12} aria-hidden="true" />
        <span>画布查看</span>
      </button>
      {svg ? <div dangerouslySetInnerHTML={{ __html: svg }} /> : <span>正在渲染图表…</span>}
    </div>
  )
}

function MarkdownCodeBlock({ children }: { children?: ReactNode }) {
  const [copied, setCopied] = useState(false)
  const [wrap, setWrap] = useState(false)
  const codeRef = useRef<HTMLElement>(null)
  const openArtifact = useSidecarStore((state) => state.openArtifact)

  let language = ''
  let rawText = ''
  if (isValidElement<{ className?: string; children?: ReactNode }>(children)) {
    const match = (children.props.className || '').match(/language-([a-zA-Z0-9_-]+)/)
    if (match) language = match[1]
    if (typeof children.props.children === 'string') rawText = children.props.children
  }
  const lineCount = rawText ? rawText.split('\n').length : 0

  const handleCopy = (e: React.MouseEvent) => {
    e.stopPropagation()
    const text = codeRef.current?.innerText || codeRef.current?.textContent || rawText
    if (!text) return
    void navigator.clipboard.writeText(text)
    setCopied(true)
    setTimeout(() => setCopied(false), 1600)
  }

  const handleOpenInCanvas = (e: React.MouseEvent) => {
    e.stopPropagation()
    const text = codeRef.current?.innerText || codeRef.current?.textContent || rawText
    if (!text.trim()) return
    const ext = language ? `.${language.toLowerCase()}` : '.txt'
    const id = `inline-code:${hashSnippet(text)}`
    openArtifact(
      {
        id,
        sessionId: 'inline',
        agentId: '',
        name: `代码片段${ext}`,
        kind: 'workspace_file',
        readUrl: '',
        mediaType: 'text/plain',
        writable: false,
        metadata: { inlineContent: text },
      },
      'monaco-viewer',
    )
  }

  const renderedCode = useMemo(
    () => (rawText && language ? highlightCode(rawText, language) : null),
    [rawText, language],
  )

  return (
    <div className={`markdown-code-wrapper${wrap ? ' is-wrapped' : ''}`}>
      <div className="markdown-code-header">
        <span className="markdown-code-lang">{(language || 'code').toUpperCase()}</span>
        <div className="markdown-code-actions">
          <button
            type="button"
            className={`markdown-code-copy-btn${wrap ? ' is-active' : ''}`}
            onClick={(e) => {
              e.stopPropagation()
              setWrap((v) => !v)
            }}
            aria-label={wrap ? '关闭自动换行' : '开启自动换行'}
            title={wrap ? '切换为横向滚动' : '切换为自动换行'}
          >
            <IconTextWrap size={12} aria-hidden="true" />
            <span>{wrap ? '滚动' : '换行'}</span>
          </button>
          {lineCount >= 6 && (
            <button
              type="button"
              className="markdown-code-copy-btn markdown-canvas-btn"
              onClick={handleOpenInCanvas}
              aria-label="在侧边画布查看代码"
              title="在右侧 Monaco 画布中对照查看"
            >
              <IconEye size={12} aria-hidden="true" />
              <span>画布</span>
            </button>
          )}
          <button
            type="button"
            className="markdown-code-copy-btn"
            onClick={handleCopy}
            aria-label={copied ? '已复制代码' : '复制代码'}
          >
            {copied ? <IconCheck size={12} color="var(--astr-teal, #12b886)" /> : <IconCopy size={12} />}
            <span>{copied ? '已复制' : '复制'}</span>
          </button>
        </div>
      </div>
      <pre className="markdown-code-block">
        {isValidElement<{ children?: ReactNode }>(children) ? (
          <code {...children.props} ref={codeRef}>
            {renderedCode ?? children.props.children}
          </code>
        ) : (
          <code ref={codeRef}>{children}</code>
        )}
      </pre>
    </div>
  )
}

function mermaidSource(children: ReactNode): string | null {
  const child = isValidElement<{ className?: string; children?: ReactNode }>(children) ? children : null
  if (!child) return null
  const code = String(child.props.children || '').replace(/\n$/, '').trim()
  const language = child.props.className || ''
  const looksLikeMermaid = /^(?:flowchart|graph|sequenceDiagram|classDiagram|stateDiagram(?:-v2)?|erDiagram|gantt|pie|mindmap|timeline|gitGraph|journey)\b/.test(code)
  return /(?:^|\s)language-mermaid(?:\s|$)/.test(language) || looksLikeMermaid ? code : null
}

function MarkdownPre({ children }: { children?: ReactNode }) {
  const code = mermaidSource(children)
  return code ? <MermaidDiagram code={code} /> : <MarkdownCodeBlock>{children}</MarkdownCodeBlock>
}

interface ParsedFileHref {
  path: string
  line?: number
  endLine?: number
}

function extractLineRange(hash: string): { line?: number; endLine?: number } {
  const match = hash.match(/^#?L(\d+)(?:-L?(\d+))?$/i)
  if (!match) return {}
  const line = Number(match[1])
  const endLine = match[2] ? Number(match[2]) : undefined
  if (!Number.isFinite(line) || line <= 0) return {}
  return { line, endLine: endLine && endLine >= line ? endLine : undefined }
}

function parseFileHref(raw: string | undefined): ParsedFileHref {
  if (!raw) return { path: '' }
  let cleaned = raw.trim()
  if (cleaned.startsWith('<') && cleaned.endsWith('>')) {
    cleaned = cleaned.slice(1, -1).trim()
  }
  if (/^file:\/\//i.test(cleaned)) {
    try {
      const fileUrl = new URL(cleaned)
      let pathname = fileUrl.hostname && fileUrl.hostname !== 'localhost'
        ? `//${fileUrl.hostname}${fileUrl.pathname}`
        : fileUrl.pathname
      if (/^\/[a-zA-Z]:[/\\]/.test(pathname)) pathname = pathname.slice(1)
      return { path: pathname, ...extractLineRange(fileUrl.hash) }
    } catch {
      return { path: cleaned }
    }
  }
  // Handle relative / local paths with trailing `#L10-L20`
  const hashIdx = cleaned.indexOf('#')
  if (hashIdx > 0 && !/^(?:https?|mailto):/i.test(cleaned)) {
    const lineInfo = extractLineRange(cleaned.slice(hashIdx))
    if (lineInfo.line) {
      return { path: cleaned.slice(0, hashIdx), ...lineInfo }
    }
  }
  return { path: cleaned }
}

function cleanHref(raw: string | undefined): string {
  return parseFileHref(raw).path
}

const IMAGE_EXTENSIONS = new Set(['.png', '.jpg', '.jpeg', '.webp', '.svg', '.gif', '.bmp', '.ico'])
const DOC_EXTENSIONS = new Set(['.md', '.markdown', '.txt', '.log', '.pdf', '.docx'])
const CONFIG_EXTENSIONS = new Set(['.json', '.yaml', '.yml', '.toml', '.ini', '.env', '.conf', '.cfg', '.properties', '.xml'])

function fileExtension(path: string): string {
  const clean = path.toLowerCase().split(/[?#]/)[0]
  const base = clean.split(/[\\/]/).pop() || clean
  if (base === '.env' || base.startsWith('.env.')) return '.env'
  const dot = base.lastIndexOf('.')
  return dot === -1 ? '' : base.slice(dot)
}

function isImagePath(path: string): boolean {
  return IMAGE_EXTENSIONS.has(fileExtension(path))
}

function pickFilePillStyle(path: string, matchedViewer: ReturnType<typeof artifactViewerRegistry.findViewer>) {
  const ext = fileExtension(path)
  if (matchedViewer && matchedViewer.id !== 'monaco-viewer') {
    return { Icon: matchedViewer.icon, tone: 'accent' as const }
  }
  if (DOC_EXTENSIONS.has(ext)) {
    return { Icon: IconFileDescription, tone: 'doc' as const }
  }
  if (CONFIG_EXTENSIONS.has(ext)) {
    return { Icon: IconBraces, tone: 'config' as const }
  }
  if (matchedViewer?.id === 'monaco-viewer') {
    return { Icon: IconFileCode, tone: 'code' as const }
  }
  return { Icon: IconFile, tone: 'file' as const }
}

function isLocalPath(path: string): boolean {
  if (/^[a-zA-Z]:[/\\\\]/.test(path)) return true
  if (path.startsWith('file://')) return true
  if (/^\/(?:Users|home|tmp|var|private|opt|etc|mnt)\b/i.test(path)) return true
  // 委托注册表：只要是任何已注册插件声明支持的后缀，统一视为工件路径
  return artifactViewerRegistry.isSupportedExtension(path) || isNonPreviewableFile(path)
}

function isFileLink(raw: string | undefined, cleaned: string): boolean {
  if (!raw || !cleaned) return false
  if (/^file:\/\//i.test(raw) || /^[a-zA-Z]:(?:[/\\]|%2f|%5c)/i.test(cleaned)) return true
  if (/^(?:[a-z][a-z\d+.-]*:|\/\/|\/api\/|#)/i.test(cleaned)) return false
  return true
}

function safeUrl(value: string | undefined): string | undefined {
  if (!value) return undefined
  const cleaned = cleanHref(value)
  if (/^(?:https?:|mailto:)/i.test(cleaned)) return cleaned
  if (cleaned.startsWith('//')) return undefined
  if (cleaned.startsWith('/') || cleaned.startsWith('#')) return cleaned
  return undefined
}

const SHELL_COMMAND_PREFIX_RE = /^(?:bash|sh|zsh|fish|python3?|node|npm|npx|pnpm|yarn|uv|go|git|docker|kubectl|helm|kustomize|curl|wget|rm|cp|mv|cat|head|tail|grep|rg|find|ls|cd|chmod|chown|sudo|env|powershell|pwsh|cmd|make|cargo|pytest|vitest)\s+/i
const SHELL_SYNTAX_RE = /(?:&&|\|\||[|;`$<>]|\s--?[a-zA-Z0-9])/

function looksLikeBacktickedFilePath(
  raw: string,
  rootFiles: Set<string> | null,
  knownArtifact?: (path: string) => boolean,
): boolean {
  const trimmed = raw.trim()
  if (!trimmed) return false
  if (SHELL_COMMAND_PREFIX_RE.test(trimmed) || SHELL_SYNTAX_RE.test(trimmed)) return false
  if (!artifactViewerRegistry.isSupportedExtension(trimmed) && !isNonPreviewableFile(trimmed)) {
    return false
  }
  const hasSlash = /[\\/]/.test(trimmed)
  if (hasSlash) {
    // Allow spaces inside Windows drive paths (e.g. `P:\...\Astrorder Setup 0.1.0.exe`) or POSIX absolute paths,
    // or inside the filename after a slash, but reject `cmd arg/file.sh` where whitespace precedes the first slash.
    const firstSlash = trimmed.search(/[\\/]/)
    const firstSpace = trimmed.search(/\s/)
    if (firstSpace !== -1 && firstSpace < firstSlash) return false
    return true
  }
  return Boolean(rootFiles?.has(trimmed) || knownArtifact?.(trimmed))
}

// Protected spans that bare-filename auto-linking must NEVER rewrite inside.
const PROTECTED_SPAN_RE = /(\[[^\]\r\n]*\]\((?:<[^>\r\n]*>|[^)\s\r\n]+)\)|`[^`\r\n]+`|(?:https?|file):\/\/[^\s)\]>\r\n]+)/gi

/**
 * 自动感知纯文本中属于已注册插件的工件文件名，并转译为 Markdown 链接
 */
function artifactSuffixes(match: string): string[] {
  return [match, ...Array.from(match.matchAll(/ +/g), (space) => match.slice(space.index! + space[0].length))]
}

function autoLinkArtifacts(
  text: string,
  rootFiles: Set<string> | null,
  checkedPaths: Set<string> | null,
  knownArtifact?: (path: string) => boolean,
): string {
  const extRegex = artifactViewerRegistry.getExtensionPattern()
  const lines = text.split('\n')
  let inCodeBlock = false

  const processed = lines.map((line) => {
    if (/^\s*(```|~~~)/.test(line)) {
      inCodeBlock = !inCodeBlock
      return line
    }
    if (inCodeBlock) return line

    // Split line into protected spans (existing markdown links, inline code, URLs) and plain prose.
    const parts = line.split(PROTECTED_SPAN_RE)
    return parts.map((segment, index) => {
      if (!segment) return segment
      // Odd indices are captured protected spans.
      if (index % 2 === 1) {
        // Only upgrade standalone backticked file paths (never shell commands or existing links).
        if (segment.startsWith('`') && segment.endsWith('`')) {
          const inner = segment.slice(1, -1)
          if (looksLikeBacktickedFilePath(inner, rootFiles, knownArtifact)) {
            const trimmed = inner.trim()
            return `[${trimmed}](<${trimmed}>)`
          }
        }
        return segment
      }

      // Plain prose: auto-link verified workspace filenames / relative paths.
      return segment.replace(extRegex, (match, fileName) => {
        const target = artifactSuffixes(fileName).find((candidate) =>
          knownArtifact?.(candidate) || (/[\\/]/.test(candidate) ? checkedPaths?.has(candidate) : rootFiles?.has(candidate)),
        )
        if (!target) return match
        return `${fileName.slice(0, fileName.length - target.length)}[${target}](<${target}>)`
      })
    }).join('')
  })

  return processed.join('\n')
}

function extractPlainProseForPathCheck(value: string): string {
  const lines = value.split('\n')
  let inCodeBlock = false
  const prose: string[] = []
  for (const line of lines) {
    if (/^\s*(```|~~~)/.test(line)) {
      inCodeBlock = !inCodeBlock
      continue
    }
    if (inCodeBlock) continue
    const parts = line.split(PROTECTED_SPAN_RE)
    for (let i = 0; i < parts.length; i += 2) {
      if (parts[i]) prose.push(parts[i])
    }
  }
  return prose.join('\n')
}

function childrenPlainText(node: ReactNode): string {
  if (typeof node === 'string' || typeof node === 'number') return String(node)
  if (Array.isArray(node)) return node.map(childrenPlainText).join('')
  if (isValidElement<{ children?: ReactNode }>(node)) return childrenPlainText(node.props.children)
  return ''
}

const CALLOUT_ICONS: Record<CalloutKind, typeof IconInfoCircle> = {
  note: IconInfoCircle,
  tip: IconBulb,
  important: IconAlertCircle,
  warning: IconAlertTriangle,
  caution: IconFlame,
}

const rootFileCache = new Map<string, { expires: number; promise: Promise<Set<string>> }>()
const pathCheckCache = new Map<string, { expires: number; promise: Promise<Set<string>> }>()

export const MarkdownContent = memo(function MarkdownContent({
  value,
  onImageClick,
  onGalleryClick,
  session,
}: {
  value: string
  onImageClick?: (url: string) => void
  onGalleryClick?: (url: string, allUrls: string[]) => void
  session?: Session | null
}) {
  const fileSourceScope = useContext(FileSourceContext)
  const defaultSession = useAstrorderStore((state) => {
    if (session !== undefined) return undefined
    for (const key in state.sessions) return state.sessions[key]
    return undefined
  })
  const currentSession = session === undefined ? defaultSession : session
  const openArtifact = useSidecarStore((state) => state.openArtifact)
  const rootKey = !fileSourceScope && session?.workspace ? JSON.stringify([session.id, session.workspace, session.connection_id]) : ''
  const [rootFiles, setRootFiles] = useState<{ key: string; names: Set<string> } | null>(null)
  const [checkedPaths, setCheckedPaths] = useState<{ key: string; names: Set<string> } | null>(null)
  const [linkMenu, setLinkMenu] = useState<MarkdownLinkMenuTarget | null>(null)
  const [hoverTarget, setHoverTarget] = useState<(FileHoverTarget & { onOpen: () => void }) | null>(null)
  const hoverOpenTimer = useRef<number | null>(null)
  const hoverCloseTimer = useRef<number | null>(null)

  const clearHoverTimers = () => {
    if (hoverOpenTimer.current !== null) {
      window.clearTimeout(hoverOpenTimer.current)
      hoverOpenTimer.current = null
    }
    if (hoverCloseTimer.current !== null) {
      window.clearTimeout(hoverCloseTimer.current)
      hoverCloseTimer.current = null
    }
  }

  useEffect(() => () => clearHoverTimers(), [])

  const closeHoverNow = () => {
    clearHoverTimers()
    setHoverTarget(null)
  }

  const scheduleHoverOpen = (
    element: HTMLElement,
    info: Omit<FileHoverTarget, 'rect'> & { onOpen: () => void },
  ) => {
    if (typeof window !== 'undefined' && typeof window.matchMedia === 'function') {
      if (window.matchMedia('(hover: none)').matches) return
    }
    clearHoverTimers()
    const rect = element.getBoundingClientRect()
    const snapshot = {
      left: rect.left,
      right: rect.right,
      top: rect.top,
      bottom: rect.bottom,
      width: rect.width,
      height: rect.height,
    }
    hoverOpenTimer.current = window.setTimeout(() => {
      hoverOpenTimer.current = null
      setHoverTarget({ ...info, rect: snapshot })
    }, 320)
  }

  const scheduleHoverClose = () => {
    if (hoverOpenTimer.current !== null) {
      window.clearTimeout(hoverOpenTimer.current)
      hoverOpenTimer.current = null
    }
    if (hoverCloseTimer.current !== null) {
      window.clearTimeout(hoverCloseTimer.current)
    }
    hoverCloseTimer.current = window.setTimeout(() => {
      hoverCloseTimer.current = null
      setHoverTarget(null)
    }, 180)
  }

  const buildRawPreviewUrl = (fullPath: string, httpUrl: boolean): string => {
    if (httpUrl) return fullPath
    if (currentSession) {
      return resolveArtifactFromPath(fullPath.replace(/%/g, '%25'), currentSession).readUrl
    }
    return `/api/v1/files/raw?path=${encodeURIComponent(fullPath)}`
  }
  const pathKey = useMemo(() => {
    const extRegex = artifactViewerRegistry.getExtensionPattern()
    return JSON.stringify([...new Set(
      Array.from(extractPlainProseForPathCheck(value).matchAll(new RegExp(extRegex.source, 'gi')))
        .flatMap((match) => artifactSuffixes(match[1]).filter((candidate) => /[\\/]/.test(candidate))),
    )].slice(0, 100))
  }, [value])
  const checkKey = `${rootKey}:${pathKey}`

  useEffect(() => {
    if (!rootKey || !session?.workspace) return
    let active = true
    let cached = rootFileCache.get(rootKey)
    if (!cached || cached.expires < Date.now()) {
      const promise = api.getWorkspaceRootFiles(session.id, session.workspace, session.connection_id)
        .then(({ root, items }) => {
          const normalize = (path: string) => {
            const normalized = path.replace(/\\/g, '/').replace(/\/+$/, '')
            return /^[a-z]:\//i.test(normalized) ? normalized.toLowerCase() : normalized
          }
          if (normalize(root) !== normalize(session.workspace!)) return new Set<string>()
          return new Set(items.filter((item) => !item.is_dir).map((item) => item.name))
        })
        .catch(() => {
          rootFileCache.delete(rootKey)
          return new Set<string>()
        })
      cached = { expires: Date.now() + 60_000, promise }
      rootFileCache.set(rootKey, cached)
    }
    void cached.promise.then((names) => { if (active) setRootFiles({ key: rootKey, names }) })
    return () => { active = false }
  }, [rootKey, session?.id, session?.workspace, session?.connection_id])

  useEffect(() => {
    if (!rootKey || !session?.workspace || pathKey === '[]') return
    let active = true
    let cached = pathCheckCache.get(checkKey)
    if (!cached || cached.expires < Date.now()) {
      const promise = api.checkWorkspaceFiles(session.workspace, JSON.parse(pathKey) as string[], session.connection_id)
        .then(({ existing }) => new Set(existing))
        .catch(() => {
          pathCheckCache.delete(checkKey)
          return new Set<string>()
        })
      cached = { expires: Date.now() + 60_000, promise }
      pathCheckCache.set(checkKey, cached)
    }
    void cached.promise.then((names) => { if (active) setCheckedPaths({ key: checkKey, names }) })
    return () => { active = false }
  }, [checkKey, pathKey, rootKey, session?.workspace, session?.connection_id])

  const visualizations = useMemo(() => extractVisualizations(value), [value])
  const effectiveRootFiles = rootFiles?.key === rootKey && rootKey ? rootFiles.names : null
  const effectiveCheckedPaths = checkedPaths?.key === checkKey && rootKey ? checkedPaths.names : null
  const normalizedValue = useMemo(
    () => autoLinkArtifacts(
      visualizations.text,
      effectiveRootFiles,
      effectiveCheckedPaths,
      fileSourceScope ? (path) => Boolean(findGroupFile(fileSourceScope.files, path)) : undefined,
    ),
    [effectiveCheckedPaths, effectiveRootFiles, fileSourceScope, visualizations.text],
  )

  const resolvePathWithWorkspace = (raw: string): string => {
    const unquoted = raw.trim().replace(/^<|>$/g, '')
    if (/^https?:\/\//i.test(unquoted)) {
      try {
        const u = new URL(unquoted)
        return decodeURIComponent(u.pathname.replace(/^\/abs\/path\//, ''))
      } catch {
        return unquoted
      }
    }
    const decoded = decodeURIComponent(unquoted.replace(/%(?![\da-f]{2})/gi, '%25'))
    if (currentSession?.workspace && !/^[a-zA-Z]:[/\\]/.test(decoded) && !decoded.startsWith('/') && !decoded.startsWith('\\\\')) {
      const sep = currentSession.workspace.includes('\\') ? '\\' : '/'
      return `${currentSession.workspace}${sep}${decoded}`
    }
    return decoded
  }

  const openLinkMenu = (event: React.MouseEvent, kind: 'url' | 'file', value: string) => {
    event.preventDefault()
    event.stopPropagation()
    setLinkMenu({ x: event.clientX, y: event.clientY, kind, value })
  }

  const handleOpenLocalFile = async (path: string, line?: number, endLine?: number) => {
    const fullPath = resolvePathWithWorkspace(path)
    if (currentSession) {
      const artifact = resolveArtifactFromPath(fullPath.replace(/%/g, '%25'), currentSession)
      if (line) {
        artifact.metadata = { ...(artifact.metadata || {}), line, ...(endLine ? { endLine } : {}) }
      }
      const viewer = artifactViewerRegistry.findViewer(artifact)
      if (viewer) {
        openArtifact(artifact, viewer.id)
        return
      }
      useSidecarStore.getState().revealFileInTree(
        currentSession.id,
        currentSession.agent_id,
        fullPath,
        currentSession.workspace || undefined,
        currentSession.project_name || currentSession.title,
        currentSession.connection_id || undefined,
      )
      return
    }
    window.open(`/api/v1/files/raw?path=${encodeURIComponent(fullPath)}&download=1`, '_blank')
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
            const parsedHref = parseFileHref(href)
            const cleaned = parsedHref.path
            const httpUrl = /^https?:\/\//i.test(cleaned)
            if (fileSourceScope && !httpUrl && isFileLink(href, cleaned)) {
              return findGroupFile(fileSourceScope.files, cleaned)
                ? <GroupFileLink href={cleaned} onImageClick={onImageClick}>{children}</GroupFileLink>
                : <span title={fileSourceScope.checking ? '文件正在核验' : '文件来源尚未核验'}>{children}</span>
            }

            if ((httpUrl && isLocalPath(cleaned)) || isFileLink(href, cleaned)) {
              const fullPath = resolvePathWithWorkspace(cleaned)
              const menuTarget = httpUrl ? cleaned : fullPath
              const menuKind = httpUrl ? 'url' : 'file'
              if (isImagePath(cleaned)) {
                const query = new URLSearchParams({ path: fullPath })
                if (currentSession?.id) query.set('session_id', currentSession.id)
                if (currentSession?.connection_id) query.set('connection_id', currentSession.connection_id)
                const rawUrl = `/api/v1/files/raw?${query.toString()}`
                const openImage = () => {
                  closeHoverNow()
                  if (onGalleryClick) {
                    const allUrls: string[] = []
                    const seen = new Set<string>()
                    const linkRe = /(!?)\[[^\]]*\]\((<[^>]+>|[^)\s]+)(?:\s+"[^"]*")?\)/g
                    for (const m of value.matchAll(linkRe)) {
                      const rawTarget = (m[2] || '').trim()
                      if (!rawTarget) continue
                      const parsed = parseFileHref(rawTarget).path
                      const isHttp = /^https?:\/\//i.test(parsed)
                      if ((!isHttp && isFileLink(rawTarget, parsed) && isImagePath(parsed)) || (isHttp && isLocalPath(parsed) && isImagePath(parsed))) {
                        const fp = resolvePathWithWorkspace(parsed)
                        const q = new URLSearchParams({ path: fp })
                        if (currentSession?.id) q.set('session_id', currentSession.id)
                        if (currentSession?.connection_id) q.set('connection_id', currentSession.connection_id)
                        const u = `/api/v1/files/raw?${q.toString()}`
                        if (!seen.has(u)) {
                          seen.add(u)
                          allUrls.push(u)
                        }
                      }
                    }
                    if (allUrls.length > 1) {
                      onGalleryClick(rawUrl, allUrls.includes(rawUrl) ? allUrls : [rawUrl, ...allUrls])
                      return
                    }
                  }
                  if (onImageClick) onImageClick(rawUrl)
                  else window.open(rawUrl, '_blank')
                }
                const displayName = childrenPlainText(children).trim() || cleaned.split(/[\\/]/).pop() || cleaned
                return (
                  <button
                    type="button"
                    className="markdown-local-link markdown-image-link is-tone-image"
                    onClick={openImage}
                    onMouseEnter={(e) => scheduleHoverOpen(e.currentTarget, {
                      path: fullPath,
                      displayName,
                      readUrl: rawUrl,
                      isImage: true,
                      isNonPreviewable: false,
                      onOpen: openImage,
                    })}
                    onMouseLeave={scheduleHoverClose}
                    onContextMenu={(event) => {
                      closeHoverNow()
                      openLinkMenu(event, menuKind, menuTarget)
                    }}
                  >
                    <IconPhoto size={13} aria-hidden="true" />
                    <span className="markdown-local-link-label">{children}</span>
                  </button>
                )
              }

              const dummyArtifact = currentSession ? resolveArtifactFromPath(fullPath.replace(/%/g, '%25'), currentSession) : null
              const matchedViewer = dummyArtifact ? artifactViewerRegistry.findViewer(dummyArtifact) : null
              const { Icon, tone } = pickFilePillStyle(cleaned, matchedViewer)
              const lineLabel = parsedHref.line
                ? parsedHref.endLine && parsedHref.endLine !== parsedHref.line
                  ? `:${parsedHref.line}-${parsedHref.endLine}`
                  : `:${parsedHref.line}`
                : ''
              const labelAlreadyHasLine = lineLabel && /:\d+/.test(childrenPlainText(children))
              const linkClass = matchedViewer
                ? `markdown-local-link markdown-file-link markdown-${matchedViewer.id.replace('-viewer', '')}-link is-tone-${tone}`
                : `markdown-local-link markdown-file-link is-tone-${tone}`
              const displayName = childrenPlainText(children).trim() || cleaned.split(/[\\/]/).pop() || cleaned
              const previewUrl = buildRawPreviewUrl(httpUrl ? cleaned : fullPath, httpUrl)
              const openFile = () => {
                closeHoverNow()
                void handleOpenLocalFile(cleaned, parsedHref.line, parsedHref.endLine)
              }

              return (
                <button
                  type="button"
                  className={linkClass}
                  onClick={openFile}
                  onMouseEnter={(e) => scheduleHoverOpen(e.currentTarget, {
                    path: fullPath,
                    displayName,
                    readUrl: previewUrl,
                    isImage: false,
                    isVideo: Boolean(videoMediaType(cleaned)),
                    isNonPreviewable: isNonPreviewableFile(cleaned),
                    line: parsedHref.line,
                    endLine: parsedHref.endLine,
                    onOpen: openFile,
                  })}
                  onMouseLeave={scheduleHoverClose}
                  onContextMenu={(event) => {
                    closeHoverNow()
                    openLinkMenu(event, menuKind, menuTarget)
                  }}
                >
                  <Icon size={13} aria-hidden="true" />
                  <span className="markdown-local-link-label">{children}</span>
                  {lineLabel && !labelAlreadyHasLine && (
                    <span className="markdown-local-link-line">{lineLabel}</span>
                  )}
                </button>
              )
            }

            const safe = safeUrl(href)
            if (!safe) return <>{children}</>

            const isArtifactUrl = httpUrl && artifactViewerRegistry.isSupportedExtension(safe)
            if (isArtifactUrl && currentSession) {
              const dummy = resolveArtifactFromPath(safe, currentSession)
              const matched = artifactViewerRegistry.findViewer(dummy)
              const { Icon, tone } = pickFilePillStyle(safe, matched)

              return (
                <button
                  type="button"
                  className={`markdown-local-link markdown-file-link is-tone-${tone}`}
                  onClick={() => void handleOpenLocalFile(safe)}
                  onContextMenu={(event) => openLinkMenu(event, 'url', safe)}
                  title={`在工作台打开：${safe}`}
                >
                  <Icon size={13} aria-hidden="true" />
                  <span className="markdown-local-link-label">{children}</span>
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
                onContextMenu={httpUrl ? (event) => openLinkMenu(event, 'url', cleaned) : undefined}
              >
                {children}
                {external && <IconExternalLink className="markdown-external-icon" size={13} aria-hidden="true" />}
              </Anchor>
            )
          },
          blockquote: ({ children }) => {
            const callout = parseCalloutBlockquote(children)
            if (callout) {
              const Icon = CALLOUT_ICONS[callout.kind]
              return (
                <aside className={`markdown-callout is-${callout.kind}`} role="note">
                  <div className="markdown-callout-title">
                    <Icon size={15} aria-hidden="true" />
                    <span>{callout.label}</span>
                  </div>
                  <div className="markdown-callout-body">{callout.children}</div>
                </aside>
              )
            }
            return <blockquote>{children}</blockquote>
          },
          img: (props) => {
            const cleaned = cleanHref(props.src)
            const isFile = /^[a-zA-Z]:[/\\]/.test(cleaned) || (
              !/^(?:[a-z][a-z\d+.-]*:|\/\/|\/api\/|#)/i.test(cleaned) && isLocalPath(cleaned)
            )
            if (fileSourceScope && isFile) {
              return findGroupFile(fileSourceScope.files, cleaned)
                ? <GroupFileLink href={cleaned} image={props.alt || '图片'} onImageClick={onImageClick} />
                : <span title={fileSourceScope.checking ? '图片正在核验' : '图片来源尚未核验'}>{props.alt || '图片'}</span>
            }
            let src = safeUrl(cleaned)
            let filePath = ''
            if (isFile) {
              const fullPath = resolvePathWithWorkspace(cleaned)
              filePath = fullPath
              const query = new URLSearchParams({ path: fullPath })
              if (currentSession?.id) query.set('session_id', currentSession.id)
              if (currentSession?.connection_id) query.set('connection_id', currentSession.connection_id)
              src = `/api/v1/files/raw?${query.toString()}`
            }
            if (!src) return null
            const menuKind = isFile ? 'file' : /^https?:\/\//i.test(cleaned) ? 'url' : null
            const onContextMenu = menuKind ? (event: React.MouseEvent) => openLinkMenu(event, menuKind, isFile ? filePath : cleaned) : undefined
            return onImageClick ? (
              <button
                type="button"
                className="markdown-image-button"
                onClick={() => onImageClick(src)}
                onContextMenu={onContextMenu}
                aria-label={`放大图片：${props.alt || ''}`}
              >
                <img src={src} alt={props.alt || ''} loading="lazy" />
              </button>
            ) : (
              <img src={src} alt={props.alt || ''} loading="lazy" onContextMenu={onContextMenu} />
            )
          },
          table: (props) => <div className="markdown-table-wrap"><table>{props.children}</table></div>,
          pre: MarkdownPre,
        }}
      >
        {normalizedValue}
      </ReactMarkdown>
      {linkMenu && <MarkdownLinkMenu target={linkMenu} session={session} onClose={() => setLinkMenu(null)} />}
      {hoverTarget && (
        <FileLinkHoverPreview
          target={hoverTarget}
          onMouseEnter={clearHoverTimers}
          onMouseLeave={scheduleHoverClose}
          onOpen={() => hoverTarget.onOpen()}
          onClose={closeHoverNow}
        />
      )}
      {visualizations.paths.map(path => <CodexVisualization key={path} path={path} session={currentSession} />)}
    </div>
  )
})
