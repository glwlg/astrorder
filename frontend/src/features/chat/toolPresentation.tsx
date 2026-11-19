import { ShinyText } from '../../components/animations/ShinyText'
import {
  IconBolt,
  IconBulb,
  IconCheck,
  IconCopy,
  IconEdit,
  IconFileText,
  IconPhoto,
  IconSearch,
  IconTerminal2,
  IconTool,
  IconX,
} from '@tabler/icons-react'
import type { Message } from '../../domain/types'
import { useState, type MouseEvent } from 'react'
import { JsonRenderView } from '../monitor/BlackboardJsonRender'

export type ToolIconKey =
  | 'terminal'
  | 'thinking'
  | 'edit'
  | 'read'
  | 'search'
  | 'image'
  | 'mcp'
  | 'tool'

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

export function fileChangeDiffs(message: Message): Array<{ file: string; diff: string }> {
  const tool = message.tool
  const args = extractToolArgs(tool)
  const name = String(tool?.name || '')

  if (Array.isArray(args.changes)) {
    const list = args.changes.flatMap((change: unknown) => {
      if (!change || typeof change !== 'object') return []
      const item = change as { path?: unknown; diff?: unknown }
      if (typeof item.diff !== 'string' || !item.diff.trim()) return []
      const path = typeof item.path === 'string' ? item.path : '文件变更'
      return [{ file: path.split(/[/\\]/).pop() || path, diff: item.diff }]
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
          return [{ file: fileName, diff: parsed.diff }]
        }
      } catch {}
    }
  }

  const filePath = firstStringArg(args, 'path', 'Path', 'TargetFile', 'AbsolutePath')
  const oldStr = firstStringArg(args, 'old_string', 'Target', 'TargetContent')
  const newStr = firstStringArg(args, 'new_string', 'Replacement', 'ReplacementContent')
  if (filePath && (oldStr !== '' || newStr !== '')) {
    const fileName = filePath.split(/[/\\]/).pop() || filePath
    const oldLines = oldStr ? oldStr.split('\n').map((l: string) => '-' + l).join('\n') + '\n' : ''
    const newLines = newStr ? newStr.split('\n').map((l: string) => '+' + l).join('\n') : ''
    const diff = '--- a/' + fileName + '\n+++ b/' + fileName + '\n@@\n' + oldLines + newLines
    return [{ file: fileName, diff }]
  }

  return []
}

export function FileChangeDiffBlock({ message }: { message: Message }) {
  const changes = fileChangeDiffs(message)
  if (!changes.length) return null
  return (
    <div className="file-change-diffs">
      {changes.map((change, index) => (
        <JsonRenderView
          key={`${change.file}-${index}`}
          itemKey={change.file}
          itemValue={{ file: change.file, diff: change.diff }}
        />
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
    const rawText = (message.text || '').trim()
    const firstLine = rawText.split('\n').map(l => l.replace(/^#+\s*|\*\*|\*/g, '').trim()).find(l => l.length > 0) || ''
    const target = firstLine.length > 60 ? firstLine.slice(0, 57) + '...' : firstLine
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
    const target = file || '文件'
    return {
      iconKey: 'edit',
      action: '编辑',
      target,
      fullTitle: `编辑文件: ${target}`,
      isFailed,
      isRunning,
    }
  }

  // 3. File read
  if (
    name === 'read_file' ||
    name === 'fileRead' ||
    name === 'view_file' ||
    name === 'view_file_outline' ||
    name === 'view_code_item' ||
    name === 'list_dir' ||
    name === 'read_resource' ||
    name === 'read_url_content' ||
    (name === 'read' && Boolean(firstStringArg(args, 'path', 'Path', 'AbsolutePath')))
  ) {
    const pathVal = firstStringArg(args, 'path', 'Path', 'AbsolutePath', 'File', 'TargetFile', 'DirectoryPath', 'Uri', 'Url')
    let file = pathVal.split(/[/\\]/).pop() || pathVal
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

  // 4. Search
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
    const q = firstStringArg(args, 'pattern', 'query', 'Pattern', 'Query', 'SearchPath', 'SearchDirectory', 'Directory')
    const target = q.length > 40 ? q.slice(0, 37) + '...' : q
    return {
      iconKey: 'search',
      action: '搜索',
      target: target ? `“${target}”` : '',
      fullTitle: target ? `搜索 “${target}”` : '搜索',
      isFailed,
      isRunning,
    }
  }

  // 5. Image view
  if (name === 'imageView' || name === 'image_view') {
    const file = String(args.path || '').split(/[/\\\\]/).pop() || String(args.path || '')
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

  // 6. MCP
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
    const completed = pack.filter(message => ['completed', 'failed'].includes(activityState(message))).length
    return (
      <span className="pack-summary-row pack-summary-running">
        <strong>{pack.every(message => message.kind === 'thinking') ? '正在思考' : '正在执行'}</strong>
        <span className="pack-summary-progress" aria-label={`已处理 ${completed} 项，共 ${pack.length} 项`}>{completed} / {pack.length}</span>
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
  status = "success",
}: {
  command: string
  output?: string | null
  status?: "success" | "failed" | "running"
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
        <span className="shell-terminal-label">Shell</span>
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
        {status === "failed" ? (
          <span className="shell-status-badge is-failed">
            <IconX size={12} /> 失败
          </span>
        ) : status === "running" ? (
          <span className="shell-status-badge is-running">
            <ShinyText text="执行中…" speed={1.5} />
          </span>
        ) : (
          <span className="shell-status-badge is-success">
            <IconCheck size={12} /> 成功
          </span>
        )}
      </div>
    </div>
  )
}
