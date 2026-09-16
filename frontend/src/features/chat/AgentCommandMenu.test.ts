import { describe, expect, it } from 'vitest'
import { filterAgentCommands, filterAgentMentions, formatAgentMention } from './AgentCommandMenu'

describe('agent composer menus', () => {
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
})
