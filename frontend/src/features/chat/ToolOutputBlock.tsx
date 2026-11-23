import { useState, useMemo, type MouseEvent } from 'react'
import {
  IconCheck,
  IconCode,
  IconCopy,
  IconExternalLink,
  IconFileText,
  IconFolder,
  IconPhoto,
  IconSearch,
  IconZoomIn,
} from '@tabler/icons-react'
import type { Session } from '../../domain/types'
import { highlightCode } from '../../components/markdownHighlight'
import { artifactViewerRegistry } from '../sidecar/registry'
import { resolveArtifactFromPath } from '../sidecar/resolver'
import { useSidecarStore } from '../sidecar/sidecarStore'
import { isImageFilePath } from './toolPresentation'

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

const READ_TOOLS = new Set([
  'read_file',
  'fileRead',
  'view_file',
  'view_file_outline',
  'view_code_item',
  'read_resource',
  'read',
])

const IMAGE_TOOLS = new Set(['imageView', 'image_view'])

const SEARCH_TOOLS = new Set([
  'search_files',
  'fileSearch',
  'grep',
  'grep_search',
  'find_by_name',
  'search_web',
  'web_search',
  'SearchContext',
])

const ALIAS_CANONICAL: Record<string, string> = {
  path: 'path',
  absolutepath: 'path',
  targetfile: 'path',
  file: 'path',
  directorypath: 'directory',
  searchpath: 'directory',
  searchdirectory: 'directory',
  directory: 'directory',
  startline: 'start_line',
  start_line: 'start_line',
  endline: 'end_line',
  end_line: 'end_line',
  byteoffset: 'byte_offset',
  byte_offset: 'byte_offset',
  commandline: 'command',
  command: 'command',
  cmd: 'command',
  cwd: 'cwd',
  toolsummary: 'summary',
  summary: 'summary',
  query: 'query',
  pattern: 'query',
  target: 'old_string',
  targetcontent: 'old_string',
  old_string: 'old_string',
  replacement: 'new_string',
  replacementcontent: 'new_string',
  new_string: 'new_string',
  waitmsbeforeasync: '_ignore_wait',
  bypasssandbox: 'bypass_sandbox',
  isdaemon: 'is_daemon',
  overwrite: 'overwrite',
}

const FRIENDLY_LABELS: Record<string, string> = {
  path: '路径',
  directory: '目录',
  start_line: '起始行',
  end_line: '结束行',
  byte_offset: '字节偏移',
  query: '查询',
  command: '命令',
  cwd: '工作目录',
  summary: '摘要',
  action: '操作',
  Action: '操作',
  name: '名称',
  Name: '名称',
  role: '角色',
  Role: '角色',
  TypeName: '类型',
  Model: '模型',
  model: '模型',
  Workspace: '工作区',
  workspace: '工作区',
  brief: '任务简报',
  title: '标题',
  Prompt: '提示词',
  prompt: '提示词',
  Recipient: '接收方',
  Message: '消息',
  text: '内容',
  since: '起始序号',
  mentions_only: '仅响应提及',
  TaskId: '任务 ID',
  DurationSeconds: '定时(秒)',
  CronExpression: 'Cron',
  Description: '描述',
  ScriptPath: '脚本',
  Url: '链接',
  url: '链接',
  Uri: '资源',
  uri: '资源',
  domain: '域名',
  bypass_sandbox: '跳过沙箱',
  is_daemon: '常驻进程',
  overwrite: '覆盖写入',
}

