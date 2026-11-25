import { describe, expect, it } from 'vitest'
import { messageForkModeForAgent } from './handoff'

describe('messageForkModeForAgent', () => {
  it('routes Hermes to native message fork, Antigravity to context branch, and hides unsupported agents', () => {
    expect(messageForkModeForAgent('hermes')).toBe('native')
    expect(messageForkModeForAgent('antigravity')).toBe('context_branch')
    expect(messageForkModeForAgent('antigravity-ssh')).toBe('context_branch')
    expect(messageForkModeForAgent('codex')).toBe('none')
    expect(messageForkModeForAgent('grok')).toBe('none')
    expect(messageForkModeForAgent(undefined)).toBe('none')
  })
})
