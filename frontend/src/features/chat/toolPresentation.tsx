import {
  IconBolt,
  IconBulb,
  IconEdit,
  IconFileText,
  IconPhoto,
  IconSearch,
  IconTerminal2,
  IconTool,
} from '@tabler/icons-react'
import type { Message } from '../../domain/types'

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
  const trimmed = raw.trim()
  const shellPattern = /^(?:\/(?:usr\/)?bin\/)?(?:zsh|bash|sh)\s+-[a-zA-Z]*c\s+(['"])([\s\S]*)\1\s*$/
  const match = trimmed.match(shellPattern)
  if (match) {
    let inner = match[2]
    if (match[1] === '"') {
      inner = inner.replace(/\\"/g, '"').replace(/\\\\/g, '\\')
    } else {
      inner = inner.replace(/\\'/g, "'")
    }
    return inner.trim()
  }
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
  if (name === 'commandExecution' || name === 'terminal' || name === 'bash' || name === 'sh' || name === 'exec' || args.command) {
    const rawCmd = String(args.command || '')
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

export function PackSummary({ pack }: { pack: Message[] }) {
  if (!pack.length) return <span>思考与工具</span>

  const hasFailed = pack.some(m => m.tool?.status === 'failed')
  const isRunning = pack.some(m => m.tool?.status === 'running')

  if (pack.length === 1) {
    const desc = describeTool(pack[0])
    return (
      <span className="pack-summary-row">
        <ToolLineIcon icon={desc.iconKey} size={14} className="pack-summary-icon" />
        <span className="pack-summary-title">{desc.fullTitle}</span>
        {desc.isFailed && <span className="activity-badge is-failed">失败</span>}
        {desc.isRunning && <span className="activity-badge is-running">执行中</span>}
      </span>
    )
  }

  const descriptions = pack.map(describeTool)
  const isAllCmd = descriptions.every(d => d.iconKey === 'terminal')
  const isAllThinking = descriptions.every(d => d.iconKey === 'thinking')

  if (isAllCmd) {
    return (
      <span className="pack-summary-row">
        <ToolLineIcon icon="terminal" size={14} className="pack-summary-icon" />
        <span>运行了 {pack.length} 个命令</span>
        {hasFailed && <span className="activity-badge is-failed">有失败项</span>}
        {isRunning && <span className="activity-badge is-running">运行中</span>}
      </span>
    )
  }

  if (isAllThinking) {
    return (
      <span className="pack-summary-row">
        <ToolLineIcon icon="thinking" size={14} className="pack-summary-icon" />
        <span>思考过程 · {pack.length} 项</span>
        {isRunning && <span className="activity-badge is-running">运行中</span>}
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
      <span className="pack-summary-count">(共 {pack.length} 项)</span>
      {hasFailed && <span className="activity-badge is-failed">有失败项</span>}
      {isRunning && <span className="activity-badge is-running">运行中</span>}
    </span>
  )
}
