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
export function MobileMarkdown({ value }: { value: string }) {
  return <div className="markdown-content"><ReactMarkdown remarkPlugins={[remarkGfm]} skipHtml components={{
    pre: ({ children }) => <CodeBlock>{children}</CodeBlock>,
    a: ({ href, children }) => <a href={href} target="_blank" rel="noopener noreferrer">{children}</a>,
    table: ({ children }) => <div style={{ overflowX: 'auto' }}><table>{children}</table></div>,
  }}>{value}</ReactMarkdown></div>
}
