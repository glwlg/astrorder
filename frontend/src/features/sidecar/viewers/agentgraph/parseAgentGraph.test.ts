import { describe, expect, it } from 'vitest'
import type { SessionCheckpoint } from '../../../../api/client'
import type { Message, Task } from '../../../../domain/types'
import { buildExecutionHubData, parseNodesFromSession } from './parseAgentGraph'

function message(partial: Partial<Message> & Pick<Message, 'id' | 'role'>): Message {
  return {
    session_id: 's',
    agent_id: 'a',
    kind: 'message',
    text: '',
    attachments: [],
    created_at: '2026-01-01T00:00:00Z',
    command_id: null,
    tool: null,
    ...partial,
  }
}

describe('parseNodesFromSession', () => {
  it('maps a user turn, thinking, tools and the assistant reply into a linear decision flow', () => {
    const nodes = parseNodesFromSession([
      message({ id: 'u1', role: 'user', text: '修一下构建' }),
      message({ id: 't1', role: 'assistant', kind: 'thinking', text: '先看测试失败原因' }),
      message({
        id: 'c1',
        role: 'tool',
        kind: 'tool',
        text: '',
        tool: { name: 'commandExecution', arguments: { command: 'npm test' }, status: 'failed' },
      }),
      message({ id: 'a1', role: 'assistant', text: '测试挂了，接着改' }),
    ], [])

    expect(nodes.map((node) => node.kind)).toEqual(['user_prompt', 'thinking', 'tool_call', 'completed'])
    expect(nodes[2]?.status).toBe('failed')
    expect(nodes[2]?.title).toContain('npm test')
  })

  it('does not treat system chatter as thinking, and surfaces subagent tasks', () => {
    const task: Task = {
      id: 'sub-1',
      session_id: 's',
      agent_id: 'a',
      kind: 'subagent',
      title: '审查 diff',
      status: 'running',
      progress: null,
      command: null,
      logs: [],
      target_id: 'sub-1',
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:01:00Z',
    }
    const nodes = parseNodesFromSession([
      message({ id: 'sys', role: 'system', text: 'runtime ready' }),
    ], [task])

    expect(nodes.some((node) => node.kind === 'thinking')).toBe(false)
    expect(nodes.some((node) => node.kind === 'subagent' && node.status === 'running')).toBe(true)
  })

  it('returns a ready placeholder when the session has no transcript yet', () => {
    const nodes = parseNodesFromSession([], [])
    expect(nodes).toHaveLength(1)
    expect(nodes[0]?.kind).toBe('planning')
    expect(nodes[0]?.status).toBe('pending')
  })
})

