import { useState, useMemo, type MouseEvent } from 'react'
import { IconCheck, IconCopy, IconFileText, IconSearch } from '@tabler/icons-react'

interface SearchResultJson {
  total_count?: number
  matches_format?: string
  matches_text?: string
  files?: string[]
  matches?: Array<{ path: string; line?: number; content?: string }>
  truncated?: boolean
  hint?: string
  _hint?: string
}

interface ReadFileJson {
  content: string
  total_lines?: number
  file_size?: number
  truncated?: boolean
  hint?: string
}

export function ToolOutputBlock({
  text,
  payload,
  toolName = '',
}: {
  text?: string | null
  payload?: Record<string, unknown> | null
  toolName?: string
}) {
  const [copied, setCopied] = useState(false)
  const [showRaw, setShowRaw] = useState(false)

  const parsed = useMemo(() => {
    if (!text) return null
    const trimmed = text.trim()
    if (!trimmed.startsWith('{') || !trimmed.endsWith('}')) return null
    try {
      return JSON.parse(trimmed) as Record<string, unknown>
    } catch {
      return null
    }
  }, [text])

  const handleCopy = (e: MouseEvent<HTMLButtonElement>, content: string) => {
    e.stopPropagation()
    void navigator.clipboard.writeText(content)
    setCopied(true)
    setTimeout(() => setCopied(false), 1600)
  }

  // 1. 结构化搜索结果渲染 (Hermes / Grok search_files)
  if (parsed && !showRaw && (parsed.matches_text || Array.isArray(parsed.files) || Array.isArray(parsed.matches) || toolName === 'search_files')) {
    const searchData = parsed as SearchResultJson
    const count = searchData.total_count
    const matchText = searchData.matches_text || (Array.isArray(searchData.files) ? searchData.files.join('\n') : '')
    const hint = searchData.hint || searchData._hint

    return (
      <div className="tool-rich-view tool-search-view" aria-label="搜索结果">
        <div className="tool-rich-header">
          <span className="tool-rich-title">
            <IconSearch size={13} style={{ marginRight: 4 }} />
            搜索匹配结果
            {typeof count === 'number' && <span className="tool-rich-badge">共 {count} 项</span>}
            {searchData.truncated && <span className="tool-rich-badge is-warning">已截断</span>}
          </span>
          <div style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
            <button
              type="button"
              className="shell-terminal-copy"
              onClick={() => setShowRaw(true)}
              style={{ width: 'auto', padding: '2px 6px', fontSize: 10 }}
            >
              查看原始 JSON
            </button>
            <button
              type="button"
              className="shell-terminal-copy"
              onClick={(e) => handleCopy(e, matchText || text || '')}
              title="复制搜索结果"
            >
              {copied ? <IconCheck size={12} color="var(--astr-teal, #12b886)" /> : <IconCopy size={12} />}
            </button>
          </div>
        </div>
        {matchText && (
          <pre className="tool-rich-content">{matchText}</pre>
        )}
        {hint && (
          <div className="tool-rich-hint">{hint}</div>
        )}
      </div>
    )
  }

  // 2. 结构化文件读取结果渲染 (Hermes / Grok read_file)
  if (parsed && !showRaw && typeof parsed.content === 'string') {
    const readData = parsed as unknown as ReadFileJson
    const hint = readData.hint

    return (
      <div className="tool-rich-view tool-file-view" aria-label="文件内容">
        <div className="tool-rich-header">
          <span className="tool-rich-title">
            <IconFileText size={13} style={{ marginRight: 4 }} />
            文件读取内容
            {typeof readData.total_lines === 'number' && <span className="tool-rich-badge">共 {readData.total_lines} 行</span>}
            {readData.truncated && <span className="tool-rich-badge is-warning">部分截断</span>}
          </span>
          <div style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
            <button
              type="button"
              className="shell-terminal-copy"
              onClick={() => setShowRaw(true)}
              style={{ width: 'auto', padding: '2px 6px', fontSize: 10 }}
            >
              查看原始 JSON
            </button>
            <button
              type="button"
              className="shell-terminal-copy"
              onClick={(e) => handleCopy(e, readData.content)}
              title="复制文件内容"
            >
              {copied ? <IconCheck size={12} color="var(--astr-teal, #12b886)" /> : <IconCopy size={12} />}
            </button>
          </div>
        </div>
        <pre className="tool-rich-content">{readData.content}</pre>
        {hint && (
          <div className="tool-rich-hint">{hint}</div>
        )}
      </div>
    )
  }

  // 3. 通用兜底渲染
  return (
    <>
      {text && (
        <div style={{ position: 'relative' }}>
          {showRaw && (
            <button
              type="button"
              className="shell-terminal-copy"
              onClick={() => setShowRaw(false)}
              style={{ position: 'absolute', right: 8, top: 8, zIndex: 2, width: 'auto', padding: '2px 6px', fontSize: 10 }}
            >
              切换至解析视图
            </button>
          )}
          <pre className="tool-output">{text}</pre>
        </div>
      )}
      {payload != null && Object.keys(payload).length > 0 && (
        <pre className="tool-payload">{JSON.stringify(payload, null, 2)}</pre>
      )}
    </>
  )
}
