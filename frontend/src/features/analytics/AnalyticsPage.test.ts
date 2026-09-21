import { describe, expect, it } from 'vitest'
import { formatTokens } from './AnalyticsPage'

describe('formatTokens', () => {
  it('uses Chinese magnitude units', () => {
    expect(formatTokens(10_270_000_000)).toBe('102.7 亿')
    expect(formatTokens(12_630_000)).toBe('1,263 万')
    expect(formatTokens(250_000)).toBe('25 万')
    expect(formatTokens(3_200)).toBe('3,200')
  })
})