describe('buildExecutionHubData', () => {
  it('aggregates turns, deduplicates repeated file edits, detects failure recovery, and binds checkpoints', () => {
    const msgs: Message[] = [
      message({ id: 'u1', role: 'user', text: '修复插件循环依赖并跑完测试', command_id: 'cmd-1', created_at: '2026-01-01T10:00:00Z' }),
      message({
        id: 'r1',
        role: 'tool',
        kind: 'tool',
        tool: { name: 'read_file', arguments: { Path: '/repo/internal/toolchain/detect.go' }, status: 'completed' },
        created_at: '2026-01-01T10:00:05Z',
      }),
      message({
        id: 'e1',
        role: 'tool',
        kind: 'tool',
        text: 'Made 1 replacement in /repo/cmd/app/main.go starting at line 38 (+10/-2 lines).',
        tool: { name: 'edit_file', arguments: { Path: '/repo/cmd/app/main.go' }, status: 'completed' },
        created_at: '2026-01-01T10:00:10Z',
      }),
      message({
        id: 'e2',
        role: 'tool',
        kind: 'tool',
        text: 'Made 1 replacement in /repo/cmd/app/main.go starting at line 90 (+5/-1 lines).',
        tool: { name: 'edit_file', arguments: { Path: '/repo/cmd/app/main.go' }, status: 'completed' },
        created_at: '2026-01-01T10:00:12Z',
      }),
      message({
        id: 'v1',
        role: 'tool',
        kind: 'tool',
        text: 'Command exited with code 1.\n# astrorder/internal/toolchain: import cycle not allowed',
        tool: { name: 'run_command', arguments: { CommandLine: 'go test ./internal/toolchain/...' }, status: 'completed' },
        created_at: '2026-01-01T10:00:15Z',
      }),
      message({
        id: 'th1',
        role: 'assistant',
        kind: 'thinking',
        text: '将共享偏好辅助函数抽离到 internal/store/plugin_settings.go 以消除循环依赖',
        created_at: '2026-01-01T10:00:20Z',
      }),
      message({
        id: 'w1',
        role: 'tool',
        kind: 'tool',
        text: 'Created file.',
        tool: { name: 'write_file', arguments: { Path: '/repo/internal/store/plugin_settings.go', Content: 'package store\nvar X = 1\n' }, status: 'completed' },
        created_at: '2026-01-01T10:00:25Z',
      }),
      message({
        id: 'v2',
        role: 'tool',
        kind: 'tool',
        text: 'Command exited with code 0.\nok  astrorder/internal/toolchain 0.4s',
        tool: { name: 'run_command', arguments: { CommandLine: 'go test ./internal/toolchain/...' }, status: 'completed' },
        created_at: '2026-01-01T10:00:30Z',
        completed_at: '2026-01-01T10:00:32Z',
      }),
      message({
        id: 'a1',
        role: 'assistant',
        text: '- 已将公共函数提取至 internal/store/plugin_settings.go\n- 全量回归测试已通过',
        created_at: '2026-01-01T10:00:35Z',
      }),
    ]

    const checkpoints: SessionCheckpoint[] = [
      {
        id: 'cp-1',
        session_id: 's',
        agent_id: 'a',
        workspace: '/repo',
        repo_root: '/repo',
        branch: 'master',
        head_sha: 'aaaaaaa',
        turn_number: 1,
        kind: 'auto_after',
        label: '修复循环依赖',
        tree_sha: '7f3a8bc123456',
        commit_sha: 'ccccccc',
        ref_name: 'refs/astrorder/cp/1',
        prev_tree_sha: '111222333',
        command_id: 'cmd-1',
        files_changed: 2,
        insertions: 17,
        deletions: 3,
        files: [],
        created_at: '2026-01-01T10:00:36Z',
      },
    ]

    const hub = buildExecutionHubData(msgs, [], checkpoints, '/repo')

    expect(hub.turns).toHaveLength(1)
    const turn = hub.turns[0]
    expect(turn.outcome).toBe('recovered')
    expect(turn.phases.research).toBe(1)
    expect(turn.phases.editFiles).toBe(2)
    expect(turn.phases.verifyPassed).toBe(1)
    expect(turn.phases.verifyFailed).toBe(1)
    expect(turn.diagnostics).toHaveLength(1)
    expect(turn.diagnostics[0].resolved).toBe(true)
    expect(turn.diagnostics[0].headline).toContain('import cycle not allowed')
    expect(turn.diagnostics[0].resolutionNote).toContain('internal/store/plugin_settings.go')
    expect(turn.checkpoint?.id).toBe('cp-1')

    // Deduplicated global files: main.go edited twice, plugin_settings.go created, detect.go read-only
    expect(hub.summary.createdFileCount).toBe(1)
    expect(hub.summary.modifiedFileCount).toBe(1)
    expect(hub.summary.readOnlyFileCount).toBe(1)
    const mainFile = hub.files.find((f) => f.fileName === 'main.go')
    expect(mainFile?.editCount).toBe(2)
    expect(mainFile?.addedLines).toBe(15)
    expect(mainFile?.removedLines).toBe(3)
    expect(mainFile?.displayPath).toBe('cmd/app/main.go')

    // Verification recovery status propagated to audit list
    expect(hub.verifications).toHaveLength(2)
    expect(hub.verifications[0].status).toBe('failed')
    expect(hub.verifications[0].recovered).toBe(true)
    expect(hub.verifications[1].status).toBe('passed')
  })
})