const EXT_TO_LANG: Record<string, string> = {
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
  sh: 'sh',
  bash: 'bash',
  zsh: 'zsh',
  ps1: 'ps1',
  sql: 'sql',
  css: 'css',
  scss: 'scss',
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

function langForPath(filePath: string): string {
  const base = filePath.toLowerCase().split(/[\\/]/).pop() || ''
  const dot = base.lastIndexOf('.')
  if (dot === -1) return ''
  return EXT_TO_LANG[base.slice(dot + 1)] || ''
}

function firstStr(obj: Record<string, unknown> | null | undefined, ...keys: string[]): string {
  if (!obj) return ''
  for (const key of keys) {
    const val = obj[key]
    if (typeof val === 'string' && val.trim() !== '') return val.trim()
  }
  return ''
}

function firstNum(obj: Record<string, unknown> | null | undefined, ...keys: string[]): number | null {
  if (!obj) return null
  for (const key of keys) {
    const val = obj[key]
    if (typeof val === 'number' && Number.isFinite(val)) return val
    if (typeof val === 'string' && val.trim() !== '') {
      const n = Number(val)
      if (Number.isFinite(n)) return n
    }
  }
  return null
}

function shortenDisplayPath(fullPath: string, workspace?: string | null): { name: string; dir: string } {
  const normalized = fullPath.replace(/\\/g, '/')
  const parts = normalized.split('/')
  const name = parts.pop() || fullPath
  let dir = parts.join('/')
  if (workspace) {
    const normWs = workspace.replace(/\\/g, '/').replace(/\/+$/, '')
    if (dir === normWs) dir = ''
    else if (dir.startsWith(`${normWs}/`)) dir = dir.slice(normWs.length + 1)
  }
  if (dir.length > 52) {
    const segs = dir.split('/').filter(Boolean)
    if (segs.length > 3) {
      dir = `…/${segs.slice(-3).join('/')}`
    }
  }
  return { name, dir }
}

/** Deduplicate case/alias duplicates emitted by Antigravity normalization (e.g. Path + path, StartLine + start_line). */
export function dedupeToolPayload(payload: Record<string, unknown> | null | undefined): Array<{ key: string; label: string; value: unknown }> {
  if (!payload || typeof payload !== 'object') return []
  const seen = new Map<string, { key: string; label: string; value: unknown }>()
  for (const [rawKey, value] of Object.entries(payload)) {
    if (value === undefined || value === null || value === '') continue
    const lower = rawKey.toLowerCase()
    const canonical = ALIAS_CANONICAL[lower] ?? rawKey
    if (canonical === '_ignore_wait') continue
    if (seen.has(canonical)) continue
    const label = FRIENDLY_LABELS[canonical] || FRIENDLY_LABELS[rawKey] || rawKey
    seen.set(canonical, { key: canonical, label, value })
  }
  return Array.from(seen.values())
}

function formatParamValue(val: unknown): string {
  if (typeof val === 'string') return val
  if (typeof val === 'number' || typeof val === 'boolean') return String(val)
  try {
    return JSON.stringify(val, null, 2)
  } catch {
    return String(val)
  }
}

function openToolFileInSidecar(filePath: string, session: Session, startLine?: number | null, endLine?: number | null) {
  const artifact = resolveArtifactFromPath(filePath.replace(/%/g, '%25'), session)
  if (startLine != null && startLine > 0) {
    artifact.metadata = {
      ...(artifact.metadata || {}),
      line: startLine,
      ...(endLine != null && endLine >= startLine ? { endLine } : {}),
    }
  }
  const viewer = artifactViewerRegistry.findViewer(artifact)
  if (viewer) {
    useSidecarStore.getState().openArtifact(artifact, viewer.id)
    return
  }
  useSidecarStore.getState().revealFileInTree(
    session.id,
    session.agent_id,
    artifact.path || filePath,
    session.workspace || undefined,
    session.project_name || session.title,
    session.connection_id || undefined,
  )
}

export function ToolOutputBlock({
  text,
  payload,
  toolName = '',
  session,
  onImageClick,
}: {
  text?: string | null
  payload?: Record<string, unknown> | null
  toolName?: string
  session?: Session | null
  onImageClick?: (url: string) => void
}) {
  const [copied, setCopied] = useState(false)
  const [showRaw, setShowRaw] = useState(false)
  const [showParams, setShowParams] = useState(false)
  const [imgFailed, setImgFailed] = useState(false)

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

  const dedupedEntries = useMemo(() => dedupeToolPayload(payload), [payload])

  const filePath = useMemo(
    () => firstStr(payload, 'path', 'Path', 'AbsolutePath', 'File', 'TargetFile', 'DirectoryPath'),
    [payload],
  )
  const startLine = useMemo(() => firstNum(payload, 'start_line', 'StartLine', 'offset'), [payload])
  const endLine = useMemo(() => firstNum(payload, 'end_line', 'EndLine'), [payload])

  const handleCopy = (e: MouseEvent<HTMLButtonElement>, content: string) => {
    e.stopPropagation()
    void navigator.clipboard.writeText(content)
    setCopied(true)
    setTimeout(() => setCopied(false), 1600)
  }

  // Raw debug view (explicit opt-in via toggle button)
  if (showRaw) {
    return (
      <div className="tool-raw-wrap">
        <div className="tool-rich-header">
          <span className="tool-rich-title">
            <IconCode size={13} className="tool-rich-title-icon" />
            原始数据
          </span>
          <button
            type="button"
            className="tool-rich-btn"
            onClick={() => setShowRaw(false)}
          >
            返回视图
          </button>
        </div>
        {text && <pre className="tool-output">{text}</pre>}
        {payload != null && Object.keys(payload).length > 0 && (
          <pre className="tool-payload">{JSON.stringify(payload, null, 2)}</pre>
        )}
      </div>
    )
  }

  // 1. 图片读取预览 (Antigravity read_file / view_file / imageView on image files)
  if (filePath && (IMAGE_TOOLS.has(toolName) || (READ_TOOLS.has(toolName) && isImageFilePath(filePath)))) {
    const { name, dir } = shortenDisplayPath(filePath, session?.workspace)
    const rawUrl = session ? resolveArtifactFromPath(filePath.replace(/%/g, '%25'), session).readUrl : ''
    const openImage = (e: MouseEvent) => {
      e.stopPropagation()
      if (!rawUrl) return
      if (onImageClick) onImageClick(rawUrl)
      else window.open(rawUrl, '_blank')
    }

    return (
      <div className="tool-rich-view tool-image-view" aria-label={`图片预览 ${name}`}>
        <div className="tool-rich-header">
          <span className="tool-rich-title" title={filePath}>
            <IconPhoto size={13} className="tool-rich-title-icon" />
            <span className="tool-rich-filename">{name}</span>
            {dir && <span className="tool-rich-subpath">{dir}</span>}
          </span>
          <div className="tool-rich-actions">
            {rawUrl && !imgFailed && (
              <button type="button" className="tool-rich-btn" onClick={openImage} title="查看大图">
                <IconZoomIn size={12} />
                <span>大图</span>
              </button>
            )}
            <button
              type="button"
              className="tool-rich-btn"
              onClick={(e) => handleCopy(e, filePath)}
              title="复制文件路径"
              aria-label="复制路径"
            >
              {copied ? <IconCheck size={12} color="var(--astr-teal, #12b886)" /> : <IconCopy size={12} />}
            </button>
            <button type="button" className="tool-rich-btn is-muted" onClick={() => setShowRaw(true)}>
              JSON
            </button>
          </div>
        </div>
        {rawUrl && !imgFailed ? (
          <div className="tool-image-stage">
            <button
              type="button"
              className="tool-image-preview-btn"
              onClick={openImage}
              aria-label={`放大查看 ${name}`}
            >
              <img
                className="tool-image-preview"
                src={rawUrl}
                alt={name}
                loading="lazy"
                onError={() => setImgFailed(true)}
              />
            </button>
          </div>
        ) : (
          <div className="tool-path-strip" title={filePath}>
            <span className="tool-kv-key">路径</span>
            <span className="tool-kv-val">{filePath}</span>
          </div>
        )}
        {text && text.trim() && (
          <div className="tool-rich-hint">{text.trim()}</div>
        )}
      </div>
    )
  }

  // 2. 结构化搜索结果渲染 (Hermes / Grok search_files JSON)
  if (parsed && (parsed.matches_text || Array.isArray(parsed.files) || Array.isArray(parsed.matches) || toolName === 'search_files')) {
    const searchData = parsed as SearchResultJson
    const count = searchData.total_count
    const matchText = searchData.matches_text || (Array.isArray(searchData.files) ? searchData.files.join('\n') : '')
    const hint = searchData.hint || searchData._hint

    return (
      <div className="tool-rich-view tool-search-view" aria-label="搜索结果">
        <div className="tool-rich-header">
          <span className="tool-rich-title">
            <IconSearch size={13} className="tool-rich-title-icon" />
            搜索匹配结果
            {typeof count === 'number' && <span className="tool-rich-badge">共 {count} 项</span>}
            {searchData.truncated && <span className="tool-rich-badge is-warning">已截断</span>}
          </span>
          <div className="tool-rich-actions">
            <button
              type="button"
              className="tool-rich-btn is-muted"
              onClick={() => setShowRaw(true)}
            >
              查看原始 JSON
            </button>
            <button
              type="button"
              className="tool-rich-btn"
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

  // 3. 结构化文件读取结果渲染 (Hermes / Grok / Antigravity read_file JSON)
  if (parsed && typeof parsed.content === 'string') {
    const readData = parsed as unknown as ReadFileJson
    const hint = readData.hint
    const displayInfo = filePath ? shortenDisplayPath(filePath, session?.workspace) : null
    const lang = filePath ? langForPath(filePath) : ''
    const lineRange = startLine != null && endLine != null
      ? `L${startLine}-${endLine}`
      : startLine != null
        ? `L${startLine}+`
        : ''

    return (
      <div className="tool-rich-view tool-file-view" aria-label="文件内容">
        <div className="tool-rich-header">
          <span className="tool-rich-title" title={filePath || undefined}>
            <IconFileText size={13} className="tool-rich-title-icon" />
            {displayInfo ? <span className="tool-rich-filename">{displayInfo.name}</span> : '文件读取内容'}
            {lineRange && <span className="tool-rich-badge">{lineRange}</span>}
            {typeof readData.total_lines === 'number' && <span className="tool-rich-badge">共 {readData.total_lines} 行</span>}
            {readData.truncated && <span className="tool-rich-badge is-warning">部分截断</span>}
          </span>
          <div className="tool-rich-actions">
            {session && filePath && (
              <button
                type="button"
                className="tool-rich-btn"
                onClick={(e) => {
                  e.stopPropagation()
                  openToolFileInSidecar(filePath, session, startLine, endLine)
                }}
                title="在右侧栏打开文件"
              >
                <IconExternalLink size={12} />
                <span>打开</span>
              </button>
            )}
            <button
              type="button"
              className="tool-rich-btn is-muted"
              onClick={() => setShowRaw(true)}
            >
              查看原始 JSON
            </button>
            <button
              type="button"
              className="tool-rich-btn"
              onClick={(e) => handleCopy(e, readData.content)}
              title="复制文件内容"
            >
              {copied ? <IconCheck size={12} color="var(--astr-teal, #12b886)" /> : <IconCopy size={12} />}
            </button>
          </div>
        </div>
        <pre className="tool-rich-content">
          <code>{lang ? highlightCode(readData.content, lang) : readData.content}</code>
        </pre>
        {hint && (
          <div className="tool-rich-hint">{hint}</div>
        )}
      </div>
    )
  }

  // 4. Antigravity 文本文件读取（纯文本输出或仅元数据读取，不倾倒重复 Path/path JSON）
  if (READ_TOOLS.has(toolName) && filePath) {
    const { name, dir } = shortenDisplayPath(filePath, session?.workspace)
    const lang = langForPath(filePath)
    const lineRange = startLine != null && endLine != null
      ? `L${startLine}-${endLine}`
      : startLine != null
        ? `L${startLine}+`
        : ''
    const cleanText = (text || '').trim()

    return (
      <div className="tool-rich-view tool-file-view" aria-label={`文件 ${name}`}>
        <div className="tool-rich-header">
          <span className="tool-rich-title" title={filePath}>
            <IconFileText size={13} className="tool-rich-title-icon" />
            <span className="tool-rich-filename">{name}</span>
            {lineRange && <span className="tool-rich-badge">{lineRange}</span>}
            {dir && <span className="tool-rich-subpath">{dir}</span>}
          </span>
          <div className="tool-rich-actions">
            {session && (
              <button
                type="button"
                className="tool-rich-btn"
                onClick={(e) => {
                  e.stopPropagation()
                  openToolFileInSidecar(filePath, session, startLine, endLine)
                }}
                title="在右侧栏打开文件"
              >
                <IconExternalLink size={12} />
                <span>打开</span>
              </button>
            )}
            <button
              type="button"
              className="tool-rich-btn"
              onClick={(e) => handleCopy(e, cleanText || filePath)}
              title={cleanText ? '复制文件内容' : '复制文件路径'}
            >
              {copied ? <IconCheck size={12} color="var(--astr-teal, #12b886)" /> : <IconCopy size={12} />}
            </button>
            {payload && Object.keys(payload).length > 0 && (
              <button type="button" className="tool-rich-btn is-muted" onClick={() => setShowRaw(true)}>
                JSON
              </button>
            )}
          </div>
        </div>
        {cleanText ? (
          <pre className="tool-rich-content">
            <code>{lang ? highlightCode(cleanText, lang) : cleanText}</code>
          </pre>
        ) : (
          <div className="tool-path-strip" title={filePath}>
            <span className="tool-kv-key">路径</span>
            <span className="tool-kv-val">{filePath}</span>
          </div>
        )}
      </div>
    )
  }

  // 5. Antigravity 搜索 / 目录浏览纯文本输出
  if (toolName === 'list_dir' || SEARCH_TOOLS.has(toolName)) {
    const query = firstStr(payload, 'query', 'pattern', 'Query', 'Pattern')
    const dir = firstStr(payload, 'DirectoryPath', 'SearchPath', 'SearchDirectory', 'Directory', 'path', 'Path')
    const isDir = toolName === 'list_dir'
    const cleanText = (text || '').trim()

    return (
      <div className="tool-rich-view tool-search-view" aria-label={isDir ? '目录内容' : '搜索结果'}>
        <div className="tool-rich-header">
          <span className="tool-rich-title" title={dir || query || undefined}>
            {isDir ? <IconFolder size={13} className="tool-rich-title-icon" /> : <IconSearch size={13} className="tool-rich-title-icon" />}
            <span>{isDir ? '目录内容' : '搜索结果'}</span>
            {query && <span className="tool-rich-badge">{query}</span>}
            {dir && <span className="tool-rich-subpath">{shortenDisplayPath(dir, session?.workspace).dir || dir}</span>}
          </span>
          <div className="tool-rich-actions">
            {cleanText && (
              <button
                type="button"
                className="tool-rich-btn"
                onClick={(e) => handleCopy(e, cleanText)}
                title="复制结果"
              >
                {copied ? <IconCheck size={12} color="var(--astr-teal, #12b886)" /> : <IconCopy size={12} />}
              </button>
            )}
            {dedupedEntries.length > 0 && (
              <button type="button" className="tool-rich-btn is-muted" onClick={() => setShowRaw(true)}>
                JSON
              </button>
            )}
          </div>
        </div>
        {cleanText ? (
          <pre className="tool-rich-content">{cleanText}</pre>
        ) : (
          <div className="tool-kv-list">
            {dedupedEntries.map((entry) => (
              <div key={entry.key} className="tool-kv-row">
                <span className="tool-kv-key">{entry.label}</span>
                <span className="tool-kv-val">{formatParamValue(entry.value)}</span>
              </div>
            ))}
          </div>
        )}
      </div>
    )
  }

  // 6. 通用兜底渲染：有输出时干净展示输出，参数折叠为按需展开的结构化键值表（不再倾倒冗余大 JSON）
  const cleanText = (text || '').trim()
  if (cleanText) {
    return (
      <div className="tool-output-stack">
        <pre className="tool-output">{text}</pre>
        {dedupedEntries.length > 0 && (
          <div className="tool-params-footer">
            <button
              type="button"
              className="tool-rich-btn is-muted"
              onClick={() => setShowParams(v => !v)}
            >
              {showParams ? '收起参数' : `查看参数 (${dedupedEntries.length})`}
            </button>
            {showParams && (
              <button type="button" className="tool-rich-btn is-muted" onClick={() => setShowRaw(true)}>
                JSON
              </button>
            )}
          </div>
        )}
        {showParams && dedupedEntries.length > 0 && (
          <div className="tool-kv-card">
            <div className="tool-kv-list">
              {dedupedEntries.map((entry) => (
                <div key={entry.key} className="tool-kv-row">
                  <span className="tool-kv-key">{entry.label}</span>
                  <span className="tool-kv-val">{formatParamValue(entry.value)}</span>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    )
  }

  // 无输出、仅参数时：渲染克制的结构化键值卡片 + 右上角 JSON 切换
  if (dedupedEntries.length > 0) {
    return (
      <div className="tool-rich-view tool-kv-card">
        <div className="tool-rich-header">
          <span className="tool-rich-title">调用参数</span>
          <button type="button" className="tool-rich-btn is-muted" onClick={() => setShowRaw(true)}>
            JSON
          </button>
        </div>
        <div className="tool-kv-list">
          {dedupedEntries.map((entry) => {
            const valStr = formatParamValue(entry.value)
            const isPathLike = (entry.key === 'path' || entry.key === 'ScriptPath') && Boolean(session) && typeof entry.value === 'string'
            return (
              <div key={entry.key} className="tool-kv-row">
                <span className="tool-kv-key">{entry.label}</span>
                {isPathLike && session ? (
                  <button
                    type="button"
                    className="tool-kv-link"
                    onClick={(e) => {
                      e.stopPropagation()
                      openToolFileInSidecar(String(entry.value), session)
                    }}
                    title="在右侧栏打开文件"
                  >
                    {valStr}
                  </button>
                ) : (
                  <span className="tool-kv-val">{valStr}</span>
                )}
              </div>
            )
          })}
        </div>
      </div>
    )
  }

  return null
}
