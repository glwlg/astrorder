import { useEffect, useState } from 'react'
import { IconX, IconLoader2 } from '@tabler/icons-react'
import { MobileMarkdown } from './MobileMarkdown'

const TEXT_EXTS = new Set([
  'md', 'markdown', 'txt', 'log', 'py', 'ts', 'tsx', 'js', 'jsx', 'json',
  'rs', 'go', 'c', 'cpp', 'h', 'css', 'scss', 'sql', 'yaml', 'yml', 'toml',
  'ini', 'env', 'sh', 'bash', 'zsh', 'html', 'xml', 'svg', 'vue', 'java',
  'rb', 'php', 'swift', 'kt', 'diff', 'patch', 'conf', 'cfg', 'properties',
])
const IMAGE_EXTS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'ico'])
const MARKDOWN_EXTS = new Set(['md', 'markdown'])

function extOf(name: string): string {
  const dot = name.lastIndexOf('.')
  return dot >= 0 ? name.slice(dot + 1).toLowerCase() : ''
}

export function MobileArtifactSheet({ path, connectionId, onClose }: {
  path: string
  workspace?: string | null
  connectionId?: string | null
  onClose: () => void
}) {
  const [content, setContent] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  const name = path.split(/[/\\]/).pop() || path
  const ext = extOf(name)
  const isImage = IMAGE_EXTS.has(ext)
  const isText = TEXT_EXTS.has(ext)
  const isMarkdown = MARKDOWN_EXTS.has(ext)

  const params = new URLSearchParams({ path })
  if (connectionId) params.set('connection_id', connectionId)
  const rawUrl = `/api/v1/files/raw?${params.toString()}`

  useEffect(() => {
    if (isImage || !isText) {
      setLoading(false)
      return
    }
    let cancelled = false
    setLoading(true)
    fetch(rawUrl)
      .then(async (res) => {
        if (cancelled) return
        if (!res.ok) {
          const body = await res.text().catch(() => '')
          setError(`加载失败 (${res.status})${body ? `：${body.slice(0, 120)}` : ''}`)
        } else {
          const text = await res.text()
          setContent(text)
        }
      })
      .catch((err) => { if (!cancelled) setError(String(err)) })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [rawUrl, isImage, isText])

  return (
    <div className="m-artifact-overlay" onClick={onClose}>
      <div className="m-artifact-sheet" onClick={(e) => e.stopPropagation()}>
        <header className="m-artifact-head">
          <span className="m-artifact-name" title={path}>{name}</span>
          <button aria-label="关闭" onClick={onClose}><IconX size={20} /></button>
        </header>
        <div className="m-artifact-body">
          {loading && <div className="m-artifact-loading"><IconLoader2 size={24} className="spin" /> 加载中…</div>}
          {error && <div className="m-artifact-error">{error}</div>}
          {!loading && !error && isImage && (
            <img className="m-artifact-image" src={rawUrl} alt={name} />
          )}
          {!loading && !error && isText && content !== null && (
            isMarkdown
              ? <div className="m-artifact-markdown"><MobileMarkdown value={content} /></div>
              : <pre className="m-artifact-code">{content}</pre>
          )}
          {!loading && !error && !isImage && !isText && (
            <div className="m-artifact-fallback">
              <p>暂不支持在手机上预览 <code>.{ext}</code> 文件</p>
              <a href={rawUrl} target="_blank" rel="noreferrer">下载查看</a>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
