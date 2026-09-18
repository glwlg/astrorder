import { describe, expect, it } from 'vitest'
import { describeTool, formatPackSummary, unwrapCommand } from './toolPresentation'
import type { Message } from '../../domain/types'

function makeMessage(override: Partial<Message>): Message {
  return {
    id: 'test-1',
    session_id: 's-1',
    agent_id: 'a-1',
    role: 'tool',
    kind: 'tool',
    text: '',
    attachments: [],
    created_at: '2026-09-10T00:00:00Z',
    command_id: null,
    tool: null,
    ...override,
  }
}

describe('unwrapCommand', () => {
  it('unwraps zsh and bash -c wrappers', () => {
   expect(unwrapCommand('/usr/bin/zsh -lc "git diff 2e4cd54..HEAD"')).toBe('git diff 2e4cd54..HEAD')
   expect(unwrapCommand("/bin/bash -c 'pytest -q'")).toBe('pytest -q')
   expect(unwrapCommand('npm test')).toBe('npm test')
    expect(unwrapCommand('"C:\\path\\pwsh.exe" -Command "python -c \"print(1)\""')).toBe('python -c "print(1)"')
    expect(unwrapCommand('powershell -NoProfile -ExecutionPolicy Bypass -Command "cd desktop; npm run check"')).toBe('cd desktop; npm run check')
    expect(unwrapCommand('cmd.exe /c "dir /b"')).toBe('dir /b')
  })
})

describe('describeTool', () => {
  it('describes commandExecution with clean unwrapped command', () => {
    const msg = makeMessage({
      tool: {
        name: 'commandExecution',
        arguments: { command: "/usr/bin/zsh -lc 'git log --oneline -n 10 -- CameraView.vue'" },
        status: 'completed',
      },
    })
    const desc = describeTool(msg)
    expect(desc.iconKey).toBe('terminal')
    expect(desc.action).toBe('Git')
    expect(desc.fullTitle).toContain('git log --oneline -n 10')
  })

  it('describes commandExecution failure', () => {
    const msg = makeMessage({
      tool: {
        name: 'commandExecution',
        arguments: { command: 'docker logs ikaros' },
        status: 'failed',
      },
    })
    const desc = describeTool(msg)
    expect(desc.isFailed).toBe(true)
    expect(desc.action).toBe('服务')
  })

  it('describes fileChange with modified files', () => {
    const msg = makeMessage({
      tool: {
        name: 'fileChange',
        arguments: { changes: [{ path: '/home/luwei/workspace/ikaros/src/views/CameraView.vue' }] },
        status: 'completed',
      },
    })
    const desc = describeTool(msg)
    expect(desc.iconKey).toBe('edit')
    expect(desc.action).toBe('编辑')
    expect(desc.target).toBe('CameraView.vue')
  })

  it('describes thinking message with first line', () => {
    const msg = makeMessage({
      kind: 'thinking',
      role: 'assistant',
      text: '**Planning diagnosis for yt-dlp upgrade**\nChecking dependencies',
    })
    const desc = describeTool(msg)
    expect(desc.iconKey).toBe('thinking')
    expect(desc.target).toBe('Planning diagnosis for yt-dlp upgrade')
  })
})

describe('formatPackSummary', () => {
  it('formats single command tool as direct command summary', () => {
    const msg = makeMessage({
      tool: { name: 'commandExecution', arguments: { command: 'pytest -q' }, status: 'completed' },
    })
    expect(formatPackSummary([msg])).toBe('pytest -q')
  })

  it('formats multiple commands as command count', () => {
    const m1 = makeMessage({ id: '1', tool: { name: 'commandExecution', arguments: { command: 'git diff' } } })
    const m2 = makeMessage({ id: '2', tool: { name: 'commandExecution', arguments: { command: 'git status' } } })
    expect(formatPackSummary([m1, m2])).toBe('运行了 2 个命令')
  })

  it('formats mixed tool calls with action badges', () => {
    const m1 = makeMessage({ id: '1', tool: { name: 'fileChange', arguments: { changes: [{ path: 'a.py' }] } } })
    const m2 = makeMessage({ id: '2', tool: { name: 'read_file', arguments: { path: 'b.py' } } })
    const m3 = makeMessage({ id: '3', tool: { name: 'commandExecution', arguments: { command: 'pytest' } } })
    expect(formatPackSummary([m1, m2, m3])).toBe('编辑 · 读取 · 测试 (共 3 项)')
  })
})
