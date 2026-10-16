const zhNumber = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 })

export function formatTokens(tokens: number = 0): string {
  if (tokens >= 100_000_000) return `${zhNumber.format(tokens / 100_000_000)} 亿`
  if (tokens >= 10_000) return `${zhNumber.format(tokens / 10_000)} 万`
  return zhNumber.format(tokens)
}

export function getModelColor(model: string = '', provider: string = ''): string {
  const m = model.toLowerCase()
  if (m.includes('gpt-5.6-sol') || m === 'sol') return '#ef4444'
  if (m.includes('gpt-5.6-luna') || m === 'luna') return '#10b981'
  if (m.includes('deepseek')) return '#3b82f6'
  if (m.includes('gemini')) return '#2563eb'
  if (m.includes('guolian')) return '#8b5cf6'
  if (m.includes('terra')) return '#38bdf8'
  if (m.includes('grok')) return '#06b6d4'
  if (m.includes('claude')) return '#f97316'
  if (m.includes('astra')) return '#a855f7'
  if (m === 'unknown') return '#14b8a6'
  const key = `${provider}/${model}`
  let hash = 0
  for (let i = 0; i < key.length; i++) hash = ((hash * 31) + key.charCodeAt(i)) >>> 0
  return `hsl(${hash % 360} 60% 50%)`
}

export function statisticsPageURL(config: { statistics_url?: string; management_url: string }): string {
  const value = (config.statistics_url || config.management_url).trim()
  try {
    const url = new URL(value)
    return ['http:', 'https:'].includes(url.protocol) && !url.username && !/^https?:\/\/[^/?#]*@/i.test(value) ? value : ''
  } catch { return '' }
}
