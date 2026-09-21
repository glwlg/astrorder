import { describe, expect, it } from 'vitest'
import { buildCommandMenuItems, filterAgentCommands, filterAgentMentions, formatAgentMention } from './AgentCommandMenu'

describe('agent composer menus', () => {
  it('shows primary command list on "/" and expands review options on "/review"', () => {
    const commands = [
      { name: 'compact', description: '压缩上下文', input_hint: null },
      { name: 'review', description: '代码审查', input_hint: null },
    ]
    const branches = ['master', 'origin/master', 'feat/login']

    // 1. 输入 "/" 时只展示一级常规命令，不抢先展开分支列表
    const rootMenu = buildCommandMenuItems(commands, branches, '/')
    expect(rootMenu.every(item => item.kind === 'command')).toBe(true)
    expect(rootMenu.map(item => item.kind === 'command' && item.command.name)).toEqual(['review', 'compact'])

    // 2. 输入 "/review" 时切入二级分支审查选择菜单
    const reviewMenu = buildCommandMenuItems(commands, branches, '/review')
    expect(reviewMenu[0]).toEqual({ kind: 'review_uncommitted', label: '审查未提交的更改' })
    expect(reviewMenu[1]).toEqual({ kind: 'header', label: '对照基础分支审查' })
    expect(reviewMenu.slice(2)).toEqual([
      { kind: 'review_branch', branch: 'master', label: 'master' },
      { kind: 'review_branch', branch: 'origin/master', label: 'origin/master' },
      { kind: 'review_branch', branch: 'feat/login', label: 'feat/login' },
    ])

    // 3. 带分支过滤的 "/review feat"
    const filterMenu = buildCommandMenuItems(commands, branches, '/review feat')
    expect(filterMenu.filter(item => item.kind === 'review_branch')).toEqual([
      { kind: 'review_branch', branch: 'feat/login', label: 'feat/login' },
    ])
  })

  it('filters slash commands and skill mentions', () => {
    expect(filterAgentCommands([
      { name: 'compact', description: '压缩上下文', input_hint: null },
      { name: 'review', description: '审查变更', input_hint: null },
    ], '/rev').map(item => item.name)).toEqual(['review'])
    expect(filterAgentMentions([
      { name: 'openai-docs', description: 'OpenAI 文档', kind: 'skill', path: '/skills/openai-docs' },
      { name: 'src/App.tsx', description: '/repo/src/App.tsx', kind: 'file', path: '/repo/src/App.tsx' },
    ], '请用 @open').map(item => item.name)).toEqual(['openai-docs'])
  })

  it('formats file mentions as markdown links and keeps skills as native mentions', () => {
    expect(formatAgentMention({ name: 'backend/src/astrorder/main.py', description: '', kind: 'file', path: 'P:/workspace/backend/src/astrorder/main.py' }))
      .toBe('[main.py](backend/src/astrorder/main.py) ')
    expect(formatAgentMention({ name: 'browser', description: '', kind: 'skill', path: '/skills/browser' }))
      .toBe('@browser ')
  })

  it('offers the Astrorder browser command for Chinese and English searches', () => {
    for (const text of ['@浏览', '@browser']) {
      const item = filterAgentMentions([], text)[0]
      expect(item.path).toBe('system:browser')
      expect(formatAgentMention(item)).toBe('@浏览器 ')
    }
  })
})
