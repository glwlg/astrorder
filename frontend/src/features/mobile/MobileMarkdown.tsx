import { useRef, useState } from 'react'
import type { ReactNode } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

function CodeBlock({ children }: { children?: ReactNode }) {
  const element = useRef<HTMLPreElement>(null)
  const [wrap, setWrap] = useState(false)
  const [copied, setCopied] = useState(false)
  return <div className="m-code-block"><div className="m-code-actions">
    <button onClick={() => setWrap(!wrap)}>{wrap ? '取消换行' : '换行'}</button>
    <button onClick={async () => { try { await navigator.clipboard.writeText(element.current?.textContent || ''); setCopied(true) } catch { setCopied(false) } }}>{copied ? '已复制' : '复制'}</button>
  </div><pre ref={element} style={{ whiteSpace: wrap ? 'pre-wrap' : 'pre', wordBreak: wrap ? 'break-all' : 'normal' }}>{children}</pre></div>
}

const LOCAL_FILE_RE = /^(?:[a-zA-Z]:[/\\]|\/|\.{1,2}\/|(?!\w+:\/\/)[^\s]+\.[a-zA-Z0-9]{1,8}$)/

export function MobileMarkdown({ value, onFileClick, onImageClick }: {
  value: string
  onFileClick?: (path: string) => void
  onImageClick?: (url: string) => void
}) {
  return <div className="markdown-content"><ReactMarkdown remarkPlugins={[remarkGfm]} skipHtml components={{
    pre: ({ children }) => <CodeBlock>{children}</CodeBlock>,
    a: ({ href, children }) => {
      const url = href || ''
      const isLocal = LOCAL_FILE_RE.test(url) && !/^https?:\/\//i.test(url)
      if (isLocal && onFileClick) {
        return <a href={url} onClick={(e) => { e.preventDefault(); onFileClick(url) }}>{children}</a>
      }
      return <a href={url} target="_blank" rel="noopener noreferrer">{children}</a>
    },
    img: ({ src, alt }) => {
      if (onImageClick && src) {
        return <button type="button" style={{ border: 'none', background: 'none', padding: 0, display: 'block' }} onClick={() => onImageClick(src)}><img src={src} alt={alt || ''} loading="lazy" style={{ maxWidth: '100%', borderRadius: 8, marginTop: 8, marginBottom: 8, display: 'block' }} /></button>
      }
      return <img src={src} alt={alt || ''} loading="lazy" style={{ maxWidth: '100%', borderRadius: 8, marginTop: 8, marginBottom: 8, display: 'block' }} />
    },
    table: ({ children }) => <div style={{ overflowX: 'auto' }}><table>{children}</table></div>,
  }}>{value}</ReactMarkdown></div>
}
