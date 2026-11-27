import { describe, expect, it } from 'vitest'
import type { GitWorktreeEntry, Message } from '../../domain/types'
import { detectActiveShadowWorktree } from './useSessionActiveWorktree'

function makeWorktree(path: string, branch: string, overrides: Partial<GitWorktreeEntry> = {}): GitWorktreeEntry {
  return {
    path,
    head: '3b115e3abcdef',
    branch,
    detached: false,
    bare: false,
    is_main: false,
    is_current: false,
    locked: false,
    prunable: false,
    status_loaded: false,
    dirty: false,
    changed_files: 0,
    ahead: 0,
    behind: 0,
    ...overrides,
  }
}

function makeMessage(id: string, text: string, tool: Record<string, unknown> | null = null): Message {
  return {
    id,
    session_id: 's1',
    agent_id: 'a1',
    role: 'assistant',
    kind: tool ? 'tool' : 'message',
    text,
    attachments: [],
    created_at: '2026-11-27T11:00:00Z',
    command_id: null,
    tool,
  }
}

describe('detectActiveShadowWorktree', () => {
  const mainWS = '/home/luwei/workspace/OpsCore'
  const wtMain = makeWorktree(mainWS, 'main', { is_main: true, is_current: true })
  const wtWave4 = makeWorktree('/tmp/opscore-wave4', 'feat/wave4')
  const wtWave5 = makeWorktree('/tmp/opscore-wave5', 'feat/wave5')
  const wtPrunable = makeWorktree('/tmp/opscore-gone', 'feat/gone', { prunable: true })

  it('detects linked worktree mentioned in assistant markdown text', () => {
    const messages = [
      makeMessage('m1', '回复我后我将立即在 `/tmp/opscore-wave4` 拉取最新 CI 回写提交，执行 Commit 2'),
    ]
    const found = detectActiveShadowWorktree(messages, [wtMain, wtWave4, wtPrunable], mainWS)
    expect(found?.path).toBe('/tmp/opscore-wave4')
    expect(found?.branch).toBe('feat/wave4')
  })

  it('detects linked worktree inside tool call payloads', () => {
    const messages = [
      makeMessage('m1', '', { command: 'cd /tmp/opscore-wave4 && go test ./...' }),
    ]
    const found = detectActiveShadowWorktree(messages, [wtMain, wtWave4], mainWS)
    expect(found?.path).toBe('/tmp/opscore-wave4')
  })

  it('respects path boundaries and does not confuse prefix siblings', () => {
    const messages = [
      makeMessage('m1', '我们在 /tmp/opscore-wave40 做了实验，但不在 wave4'),
    ]
    const found = detectActiveShadowWorktree(messages, [wtMain, wtWave4], mainWS)
    expect(found).toBeNull()
  })

  it('matches nested file paths inside the linked worktree', () => {
    const messages = [
      makeMessage('m1', '已写入 /tmp/opscore-wave4/backend/migrations/biz-system/003_audit.sql'),
    ]
    const found = detectActiveShadowWorktree(messages, [wtMain, wtWave4], mainWS)
    expect(found?.path).toBe('/tmp/opscore-wave4')
  })

  it('prefers the most recently mentioned registered linked worktree', () => {
    const messages = [
      makeMessage('m1', '早期在 /tmp/opscore-wave4 工作'),
      makeMessage('m2', '现在已经切到 /tmp/opscore-wave5 继续推进'),
    ]
    const found = detectActiveShadowWorktree(messages, [wtMain, wtWave4, wtWave5], mainWS)
    expect(found?.path).toBe('/tmp/opscore-wave5')
  })

  it('ignores prunable/removed worktrees and the main workspace itself', () => {
    const messages = [
      makeMessage('m1', '工作区在 /home/luwei/workspace/OpsCore 和已删除的 /tmp/opscore-gone'),
    ]
    const found = detectActiveShadowWorktree(messages, [wtMain, wtPrunable], mainWS)
    expect(found).toBeNull()
  })

  it('handles Windows backslash paths case-insensitively', () => {
    const winMain = 'D:\\repo\\OpsCore'
    const winWt = makeWorktree('D:\\worktrees\\OpsCore-Wave4', 'feat/win-wave4')
    const messages = [
      makeMessage('m1', '已生成 d:\\worktrees\\opscore-wave4\\backend\\003.sql'),
    ]
    const found = detectActiveShadowWorktree(messages, [makeWorktree(winMain, 'main', { is_main: true }), winWt], winMain)
    expect(found?.path).toBe('D:\\worktrees\\OpsCore-Wave4')
  })
})
