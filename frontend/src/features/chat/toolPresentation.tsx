import { ShinyText } from '../../components/animations/ShinyText'
import {
  IconBolt,
  IconBulb,
  IconCheck,
  IconCopy,
  IconEdit,
  IconExternalLink,
  IconFileText,
  IconGitPullRequest,
  IconPhoto,
  IconSearch,
  IconTerminal2,
  IconTool,
  IconX,
} from '@tabler/icons-react'
import type { Message, Session } from '../../domain/types'
import { useState, type MouseEvent } from 'react'
import { artifactViewerRegistry } from '../sidecar/registry'
import { resolveArtifactFromPath } from '../sidecar/resolver'
import { useSidecarStore } from '../sidecar/sidecarStore'

export type ToolIconKey =
  | 'terminal'
  | 'thinking'
  | 'edit'
  | 'read'
  | 'search'
  | 'image'
  | 'mcp'
  | 'tool'

const IMAGE_FILE_EXT_RE = /\.(?:png|jpe?g|gif|webp|svg|bmp|ico|avif)$/i

export function isImageFilePath(rawPath: string): boolean {
  if (!rawPath) return false
  const clean = rawPath.trim().split(/[?#]/)[0]
  return IMAGE_FILE_EXT_RE.test(clean)
}

export function cleanThinkingHeadline(rawText: string): string {
  const lines = rawText.trim().split('\n')
  for (const rawLine of lines) {
    const cleaned = rawLine
      .replace(/^\s*(?:#+\s*|[-*+]\s+|\d+\.\s+|>\s*)+/, '')
      .replace(/\[([^\]]+)\]\([^)]+\)/g, '$1')
      .replace(/`([^`]+)`/g, '$1')
      .replace(/\*\*([^*]+)\*\*/g, '$1')
      .replace(/__([^_]+)__/g, '$1')
      .replace(/\*([^*]+)\*/g, '$1')
      .replace(/\s+/g, ' ')
      .trim()
    if (cleaned.length > 0) return cleaned
  }
  return ''
}

export function unwrapCommand(raw: string): string {
  if (!raw) return ''
  let trimmed = raw.trim()

  // 1. Windows pwsh / powershell 包装调用
  const pwshPattern = /^(?:"[^"]*(?:pwsh|powershell)(?:\.exe)?"|'[^']*(?:pwsh|powershell)(?:\.exe)?'|(?:[^\s"']*[\\/])?(?:pwsh|powershell)(?:\.exe)?)\s+[\s\S]*?(?:-Command|-c)\s+(['"])([\s\S]*)\1\s*$/i
  let match = trimmed.match(pwshPattern)
  if (match) {
    let inner = match[2]
    if (match[1] === '"') {
      inner = inner.replace(/\\"/g, '"').replace(/\\\\/g, '\\')
    } else {
      inner = inner.replace(/\\'/g, "'")
    }
    trimmed = inner.trim()
  }

  // 2. Unix shells (bash, zsh, sh -c "...")
  const unixPattern = /^(?:\/(?:usr\/)?bin\/)?(?:zsh|bash|sh)\s+-[a-zA-Z]*c\s+(['"])([\s\S]*)\1\s*$/i
  match = trimmed.match(unixPattern)
  if (match) {
    let inner = match[2]
    if (match[1] === '"') {
      inner = inner.replace(/\\"/g, '"').replace(/\\\\/g, '\\')
    } else {
      inner = inner.replace(/\\'/g, "'")
    }
    trimmed = inner.trim()
  }

  // 3. cmd /c "..."
  const cmdPattern = /^(?:"[^"]*cmd(?:\.exe)?"|'[^']*cmd(?:\.exe)?'|(?:[^\s"']*[\\/])?cmd(?:\.exe)?)\s+\/[a-zA-Z]\s+(['"]?)([\s\S]*?)\1\s*$/i
  match = trimmed.match(cmdPattern)
  if (match) {
    trimmed = match[2].trim()
  }

  // 4. 清洗 PowerShell UTF-8 编码设置样板代码
  const utf8Boilerplate = /^try\s*\{\s*\[Console\]::OutputEncoding\s*=\s*\[System\.Text\.Encoding\]::UTF8\s*\}\s*catch\s*\{\s*\}\s*;?\s*[\r\n]*/i
  trimmed = trimmed.replace(utf8Boilerplate, '').trim()

  return trimmed
}

export interface ToolDescription {
  iconKey: ToolIconKey
  icon?: string
  action: string
  target: string
  summary?: string
  fullTitle: string
  isFailed: boolean
  isRunning: boolean
}

export function extractToolArgs(tool: Message['tool'] | null | undefined): Record<string, unknown> {
  if (!tool || typeof tool !== 'object') return {}
  const record = tool as Record<string, unknown>
  if (record.arguments && typeof record.arguments === 'object' && !Array.isArray(record.arguments)) {
    const args = record.arguments as Record<string, unknown>
    if (Object.keys(args).length > 0) return args
  }
  const rawInput = record.input
  if (rawInput && typeof rawInput === 'object' && !Array.isArray(rawInput)) {
    return rawInput as Record<string, unknown>
  }
  if (typeof rawInput === 'string') {
    const trimmed = rawInput.trim()
    if (trimmed.startsWith('{') && trimmed.endsWith('}')) {
      try {
        const parsed = JSON.parse(trimmed)
        if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
          return parsed as Record<string, unknown>
        }
      } catch {}
    }
  }
  if (record.arguments && typeof record.arguments === 'object' && !Array.isArray(record.arguments)) {
    return record.arguments as Record<string, unknown>
  }
  return {}
}

function firstStringArg(args: Record<string, unknown>, ...keys: string[]): string {
  for (const key of keys) {
    const val = args[key]
    if (typeof val === 'string' && val.trim() !== '') {
      return val
    }
  }
  return ''
}

function firstNumberArg(args: Record<string, unknown>, ...keys: string[]): number | null {
  for (const key of keys) {
    const val = args[key]
    if (typeof val === 'number' && Number.isFinite(val)) return val
    if (typeof val === 'string' && val.trim() !== '') {
      const n = Number(val)
      if (Number.isFinite(n)) return n
    }
  }
  return null
}

function isToolFailed(status: unknown): boolean {
  return status === 'failed' || status === 'error'
}

export function activityState(message: Message): 'completed' | 'failed' | 'running' | 'recorded' {
  if (isToolFailed(message.tool?.status)) return 'failed'
  if (message.tool?.status === 'running') return 'running'
  if (message.tool?.status === 'completed' || message.tool?.status === 'succeeded' || message.completed_at) return 'completed'
  return 'recorded'
}

export function activityDuration(message: Message): string | null {
  if (!message.completed_at) return null
  const start = Date.parse(message.created_at)
  const end = Date.parse(message.completed_at)
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) return null
  const seconds = Math.round((end - start) / 1000)
  if (seconds < 1) return '不到 1 秒'
  return seconds >= 60 ? `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒` : `${seconds} 秒`
}

export interface FileChangeDiffItem {
  file: string
  path?: string
  diff: string
}

function buildMultiReplaceDiffFromChunks(fileName: string, rawChunks: unknown): string {
  let chunks: Array<Record<string, unknown>> = []
  if (Array.isArray(rawChunks)) {
    chunks = rawChunks.filter((el): el is Record<string, unknown> => Boolean(el && typeof el === 'object'))
  } else if (typeof rawChunks === 'string' && rawChunks.trim().startsWith('[')) {
    try {
      const parsed = JSON.parse(rawChunks)
      if (Array.isArray(parsed)) {
        chunks = parsed.filter((el): el is Record<string, unknown> => Boolean(el && typeof el === 'object'))
      }
    } catch {}
  }
  if (!chunks.length) return ''
  const parts: string[] = [`--- a/${fileName}`, `+++ b/${fileName}`]
  for (const ch of chunks) {
    const oldStr = firstStringArg(ch, 'TargetContent', 'old_string', 'Target')
    const newStr = firstStringArg(ch, 'ReplacementContent', 'new_string', 'Replacement')
    if (!oldStr && !newStr) continue
    const startLine = firstNumberArg(ch, 'StartLine', 'start_line')
    parts.push(startLine && startLine > 0 ? `@@ -${startLine} @@` : '@@')
    if (oldStr) parts.push(oldStr.replace(/\r\n/g, '\n').split('\n').map(l => '-' + l).join('\n'))
    if (newStr) parts.push(newStr.replace(/\r\n/g, '\n').split('\n').map(l => '+' + l).join('\n'))
  }
  return parts.length > 2 ? parts.join('\n') : ''
}

export function fileChangeDiffs(message: Message): FileChangeDiffItem[] {
  const tool = message.tool
  const args = extractToolArgs(tool)
  const name = String(tool?.name || '')

  if (Array.isArray(args.changes)) {
    const list = args.changes.flatMap((change: unknown) => {
      if (!change || typeof change !== 'object') return []
      const item = change as { path?: unknown; diff?: unknown }
      if (typeof item.diff !== 'string' || !item.diff.trim()) return []
      const fullPath = typeof item.path === 'string' ? item.path : ''
      const fileName = (fullPath ? fullPath.split(/[/\\]/).pop() : '') || fullPath || '文件变更'
      return [fullPath ? { file: fileName, path: fullPath, diff: item.diff } : { file: fileName, diff: item.diff }]
    })
    if (list.length > 0) return list
  }

  if (name === 'patch' || name === 'fileChange') {
    const text = (message.text || '').trim()
    if (text.startsWith('{') && text.endsWith('}')) {
      try {
        const parsed = JSON.parse(text)
        if (typeof parsed?.diff === 'string' && parsed.diff.trim()) {
          const filePath = firstStringArg(args, 'path', 'Path', 'TargetFile', 'AbsolutePath') || String(parsed.path || parsed.file || '文件变更')
          const fileName = filePath.split(/[/\\]/).pop() || filePath
          return [{ file: fileName, path: filePath, diff: parsed.diff }]
        }
      } catch {}
    }
  }

  const filePath = firstStringArg(args, 'path', 'Path', 'TargetFile', 'AbsolutePath')
  const oldStr = firstStringArg(args, 'old_string', 'Target', 'TargetContent')
  const newStr = firstStringArg(args, 'new_string', 'Replacement', 'ReplacementContent')
  if (filePath && (oldStr !== '' || newStr !== '')) {
    const fileName = filePath.split(/[/\\]/).pop() || filePath
    const oldLines = oldStr ? oldStr.replace(/\r\n/g, '\n').split('\n').map((l: string) => '-' + l).join('\n') + '\n' : ''
    const newLines = newStr ? newStr.replace(/\r\n/g, '\n').split('\n').map((l: string) => '+' + l).join('\n') : ''
    const diff = '--- a/' + fileName + '\n+++ b/' + fileName + '\n@@\n' + oldLines + newLines
    return [{ file: fileName, path: filePath, diff }]
  }

  if (filePath && args.ReplacementChunks) {
    const fileName = filePath.split(/[/\\]/).pop() || filePath
    const diff = buildMultiReplaceDiffFromChunks(fileName, args.ReplacementChunks)
    if (diff) return [{ file: fileName, path: filePath, diff }]
  }

  if (
    filePath &&
    (name === 'write_file' || name === 'write_to_file')
  ) {
    const content = firstStringArg(args, 'Content', 'CodeContent', 'content')
    if (content) {
      const fileName = filePath.split(/[/\\]/).pop() || filePath
      const capped = content.length > 65536 ? `${content.slice(0, 65536)}\n... (truncated)` : content
      const added = capped.replace(/\r\n/g, '\n').split('\n').map((l: string) => '+' + l).join('\n')
      const diff = `--- /dev/null\n+++ b/${fileName}\n@@\n${added}`
      return [{ file: fileName, path: filePath, diff }]
    }
  }

  return []
}

function openFileInSidecar(filePath: string, session: Session, line?: number) {
  const artifact = resolveArtifactFromPath(filePath.replace(/%/g, '%25'), session)
  if (line && line > 0) {
    artifact.metadata = { ...(artifact.metadata || {}), line }
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

function DiffCard({ change, session }: { change: FileChangeDiffItem; session?: Session | null }) {
  const [copied, setCopied] = useState(false)
  const rawLines = change.diff.split('\n')
  let added = 0
  let removed = 0
  for (const ln of rawLines) {
    if (ln.startsWith('+') && !ln.startsWith('+++')) added++
    else if (ln.startsWith('-') && !ln.startsWith('---')) removed++
  }
  const lines = rawLines.filter((ln) => {
    const t = ln.trim()
    if (ln.startsWith('--- ') || ln.startsWith('+++ ')) return false
    if (t === '@@' || t === '@@ @@') return false
    return true
  })

  const handleCopy = (e: MouseEvent<HTMLButtonElement>) => {
    e.stopPropagation()
    void navigator.clipboard.writeText(change.diff)
    setCopied(true)
    setTimeout(() => setCopied(false), 1600)
  }

  return (
    <div className="tool-rich-view tool-diff-view">
      <div className="tool-rich-header">
        <span className="tool-rich-title" title={change.path || change.file}>
          <IconGitPullRequest size={13} className="tool-rich-title-icon" />
          <span className="tool-rich-filename">{change.file}</span>
          {added > 0 && <span className="tool-diff-stat is-add">+{added}</span>}
          {removed > 0 && <span className="tool-diff-stat is-del">-{removed}</span>}
        </span>
        <div className="tool-rich-actions">
          {session && change.path && (
            <button
              type="button"
              className="tool-rich-btn"
              onClick={(e) => {
                e.stopPropagation()
                openFileInSidecar(change.path!, session)
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
            onClick={handleCopy}
            title="复制差异内容"
            aria-label="复制差异"
          >
            {copied ? <IconCheck size={12} color="var(--astr-teal, #12b886)" /> : <IconCopy size={12} />}
          </button>
        </div>
      </div>
      <div className="tool-diff-body">
        {lines.map((line, idx) => {
          const isAdd = line.startsWith('+') && !line.startsWith('+++')
          const isDel = line.startsWith('-') && !line.startsWith('---')
          const isHunk = line.startsWith('@@')
          const isHeader = line.startsWith('--- ') || line.startsWith('+++ ')
          const cls = isAdd
            ? 'tool-diff-line is-add'
            : isDel
              ? 'tool-diff-line is-del'
              : isHunk
                ? 'tool-diff-line is-hunk'
                : isHeader
                  ? 'tool-diff-line is-meta'
                  : 'tool-diff-line'
          return (
            <div key={idx} className={cls}>
              {line || ' '}
            </div>
          )
        })}
      </div>
    </div>
  )
}

export function FileChangeDiffBlock({ message, session }: { message: Message; session?: Session | null }) {
  const changes = fileChangeDiffs(message)
  if (!changes.length) return null
  return (
    <div className="file-change-diffs">
      {changes.map((change, index) => (
        <DiffCard key={`${change.file}-${index}`} change={change} session={session} />
      ))}
    </div>
  )
}

export function ToolLineIcon({
  icon,
  size = 14,
  className,
}: {
  icon: ToolIconKey
  size?: number
  className?: string
}) {
  const props = { size, stroke: 1.8, className }
  switch (icon) {
    case 'terminal':
      return <IconTerminal2 {...props} />
    case 'thinking':
      return <IconBulb {...props} />
    case 'edit':
      return <IconEdit {...props} />
    case 'read':
      return <IconFileText {...props} />
    case 'search':
      return <IconSearch {...props} />
    case 'image':
      return <IconPhoto {...props} />
    case 'mcp':
      return <IconBolt {...props} />
    case 'tool':
    default:
      return <IconTool {...props} />
  }
}

export function describeTool(message: Message): ToolDescription {
  const isFailed = isToolFailed(message.tool?.status)
  const isRunning = message.tool?.status === 'running'

  if (message.kind === 'thinking') {
    const firstLine = cleanThinkingHeadline(message.text || '')
    const target = firstLine.length > 72 ? firstLine.slice(0, 69) + '...' : firstLine
    return {
      iconKey: 'thinking',
      action: '思考',
      target: target || '思考过程',
      fullTitle: target ? `思考: ${target}` : '思考过程',
      isFailed,
      isRunning,
    }
  }

  const tool = message.tool || { name: 'tool', arguments: {} }
  const name = String(tool.name || '')
  const args = extractToolArgs(tool)

  // 1. Shell commands
  if (
    name === 'commandExecution' ||
    name === 'terminal' ||
    name === 'bash' ||
    name === 'sh' ||
    name === 'exec' ||
    name === 'exec_command' ||
    name === 'run_command' ||
    name === 'command_status' ||
    name === 'send_command_input' ||
    Boolean(firstStringArg(args, 'command', 'cmd', 'CommandLine', 'Command'))
  ) {
    const rawCmd = firstStringArg(args, 'command', 'cmd', 'CommandLine', 'Command')
    const summaryFallback = firstStringArg(args, 'summary', 'toolSummary', 'Summary')
    const cmd = unwrapCommand(rawCmd) || summaryFallback || name
    let action = '运行'
    if (/^git\s/i.test(cmd)) action = 'Git'
    else if (/^(?:pytest|python\s+-m\s+pytest|npm\s+test|vitest|cargo\s+test|go\s+test)\b/i.test(cmd)) action = '测试'
    else if (/^(?:cat|head|tail|sed)\b/i.test(cmd)) action = '查看'
    else if (/^(?:rg|grep|find)\b/i.test(cmd)) action = '搜索'
    else if (/^(?:docker|systemctl|service)\b/i.test(cmd)) action = '服务'

    const target = cmd.length > 70 ? cmd.slice(0, 67) + '...' : cmd
    return {
      iconKey: 'terminal',
      action,
      target,
      summary: summaryFallback && summaryFallback !== cmd ? summaryFallback : undefined,
      fullTitle: cmd.length > 75 ? cmd.slice(0, 72) + '...' : cmd,
      isFailed,
      isRunning,
    }
  }

  // 2. File modification
  if (
    name === 'fileChange' ||
    name === 'patch' ||
    name === 'write_file' ||
    name === 'write_to_file' ||
    name === 'edit' ||
    name === 'edit_file' ||
    name === 'replace_file_content' ||
    name === 'multi_replace_file_content' ||
    name === 'sed_file' ||
    name === 'notebook_edit' ||
    args.changes
  ) {
    let file = ''
    if (Array.isArray(args.changes) && args.changes[0] && typeof args.changes[0] === 'object') {
      const paths = (args.changes as Array<{ path?: string }>).map(c => String(c.path || '').split(/[/\\]/).pop()).filter(Boolean)
      file = paths.slice(0, 2).join(', ') + (paths.length > 2 ? ` 等 ${paths.length} 个文件` : '')
    } else {
      const pathVal = firstStringArg(args, 'path', 'Path', 'TargetFile', 'AbsolutePath')
      if (pathVal) {
        file = pathVal.split(/[/\\]/).pop() || pathVal
      }
    }
    const isWrite = name === 'write_file' || name === 'write_to_file'
    const action = isWrite ? '写入' : '编辑'
    const target = file || '文件'
    return {
      iconKey: 'edit',
      action,
      target,
      fullTitle: `${action}文件: ${target}`,
      isFailed,
      isRunning,
    }
  }

  // 3. Directory listing & URL reading
  if (name === 'list_dir') {
    const dirVal = firstStringArg(args, 'DirectoryPath', 'path', 'Path', 'AbsolutePath')
    const dirName = dirVal.replace(/[/\\]+$/, '').split(/[/\\]/).pop() || dirVal || '目录'
    return {
      iconKey: 'read',
      action: '浏览目录',
      target: dirName,
      fullTitle: `浏览目录: ${dirName}`,
      isFailed,
      isRunning,
    }
  }

  if (name === 'read_url_content' || name === 'open_browser_url') {
    const rawUrl = firstStringArg(args, 'Url', 'url', 'Uri', 'uri')
    const shortUrl = rawUrl.length > 56 ? rawUrl.slice(0, 53) + '...' : rawUrl
    return {
      iconKey: 'search',
      action: name === 'open_browser_url' ? '浏览器' : '抓取网页',
      target: shortUrl || '网页',
      fullTitle: shortUrl ? `抓取网页: ${shortUrl}` : '抓取网页',
      isFailed,
      isRunning,
    }
  }

  // 4. File read (with image detection!)
  if (
    name === 'read_file' ||
    name === 'fileRead' ||
    name === 'view_file' ||
    name === 'view_file_outline' ||
    name === 'view_code_item' ||
    name === 'read_resource' ||
    (name === 'read' && Boolean(firstStringArg(args, 'path', 'Path', 'AbsolutePath')))
  ) {
    const pathVal = firstStringArg(args, 'path', 'Path', 'AbsolutePath', 'File', 'TargetFile', 'DirectoryPath', 'Uri', 'Url')
    let file = pathVal.split(/[/\\]/).pop() || pathVal

    if (pathVal && isImageFilePath(pathVal)) {
      const target = file || '图片'
      return {
        iconKey: 'image',
        action: '查看图片',
        target,
        fullTitle: `查看图片: ${target}`,
        isFailed,
        isRunning,
      }
    }

    if (file) {
      const startLine = firstNumberArg(args, 'start_line', 'StartLine', 'offset')
      const endLine = firstNumberArg(args, 'end_line', 'EndLine')
      if (startLine != null && endLine != null) {
        file = `${file}:${startLine}-${endLine}`
      } else if (startLine != null) {
        file = `${file}:${startLine}`
      }
    }
    const target = file || '文件'
    return {
      iconKey: 'read',
      action: '读取',
      target,
      fullTitle: `读取文件: ${target}`,
      isFailed,
      isRunning,
    }
  }

  // 5. Search
  if (
    name === 'search_files' ||
    name === 'fileSearch' ||
    name === 'grep' ||
    name === 'web_search' ||
    name === 'grep_search' ||
    name === 'find_by_name' ||
    name === 'search_web' ||
    name === 'SearchContext'
  ) {
    const isWeb = name === 'search_web' || name === 'web_search'
    const q = firstStringArg(args, 'pattern', 'query', 'Pattern', 'Query', 'SearchPath', 'SearchDirectory', 'Directory')
    const target = q.length > 44 ? q.slice(0, 41) + '...' : q
    const action = isWeb ? '联网搜索' : '搜索'
    return {
      iconKey: 'search',
      action,
      target: target ? `“${target}”` : '',
      fullTitle: target ? `${action} “${target}”` : action,
      isFailed,
      isRunning,
    }
  }

  // 6. Image view
  if (name === 'imageView' || name === 'image_view') {
    const p = firstStringArg(args, 'path', 'Path', 'AbsolutePath')
    const file = p.split(/[/\\]/).pop() || p
    const target = file || '图片'
    return {
      iconKey: 'image',
      action: '查看图片',
      target,
      fullTitle: `查看图片: ${target}`,
      isFailed,
      isRunning,
    }
  }

  // 7. Antigravity orchestration / agent tools
  if (name === 'swarm') {
    const subAction = firstStringArg(args, 'action')
    const memberName = firstStringArg(args, 'name', 'title')
    const target = [subAction, memberName].filter(Boolean).join(' ') || '协同'
    return {
      iconKey: 'mcp',
      action: 'Swarm',
      target,
      fullTitle: `Swarm: ${target}`,
      isFailed,
      isRunning,
    }
  }
  if (name === 'post_message' || name === 'read_board' || name === 'swarm_wait') {
    const actionMap: Record<string, string> = {
      post_message: '看板发言',
      read_board: '读取看板',
      swarm_wait: '等待看板',
    }
    const action = actionMap[name]
    const rawMsg = firstStringArg(args, 'text')
    const since = firstNumberArg(args, 'since')
    const target = rawMsg
      ? (rawMsg.length > 44 ? rawMsg.slice(0, 41) + '...' : rawMsg)
      : since != null
        ? `since #${since}`
        : '最新消息'
    return {
      iconKey: 'mcp',
      action,
      target,
      fullTitle: `${action}: ${target}`,
      isFailed,
      isRunning,
    }
  }
  if (name === 'invoke_subagent' || name === 'define_subagent' || name === 'manage_subagents') {
    const actionMap: Record<string, string> = {
      invoke_subagent: '子代理',
      define_subagent: '定义子代理',
      manage_subagents: '管理子代理',
    }
    const action = actionMap[name]
    const target = firstStringArg(args, 'Role', 'Name', 'Action', 'TypeName') || name
    return {
      iconKey: 'mcp',
      action,
      target,
      fullTitle: `${action}: ${target}`,
      isFailed,
      isRunning,
    }
  }
  if (name === 'send_message') {
    const recipient = firstStringArg(args, 'Recipient', 'recipient')
    const shortId = recipient.length > 16 ? recipient.slice(0, 12) + '…' : recipient
    return {
      iconKey: 'mcp',
      action: '发送消息',
      target: shortId || 'Agent',
      fullTitle: `发送消息: ${shortId || 'Agent'}`,
      isFailed,
      isRunning,
    }
  }
  if (name === 'schedule') {
    const cron = firstStringArg(args, 'CronExpression')
    const dur = firstNumberArg(args, 'DurationSeconds')
    const target = cron || (dur != null ? `${dur}s 后唤醒` : '定时触发')
    return {
      iconKey: 'tool',
      action: '定时调度',
      target,
      fullTitle: `定时调度: ${target}`,
      isFailed,
      isRunning,
    }
  }
  if (name === 'manage_task') {
    const sub = firstStringArg(args, 'Action', 'action')
    const tid = firstStringArg(args, 'TaskId', 'task_id')
    const shortTid = tid.includes('/') ? tid.split('/').pop() || tid : tid
    const target = [sub, shortTid].filter(Boolean).join(' ') || '任务'
    return {
      iconKey: 'terminal',
      action: '后台任务',
      target,
      fullTitle: `后台任务: ${target}`,
      isFailed,
      isRunning,
    }
  }
  if (name === 'run_workflow') {
    const target = firstStringArg(args, 'Description', 'ScriptPath') || '工作流'
    return {
      iconKey: 'mcp',
      action: '工作流',
      target: target.length > 48 ? target.slice(0, 45) + '...' : target,
      fullTitle: `工作流: ${target}`,
      isFailed,
      isRunning,
    }
  }

  // 8. MCP
  if (name.startsWith('mcp:')) {
    const sub = name.slice(4)
    return {
      iconKey: 'mcp',
      action: 'MCP',
      target: sub,
      fullTitle: `MCP: ${sub}`,
      isFailed,
      isRunning,
    }
  }

  // Fallback
  // Grok terminal tool names like: Execute `cmd` or [bg] cmd (call-xxx)
  if (name.startsWith('Execute `') || (name.startsWith('[bg] ') && Boolean(args.command)) || /\(call-[a-z0-9]+\)$/i.test(name)) {
    const rawCmd = String(args.command || name.replace(/^Execute `|`$/g, '').replace(/^\[bg\]\s*/, '').replace(/\s*\(call-[a-z0-9]+\)$/i, ''))
    const cmd = unwrapCommand(rawCmd) || rawCmd
    let action = '运行'
    if (/^git\s/i.test(cmd)) action = 'Git'
    else if (/^(?:pytest|python\s+-m\s+pytest|npm\s+test|vitest|cargo\s+test)\b/i.test(cmd)) action = '测试'
    else if (/^(?:cat|head|tail|sed)\b/i.test(cmd)) action = '查看'
    else if (/^(?:rg|grep|find)\b/i.test(cmd)) action = '搜索'
    else if (/^(?:docker|systemctl|service)\b/i.test(cmd)) action = '服务'
    const target = cmd.length > 70 ? cmd.slice(0, 67) + '...' : cmd
    return {
      iconKey: 'terminal',
      action,
      target,
      fullTitle: cmd.length > 75 ? cmd.slice(0, 72) + '...' : cmd,
      isFailed,
      isRunning,
    }
  }

  return {
    iconKey: 'tool',
    action: '工具',
    target: name,
    fullTitle: `工具 · ${name}`,
    isFailed,
    isRunning,
  }
}

export function formatCommandRowDisplay(
  desc: ToolDescription,
  unwrappedCmd: string,
): { action: string; text: string; isHumanSummary: boolean; tooltip: string } {
  const cmd = unwrappedCmd || desc.target || desc.fullTitle
  const hasNoisyPrefix = /^(?:[A-Z_][A-Z0-9_]*=\S+\s+|cd\s+\S+\s*&&\s*)/.test(cmd)
  const isLongOrMultiline = cmd.length > 48 || cmd.includes('\n')
  const action = desc.isRunning ? '运行' : desc.isFailed ? '运行失败' : desc.action === '运行' ? '已运行' : desc.action
  if (desc.summary && (hasNoisyPrefix || isLongOrMultiline)) {
    return {
      action,
      text: desc.summary,
      isHumanSummary: true,
      tooltip: cmd,
    }
  }
  const singleLine = cmd.replace(/\s*\r?\n\s*/g, ' ').trim()
  return {
    action,
    text: singleLine.length > 72 ? singleLine.slice(0, 69) + '...' : singleLine,
    isHumanSummary: false,
    tooltip: cmd,
  }
}

export function formatPackSummary(pack: Message[]): string {
  if (!pack.length) return '思考与工具'
  if (pack.length === 1) {
    const desc = describeTool(pack[0])
    return `${desc.fullTitle}${desc.isFailed ? ' · 失败' : desc.isRunning ? ' · 运行中' : ''}`
  }

  const hasFailed = pack.some(m => isToolFailed(m.tool?.status))
  const isRunning = pack.some(m => m.tool?.status === 'running')

  const descriptions = pack.map(describeTool)
  const isAllCmd = descriptions.every(d => d.iconKey === 'terminal')
  const isAllThinking = descriptions.every(d => d.iconKey === 'thinking')

  let title = ''
  if (isAllCmd) {
    title = `运行了 ${pack.length} 个命令`
  } else if (isAllThinking) {
    title = `思考过程 · ${pack.length} 项`
  } else {
    const actions: string[] = []
    const seen = new Set<ToolIconKey>()
    for (const d of descriptions) {
      if (!seen.has(d.iconKey)) {
        seen.add(d.iconKey)
        actions.push(d.action)
      }
    }
    title = `${actions.slice(0, 3).join(' · ')} (共 ${pack.length} 项)`
  }

  if (hasFailed) title += ' · 有失败项'
  else if (isRunning) title += ' · 运行中'

  return title
}

export function PackSummary({ pack, isRunning: propIsRunning }: { pack: Message[]; isRunning?: boolean }) {
  if (!pack.length) return <span>思考与工具</span>

  const hasFailed = pack.some(m => isToolFailed(m.tool?.status))
  const isRunning = propIsRunning ?? pack.some(m => m.tool?.status === 'running')

  if (propIsRunning) {
    const toolItems = pack.filter(message => message.kind !== 'thinking')
    if (toolItems.length === 0) {
      return (
        <span className="pack-summary-row pack-summary-running">
          <strong>正在思考</strong>
          <span className="pack-summary-progress" aria-label={`思考 ${pack.length} 段`}>{pack.length} 段</span>
        </span>
      )
    }
    const completed = toolItems.filter(message => ['completed', 'failed'].includes(activityState(message))).length
    return (
      <span className="pack-summary-row pack-summary-running">
        <strong>正在执行</strong>
        <span className="pack-summary-progress" aria-label={`已处理 ${completed} 项，共 ${toolItems.length} 项`}>{completed} / {toolItems.length}</span>
      </span>
    )
  }

  if (pack.length === 1) {
    const desc = describeTool(pack[0])
    return (
      <span className="pack-summary-row">
        <ToolLineIcon icon={desc.iconKey} size={14} className="pack-summary-icon" />
        <span className="pack-summary-title">{desc.fullTitle}</span>
        {desc.isFailed && <span className="activity-badge is-failed">失败</span>}
        {isRunning && <span className="activity-badge is-running">执行中</span>}
      </span>
    )
  }

  const descriptions = pack.map(describeTool)
  const isAllCmd = descriptions.every(d => d.iconKey === 'terminal')
  const isAllThinking = descriptions.every(d => d.iconKey === 'thinking')

  // 计算整个过程包的耗时（起始时间至结束时间）
  let durationText = ''
  const timestamps = pack.map(m => Date.parse(m.created_at)).filter(t => !isNaN(t) && t > 0)
  if (timestamps.length >= 2) {
    const totalSecs = Math.max(1, Math.round((Math.max(...timestamps) - Math.min(...timestamps)) / 1000))
    const m = Math.floor(totalSecs / 60)
    const s = totalSecs % 60
    durationText = m > 0 ? `用时 ${m} 分钟 ${s} 秒` : `用时 ${s} 秒`
  }

  if (isAllCmd) {
    return (
      <span className="pack-summary-row">
        <ToolLineIcon icon="terminal" size={14} className="pack-summary-icon" />
        <span>运行了 {pack.length} 个命令{durationText ? ` · ${durationText}` : ''}</span>
        {hasFailed && <span className="activity-badge is-failed">有失败项</span>}
        {isRunning && <span className="activity-badge is-running"><ShinyText text="运行中" speed={1.5} /></span>}
      </span>
    )
  }

  if (isAllThinking) {
    return (
      <span className="pack-summary-row">
        <ToolLineIcon icon="thinking" size={14} className="pack-summary-icon" />
        <span>{durationText || `思考过程 · ${pack.length} 项`}</span>
        {isRunning && <span className="activity-badge is-running"><ShinyText text="运行中" speed={1.5} /></span>}
      </span>
    )
  }

  const distinct: Array<{ iconKey: ToolIconKey; label: string }> = []
  const seen = new Set<ToolIconKey>()
  for (const d of descriptions) {
    if (!seen.has(d.iconKey)) {
      seen.add(d.iconKey)
      distinct.push({ iconKey: d.iconKey, label: d.action })
    }
  }

  return (
    <span className="pack-summary-row">
      {distinct.slice(0, 3).map((item, idx) => (
        <span key={item.iconKey} className="pack-summary-chunk">
          {idx > 0 && <span className="pack-summary-dot">·</span>}
          <ToolLineIcon icon={item.iconKey} size={14} className="pack-summary-icon" />
          <span>{item.label}</span>
        </span>
      ))}
      <span className="pack-summary-count">(共 {pack.length} 项{durationText ? ` · ${durationText}` : ''})</span>
      {hasFailed && <span className="activity-badge is-failed">有失败项</span>}
      {isRunning && <span className="activity-badge is-running"><ShinyText text="运行中" speed={1.5} /></span>}
    </span>
  )
}

export function ShellOutputBlock({
  command,
  output,
  status = 'success',
  summary,
  cwd,
  exitCode,
}: {
  command: string
  output?: string | null
  status?: 'success' | 'failed' | 'running'
  summary?: string
  cwd?: string
  exitCode?: number | null
}) {
  const [copied, setCopied] = useState(false)
  const handleCopy = (e: MouseEvent<HTMLButtonElement>) => {
    e.stopPropagation()
    const content = output ? `$ ${command}\n${output}` : `$ ${command}`
    void navigator.clipboard.writeText(content)
    setCopied(true)
    setTimeout(() => setCopied(false), 1600)
  }

  return (
    <div className="shell-terminal-card" aria-label="终端执行输出">
      <div className="shell-terminal-header">
        <span className="shell-terminal-label">
          Shell
          {summary && <span className="shell-terminal-summary">· {summary}</span>}
        </span>
        <button
          type="button"
          className="shell-terminal-copy"
          onClick={handleCopy}
          title="复制终端命令与输出"
          aria-label="复制终端输出"
        >
          {copied ? <IconCheck size={12} color="var(--astr-teal, #12b886)" /> : <IconCopy size={12} />}
        </button>
      </div>
      <div className="shell-terminal-body">
        <div className="shell-command-line">
          <span className="shell-prompt">$</span>
          <span className="shell-command-text">{command}</span>
        </div>
        {output && <div className="shell-output-text">{output}</div>}
      </div>
      <div className="shell-terminal-footer">
        {status === 'failed' ? (
          <span className="shell-status-badge is-failed">
            <IconX size={12} /> 失败{typeof exitCode === 'number' && exitCode !== 0 ? ` (退出码 ${exitCode})` : ''}
          </span>
        ) : status === 'running' ? (
          <span className="shell-status-badge is-running">
            <ShinyText text="执行中…" speed={1.5} />
          </span>
        ) : (
          <span className="shell-status-badge is-success">
            <IconCheck size={12} /> 成功
          </span>
        )}
        {cwd && <span className="shell-terminal-cwd" title={cwd}>{cwd}</span>}
      </div>
    </div>
  )
}
