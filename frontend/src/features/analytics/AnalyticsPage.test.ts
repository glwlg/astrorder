import { describe, expect, it } from 'vitest'
import { formatTokens, getModelColor } from './AnalyticsPage'

describe('formatTokens', () => {
  it('uses Chinese magnitude units', () => {
    expect(formatTokens(10_270_000_000)).toBe('102.7 亿')
    expect(formatTokens(12_630_000)).toBe('1,263 万')
    expect(formatTokens(250_000)).toBe('25 万')
    expect(formatTokens(3_200)).toBe('3,200')
  })

  it('assigns recognizable theme colors to known models', () => {
    expect(getModelColor('gpt-5.6-sol', 'openai')).toBe('#ef4444')
    expect(getModelColor('gemini-3.8-flash', 'google-antigravity')).toBe('#2563eb')
    expect(getModelColor('gpt-5.6-luna', 'openai')).toBe('#10b981')
    expect(getModelColor('deepseek-v4-flash', 'deepseek')).toBe('#3b82f6')
    expect(getModelColor('unknown', '')).toBe('#14b8a6')
  })
})
