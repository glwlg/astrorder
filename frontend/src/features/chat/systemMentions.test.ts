import { describe, expect, it } from 'vitest'
import { expandSystemMentions } from './systemMentions'

describe('system mentions', () => {
  it('forces Chinese and English browser mentions through Astrorder browser_run', () => {
    for (const mention of ['@浏览器', '@browser']) {
      const result = expandSystemMentions(`用 ${mention} 查询天气`)
      expect(result).toContain('mcp:astrorder.browser_run')
      expect(result).toContain('用 【星序 · 浏览器自动执行指令】 查询天气')
      expect(result).not.toContain(mention)
    }
  })

  it('keeps swarm and browser directives when both are requested', () => {
    const result = expandSystemMentions('@群星 使用 @browser 收集资料')
    expect(result).toContain('群星多 Agent 协同作战指令')
    expect(result).toContain('浏览器自动执行指令')
    expect(result).toContain('【星序 · 群星多 Agent 协同作战指令】 使用 【星序 · 浏览器自动执行指令】 收集资料')
  })

  it('does not change ordinary messages', () => {
    expect(expandSystemMentions('普通消息')).toBe('普通消息')
  })
})
