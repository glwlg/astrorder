import { describe, expect, it } from 'vitest'
import {
  availableEffortsForModel,
  findModelGroup,
  groupModelChoices,
  parseAntigravityModelId,
  resolveGroupVariant,
} from './antigravityModels'

const SAMPLE_AGY_CATALOG = [
  { provider: 'antigravity', model: 'gemini-4-neon-high', label: 'Gemini 4 Neon (High)' },
  { provider: 'antigravity', model: 'gemini-4-neon-xhigh', label: 'Gemini 4 Neon (XHigh)' },
  { provider: 'antigravity', model: 'gemini-4-neon-medium', label: 'Gemini 4 Neon (Medium)' },
  { provider: 'antigravity', model: 'gemini-4-neon-low', label: 'Gemini 4 Neon (Low)' },
  { provider: 'antigravity', model: 'gemini-4-carbon-medium', label: 'Gemini 4 Carbon (Medium)' },
  { provider: 'antigravity', model: 'gemini-4-carbon-high', label: 'Gemini 4 Carbon (High)' },
  { provider: 'antigravity', model: 'gemini-4-carbon-low', label: 'Gemini 4 Carbon (Low)' },
  { provider: 'antigravity', model: 'gemini-pro-orchestrator-neon', label: 'Orchestrator (Neon)' },
  { provider: 'antigravity', model: 'gemini-pro-orchestrator', label: 'Orchestrator (Carbon)' },
  { provider: 'antigravity', model: 'claude-opus-5-5-low', label: 'Claude Opus 5.5 (Low)' },
  { provider: 'antigravity', model: 'claude-opus-5-5-medium', label: 'Claude Opus 5.5 (Medium)' },
  { provider: 'antigravity', model: 'claude-opus-5-5-high', label: 'Claude Opus 5.5 (High)' },
  { provider: 'antigravity', model: 'gpt-oss-120b-medium', label: 'GPT-OSS 120B (Medium)' },
]

describe('antigravityModels', () => {
  it('parses Antigravity effort suffixes without touching non-effort suffixes', () => {
    expect(parseAntigravityModelId('gemini-4-neon-xhigh')).toEqual({ baseModel: 'gemini-4-neon', effort: 'xhigh' })
    expect(parseAntigravityModelId('claude-opus-5-5-low')).toEqual({ baseModel: 'claude-opus-5-5', effort: 'low' })
    expect(parseAntigravityModelId('gemini-pro-orchestrator-neon')).toEqual({
      baseModel: 'gemini-pro-orchestrator-neon',
      effort: null,
    })
  })

  it('groups multi-effort Antigravity variants by base model in original order while keeping single/non-effort models intact', () => {
    const groups = groupModelChoices(SAMPLE_AGY_CATALOG)
    expect(groups.map((g) => ({ base: g.baseModel, label: g.label, grouped: g.isGrouped, efforts: g.efforts.map((e) => e.effort) }))).toEqual([
      { base: 'gemini-4-neon', label: 'Gemini 4 Neon', grouped: true, efforts: ['low', 'medium', 'high', 'xhigh'] },
      { base: 'gemini-4-carbon', label: 'Gemini 4 Carbon', grouped: true, efforts: ['low', 'medium', 'high'] },
      { base: 'gemini-pro-orchestrator-neon', label: 'Orchestrator (Neon)', grouped: false, efforts: [] },
      { base: 'gemini-pro-orchestrator', label: 'Orchestrator (Carbon)', grouped: false, efforts: [] },
      { base: 'claude-opus-5-5', label: 'Claude Opus 5.5', grouped: true, efforts: ['low', 'medium', 'high'] },
      { base: 'gpt-oss-120b', label: 'GPT-OSS 120B (Medium)', grouped: false, efforts: ['medium'] },
    ])
  })

  it('preserves exact effort when supported and falls back to nearest effort when clamping', () => {
    const groups = groupModelChoices(SAMPLE_AGY_CATALOG)
    const neon = findModelGroup(groups, 'antigravity', 'gemini-4-neon-xhigh')!
    const carbon = findModelGroup(groups, 'antigravity', 'gemini-4-carbon-low')!
    const orchestrator = findModelGroup(groups, 'antigravity', 'gemini-pro-orchestrator')!

    expect(neon.label).toBe('Gemini 4 Neon')
    expect(resolveGroupVariant(carbon, 'xhigh').model).toBe('gemini-4-carbon-high')
    expect(resolveGroupVariant(carbon, 'low').model).toBe('gemini-4-carbon-low')
    expect(resolveGroupVariant(neon, null).model).toBe('gemini-4-neon-high')
    expect(resolveGroupVariant(orchestrator, 'xhigh').model).toBe('gemini-pro-orchestrator')
    expect(availableEffortsForModel(neon).map((e) => e.value)).toEqual(['low', 'medium', 'high', 'xhigh'])
    expect(availableEffortsForModel(carbon).map((e) => e.value)).toEqual(['low', 'medium', 'high'])
  })

  it('never groups non-antigravity providers', () => {
    const items = [
      { provider: 'openai', model: 'gpt-5-low', label: 'GPT-5 (Low)' },
      { provider: 'openai', model: 'gpt-5-high', label: 'GPT-5 (High)' },
    ]
    const groups = groupModelChoices(items)
    expect(groups).toHaveLength(2)
    expect(groups.every((g) => !g.isGrouped)).toBe(true)
  })
})
