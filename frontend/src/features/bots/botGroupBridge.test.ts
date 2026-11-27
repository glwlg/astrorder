import { describe, expect, it } from 'vitest'
import type { BotSummary } from '../../api/client'
import type { Agent } from '../../domain/types'
import {
  buildBotRolePrompt,
  createMemberSeedFromBot,
  sanitizeGroupAlias,
  uniqueBotAlias,
} from './botGroupBridge'

const agents: Record<string, Agent> = {
  codex: { id: 'codex', kind: 'codex', name: 'Codex', status: 'ready', capabilities: ['chat'], limitation: null },
  'agy-wsl': {
    id: 'agy-wsl',
    kind: 'antigravity-ssh',
    name: 'WSL · Antigravity',
    connection_id: 'wsl-dev',
    status: 'ready',
    capabilities: ['chat'],
    limitation: null,
  },
}

const sampleBot: BotSummary = {
  id: 'bot-1',
  slug: 'doc-writer',
  name: '公文写作助手',
  description: '擅长起草周报、立项方案与汇报材料',
  workspace: '~/Documents/office-docs',
  connection_id: 'wsl-dev',
  default_agent_id: 'agy-wsl',
  default_provider: 'antigravity',
  default_model: 'gemini-4-neon-high',
  default_effort: 'high',
  default_approval_mode: 'auto',
  created_at: '2026-11-27T00:00:00Z',
  updated_at: '2026-11-27T00:00:00Z',
  has_agents_md: true,
  agents_md_excerpt: '输出前先给结论再列论据',
  skills: [],
  session_count: 2,
  running_count: 0,
}

describe('botGroupBridge', () => {
  it('sanitizes and deduplicates aliases cleanly', () => {
    expect(sanitizeGroupAlias('  产品 @经理 ')).toBe('产品-经理')
    expect(sanitizeGroupAlias('all')).toBe('all-bot')
    expect(uniqueBotAlias('公文写作助手', ['公文写作助手'])).toBe('公文写作助手-2')
  })

  it('maps a Bot Studio bot into a CreateBotGroupDialog member draft preserving persona, workspace, model, and AGY guardrails', () => {
    const seed = createMemberSeedFromBot(sampleBot, agents, [], 'member-seed-1')
    expect(seed).toMatchObject({
      id: 'member-seed-1',
      sourceBotId: 'bot-1',
      agentId: 'agy-wsl',
      machineId: 'wsl-dev',
      name: '公文写作助手',
      alias: '公文写作助手',
      role: '擅长起草周报、立项方案与汇报材料；输出前先给结论再列论据',
      workspace: '~/Documents/office-docs',
      model: JSON.stringify(['antigravity', 'gemini-4-neon-high']),
      effort: '',
      approval: 'manual',
    })
    expect(buildBotRolePrompt({ description: '摘要', agents_md_excerpt: '包含摘要的详细说明' })).toBe(
      '包含摘要的详细说明',
    )
  })
})
