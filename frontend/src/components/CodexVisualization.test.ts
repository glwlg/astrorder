import { describe, expect, it } from 'vitest'
import { extractVisualizations } from './CodexVisualization'

describe('Codex visualization directives', () => {
  it('preserves prose and extracts native HTML paths', () => {
    const result = extractVisualizations('设计稿\n\uE200visualize\uE202{"path":"/home/user/demo.html"}\uE201')
    expect(result).toEqual({ text: '设计稿\n', paths: ['/home/user/demo.html'] })
  })
  it('does not consume malformed or external directives', () => {
    for (const payload of ['broken', '{"path":"https://example.com/a.html"}', '{"path":"//example.com/a.html"}']) {
      const text = `\uE200visualize\uE202${payload}\uE201`
      expect(extractVisualizations(text)).toEqual({ text, paths: [] })
    }
  })
})
