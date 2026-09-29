import {
  IconCheck,
  IconCopy,
  IconExternalLink,
  IconEye,
  IconFileText,
} from '@tabler/icons-react'
import { Anchor } from '@mantine/core'
import { isValidElement, useEffect, useRef, useState, type ReactNode } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import type { Session } from '../domain/types'
import { api } from '../api/client'
import { artifactViewerRegistry, isNonPreviewableFile } from '../features/sidecar/registry'
import { resolveArtifactFromPath } from '../features/sidecar/resolver'
import { useSidecarStore } from '../features/sidecar/sidecarStore'
import { useAstrorderStore } from '../state/store'
import { renderMermaid } from './renderMermaid'

function MermaidDiagram({ code }: { code: string }) {
  const [svg, setSvg] = useState('')
  const [failed, setFailed] = useState(false)

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

  if (failed) return <MarkdownCodeBlock><code>{code}</code></MarkdownCodeBlock>
  return (
    <div className="markdown-mermaid" aria-label="Mermaid 图表">
      {svg ? <div dangerouslySetInnerHTML={{ __html: svg }} /> : <span>正在渲染图表…</span>}
    </div>
  )
}

function MarkdownCodeBlock({ children }: { children?: ReactNode }) {
  const [copied, setCopied] = useState(false)
  const codeRef = useRef<HTMLElement>(null)

  let language = ''
  if (isValidElement<{ className?: string }>(children)) {
    const match = (children.props.className || '').match(/language-([a-zA-Z0-9_-]+)/)
    if (match) language = match[1]
  }

  const handleCopy = (e: React.MouseEvent) => {
    e.stopPropagation()
    const text = codeRef.current?.innerText || codeRef.current?.textContent || ''
    if (!text) return
    void navigator.clipboard.writeText(text)
    setCopied(true)
    setTimeout(() => setCopied(false), 1600)
  }

  return (
    <div className="markdown-code-wrapper">
      <div className="markdown-code-header">
        <span className="markdown-code-lang">{(language || 'code').toUpperCase()}</span>
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
      <pre className="markdown-code-block">
        {isValidElement<{ children?: ReactNode }>(children) ? (
          <code {...children.props} ref={codeRef}>
            {children.props.children}
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
  return artifactViewerRegistry.isSupportedExtension(path) || isNonPreviewableFile(path)
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
function artifactSuffixes(match: string): string[] {
  return [match, ...Array.from(match.matchAll(/ +/g), (space) => match.slice(space.index! + space[0].length))]
}

function autoLinkArtifacts(text: string, rootFiles: Set<string> | null, checkedPaths: Set<string> | null): string {
  const extRegex = artifactViewerRegistry.getExtensionPattern()
  const lines = text.split('\n')
  let inCodeBlock = false

  const processed = lines.map((line) => {
    if (/^\s*(```|~~~)/.test(line)) {
      inCodeBlock = !inCodeBlock
      return line
    }
    if (inCodeBlock) return line

    // 1. 先将行内反引号包裹的有效工件路径（如 `P:\...\test.png` 或 `docs/design.md`）转换为 Markdown 链接
    let step = line.replace(/`([^`\r\n]+)`/g, (match, raw) => {
      const trimmed = String(raw).trim()
      if ((artifactViewerRegistry.isSupportedExtension(trimmed) || isNonPreviewableFile(trimmed)) &&
          (/[\\/]/.test(trimmed) || rootFiles?.has(trimmed))) {
        return `[${trimmed}](<${trimmed}>)`
      }
      return match
    })

    // 2. 将非代码块中的裸工件名/路径转为 Markdown 链接
    return step.replace(extRegex, (match, fileName, offset, fullStr) => {
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
      const target = artifactSuffixes(fileName).find((candidate) =>
        /[\\/]/.test(candidate) ? checkedPaths?.has(candidate) : rootFiles?.has(candidate))
      if (!target) return match

      return `${fileName.slice(0, fileName.length - target.length)}[${target}](<${target}>)`
    })
  })

  return processed.join('\n')
}

const rootFileCache = new Map<string, { expires: number; promise: Promise<Set<string>> }>()
const pathCheckCache = new Map<string, { expires: number; promise: Promise<Set<string>> }>()

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
  const rootKey = session?.workspace ? JSON.stringify([session.id, session.workspace, session.connection_id]) : ''
  const [rootFiles, setRootFiles] = useState<{ key: string; names: Set<string> } | null>(null)
  const [checkedPaths, setCheckedPaths] = useState<{ key: string; names: Set<string> } | null>(null)
  const extRegex = artifactViewerRegistry.getExtensionPattern()
  const pathKey = JSON.stringify([...new Set(
    Array.from(value.matchAll(new RegExp(extRegex.source, 'gi')))
      .flatMap((match) => artifactSuffixes(match[1]).filter((candidate) => /[\\/]/.test(candidate))),
  )].slice(0, 100))
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

  const normalizedValue = autoLinkArtifacts(
    value,
    rootFiles?.key === rootKey && rootKey ? rootFiles.names : null,
    checkedPaths?.key === checkKey && rootKey ? checkedPaths.names : null,
  )

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
            const cleaned = cleanHref(href)

            if (isLocalPath(cleaned)) {
              const fullPath = resolvePathWithWorkspace(cleaned)
              if (isImagePath(cleaned)) {
                const query = new URLSearchParams({ path: fullPath })
                if (currentSession?.id) query.set('session_id', currentSession.id)
                if (currentSession?.connection_id) query.set('connection_id', currentSession.connection_id)
                const rawUrl = `/api/v1/files/raw?${query.toString()}`
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
              const dummyArtifact = currentSession ? resolveArtifactFromPath(fullPath, currentSession) : null
              const matchedViewer = dummyArtifact ? artifactViewerRegistry.findViewer(dummyArtifact) : null

              const Icon = matchedViewer ? matchedViewer.icon : IconFileText
              const badgeText = matchedViewer ? matchedViewer.badgeLabel || '[工件]' : '[文件]'
              const actionTitle = matchedViewer ? `在右侧打开${matchedViewer.title}：${cleaned}` : `在文件树中定位：${cleaned}`
              const linkClass = matchedViewer
                ? `markdown-local-link markdown-file-link markdown-${matchedViewer.id.replace('-viewer', '')}-link`
                : 'markdown-local-link markdown-file-link'

              return (
                <button
                  type="button"
                  className={linkClass}
                  onClick={() => void handleOpenLocalFile(fullPath)}
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
          pre: (props) => {
            const code = mermaidSource(props.children)
            return code ? <MermaidDiagram code={code} /> : <MarkdownCodeBlock>{props.children}</MarkdownCodeBlock>
          },
        }}
      >
        {normalizedValue}
      </ReactMarkdown>
    </div>
  )
}
