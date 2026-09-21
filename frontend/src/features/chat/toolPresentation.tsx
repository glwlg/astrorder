import { ShinyText } from '../../components/animations/ShinyText'
import {
  IconBolt,
  IconSparkles,
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
import { api } from '../../api/client'
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

export function fileChangeDiffs(message: Message): Array<{ file: string; diff: string }> {
  const args = message.tool?.arguments
  if (!args || typeof args !== 'object') return []
  const changes = (args as Record<string, unknown>).changes
  if (!Array.isArray(changes)) return []
  return changes.flatMap((change: unknown) => {
    if (!change || typeof change !== 'object') return []
    const item = change as { path?: unknown; diff?: unknown }
    if (typeof item.diff !== 'string' || !item.diff.trim()) return []
    const path = typeof item.path === 'string' ? item.path : '文件变更'
    return [{ file: path.split(/[/\\]/).pop() || path, diff: item.diff }]
  })
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
  const isFailed = message.tool?.status === 'failed'
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
 const args = (tool.arguments && typeof tool.arguments === 'object') ? (tool.arguments as Record<string, unknown>) : {}

  // 1. Shell commands
  if (name === 'commandExecution' || name === 'terminal' || name === 'bash' || name === 'sh' || name === 'exec' || name === 'exec_command' || Boolean(args.command) || Boolean(args.cmd)) {
    const rawCmd = String(args.command || args.cmd || '')
    const cmd = unwrapCommand(rawCmd) || name
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

  // 2. File modification
  if (name === 'fileChange' || name === 'patch' || name === 'write_file' || name === 'edit' || args.changes) {
    let file = ''
    if (Array.isArray(args.changes) && args.changes[0] && typeof args.changes[0] === 'object') {
      const paths = (args.changes as Array<{ path?: string }>).map(c => String(c.path || '').split(/[/\\\\]/).pop()).filter(Boolean)
      file = paths.slice(0, 2).join(', ') + (paths.length > 2 ? ` 等 ${paths.length} 个文件` : '')
    } else if (args.path) {
      file = String(args.path).split(/[/\\\\]/).pop() || String(args.path)
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
  if (name === 'read_file' || name === 'fileRead' || (name === 'read' && args.path)) {
    const file = String(args.path || '').split(/[/\\\\]/).pop() || String(args.path || '')
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
  if (name === 'search_files' || name === 'fileSearch' || name === 'grep' || name === 'web_search') {
    const q = String(args.pattern || args.query || '')
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

  const hasFailed = pack.some(m => m.tool?.status === 'failed')
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

  const hasFailed = pack.some(m => m.tool?.status === 'failed')
  const isRunning = propIsRunning ?? pack.some(m => m.tool?.status === 'running')

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
  const [filteredOutput, setFilteredOutput] = useState<string | null>(null)
  const [filtering, setFiltering] = useState(false)
  const [showFiltered, setShowFiltered] = useState(true)

  const handleJevFilter = async (e: MouseEvent<HTMLButtonElement>) => {
    e.stopPropagation()
    if (filteredOutput !== null) {
      setShowFiltered(!showFiltered)
      return
    }
    if (!output || filtering) return
    setFiltering(true)
    try {
      const res = await api.filterWithJev({ command, output })
      setFilteredOutput(res.filtered)
      setShowFiltered(true)
    } catch {
      // fallback
    } finally {
      setFiltering(false)
    }
  }
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
        {output && output.length > 250 && (
          <button
            type="button"
            className={`shell-terminal-copy ${filteredOutput && showFiltered ? 'is-active' : ''}`}
            onClick={handleJevFilter}
            title={filteredOutput ? (showFiltered ? '显示完整原始日志' : '切换至 Jev 降噪精简视图') : '使用 Jev 语义降噪过滤'}
            aria-label="Jev 语义降噪"
            style={{ display: 'inline-flex', alignItems: 'center', gap: 4, padding: '2px 6px', width: 'auto', fontSize: 10, color: filteredOutput && showFiltered ? '#10b981' : 'var(--astr-muted)' }}
          >
            <IconSparkles size={11} />
            <span>{filtering ? '降噪中…' : (filteredOutput ? (showFiltered ? '已降噪 (查看原始)' : '查看降噪') : 'Jev 降噪')}</span>
          </button>
        )}
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
        {output && <div className="shell-output-text">{filteredOutput && showFiltered ? filteredOutput : output}</div>}
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
