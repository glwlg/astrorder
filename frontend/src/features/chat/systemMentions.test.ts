import { describe, expect, it } from 'vitest'
import { expandSystemMentions } from './systemMentions'

describe('system mentions', () => {
  it('forces Chinese and English browser mentions through Astrorder browser_run', () => {
    for (const mention of ['@浏览器', '@browser']) {
      const result = expandSystemMentions(`用 ${mention} 查询天气`)
      expect(result).toContain('mcp:astrorder.browser_run')
      expect(result).toContain('mcp:astrorder.browser_cdp')
      expect(result).toContain('用 【星序 · 浏览器自动执行指令】 查询天气')
      expect(result).not.toContain(mention)
    }
  })

  it('binds browser tasks to the current session', () => {
    const result = expandSystemMentions('@browser 查询', 'codex::session-1')
    expect(result).toContain('browser_run 和 browser_cdp')
    expect(result).toContain('session_key 必须传入“codex::session-1”')
  })

  it('forces Chinese and English blackboard mentions with session scope', () => {
    for (const mention of ['@黑板', '@blackboard']) {
      const result = expandSystemMentions(`请把架构方案写入 ${mention}`, 'local-codex::sess-123')
      expect(result).toContain('mcp:astrorder.blackboard_set')
      expect(result).toContain('mcp:astrorder.blackboard_get')
      expect(result).toContain('session_key 参数必须传入“local-codex::sess-123”')
      expect(result).toContain('请把架构方案写入 【星序 · 作战黑板同步指令】')
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
