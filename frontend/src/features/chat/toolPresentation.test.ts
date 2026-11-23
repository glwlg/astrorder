import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { activityDuration, activityState, describeTool, fileChangeDiffs, formatCommandRowDisplay, formatPackSummary, PackSummary, unwrapCommand } from './toolPresentation'
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
        arguments: { command: 'docker logs example-app' },
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
        arguments: { changes: [{ path: '/home/fixture/workspace/example-app/src/views/CameraView.vue' }] },
        status: 'completed',
      },
    })
    const desc = describeTool(msg)
    expect(desc.iconKey).toBe('edit')
    expect(desc.action).toBe('编辑')
    expect(desc.target).toBe('CameraView.vue')
  })

  it('extracts renderable diffs from file changes', () => {
    const msg = makeMessage({
      tool: {
        name: 'fileChange',
        arguments: {
          changes: [
            { path: '/repo/src/App.tsx', diff: '@@ -1 +1 @@\n-old\n+new' },
            { path: '/repo/src/empty.ts', diff: '' },
          ],
        },
      },
    })
    expect(fileChangeDiffs(msg)).toEqual([
      { file: 'App.tsx', path: '/repo/src/App.tsx', diff: '@@ -1 +1 @@\n-old\n+new' },
    ])
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

describe('activity execution facts', () => {
  it('keeps unknown tool state distinct from completed execution', () => {
    expect(activityState(makeMessage({ tool: { name: 'read_file' } }))).toBe('recorded')
    expect(activityState(makeMessage({ tool: { name: 'read_file', status: 'completed' } }))).toBe('completed')
    expect(activityState(makeMessage({ completed_at: '2026-09-10T00:00:41Z' }))).toBe('completed')
  })

  it('keeps failures and running state even when completion metadata is present', () => {
    const completed_at = '2026-09-10T00:00:41Z'
    expect(activityState(makeMessage({ completed_at, tool: { name: 'commandExecution', status: 'failed' } }))).toBe('failed')
    expect(activityState(makeMessage({ completed_at, tool: { name: 'commandExecution', status: 'running' } }))).toBe('running')
  })

  it('only shows durations from valid native timestamps', () => {
    expect(activityDuration(makeMessage({}))).toBeNull()
    expect(activityDuration(makeMessage({ completed_at: 'invalid' }))).toBeNull()
    expect(activityDuration(makeMessage({ created_at: 'invalid', completed_at: '2026-09-10T00:00:41Z' }))).toBeNull()
    expect(activityDuration(makeMessage({ completed_at: '2026-09-09T23:59:59Z' }))).toBeNull()
    expect(activityDuration(makeMessage({ completed_at: '2026-09-10T00:02:17Z' }))).toBe('2 分 17 秒')
  })
})

describe('antigravity tool normalization fallback', () => {
  it('describes Antigravity run_command, read_file, and edit_file with concrete targets and error state', () => {
    const cmdDesc = describeTool(makeMessage({
      tool: {
        name: 'run_command',
        status: 'error',
        input: JSON.stringify({ CommandLine: 'git status -sb', toolSummary: 'Check repo' }),
      },
    }))
    expect(cmdDesc.iconKey).toBe('terminal')
    expect(cmdDesc.action).toBe('Git')
    expect(cmdDesc.target).toBe('git status -sb')
    expect(cmdDesc.isFailed).toBe(true)

    const readDesc = describeTool(makeMessage({
      tool: {
        name: 'read_file',
        status: 'completed',
        arguments: { Path: '/repo/internal/adapter.go', StartLine: 405, EndLine: 630 },
      },
    }))
    expect(readDesc.iconKey).toBe('read')
    expect(readDesc.action).toBe('读取')
    expect(readDesc.target).toBe('adapter.go:405-630')

    const editMsg = makeMessage({
      tool: {
        name: 'edit_file',
        status: 'completed',
        arguments: { Path: '/repo/main.go', Target: 'oldCall()', Replacement: 'newCall()' },
      },
    })
    const editDesc = describeTool(editMsg)
    expect(editDesc.iconKey).toBe('edit')
    expect(editDesc.target).toBe('main.go')
    expect(fileChangeDiffs(editMsg)).toEqual([
      { file: 'main.go', path: '/repo/main.go', diff: '--- a/main.go\n+++ b/main.go\n@@\n-oldCall()\n+newCall()' },
    ])
  })

  it('renders image read_file, clean thinking headline, noisy command summary, and excludes thinking from running tool progress', () => {
    const imgDesc = describeTool(makeMessage({
      tool: {
        name: 'read_file',
        status: 'completed',
        arguments: { Path: '/tmp/scratch/agent-detail-install-light.png', path: '/tmp/scratch/agent-detail-install-light.png' },
      },
    }))
    expect(imgDesc.iconKey).toBe('image')
    expect(imgDesc.action).toBe('查看图片')
    expect(imgDesc.target).toBe('agent-detail-install-light.png')

    const thinkDesc = describeTool(makeMessage({
      kind: 'thinking',
      role: 'assistant',
      text: 'Initial hypothesis centered on `detail-pending` metrics in [MonitorPage](file:///app/MonitorPage.tsx).',
    }))
    expect(thinkDesc.target).toBe('Initial hypothesis centered on detail-pending metrics in MonitorPage.')

    const cmdDesc = describeTool(makeMessage({
      tool: {
        name: 'run_command',
        status: 'completed',
        arguments: {
          CommandLine: 'XDG_CONFIG_HOME=/tmp/xdg-cfg XDG_CACHE_HOME=/tmp/xdg-cache node /home/user/.gemini/verify_agent_install_ui.mjs',
          toolSummary: 'Verify agent install UI',
        },
      },
    }))
    const rowDisplay = formatCommandRowDisplay(cmdDesc, 'XDG_CONFIG_HOME=/tmp/xdg-cfg XDG_CACHE_HOME=/tmp/xdg-cache node /home/user/.gemini/verify_agent_install_ui.mjs')
    expect(rowDisplay.isHumanSummary).toBe(true)
    expect(rowDisplay.text).toBe('Verify agent install UI')

    const swarmDesc = describeTool(makeMessage({
      tool: { name: 'swarm', status: 'completed', arguments: { action: 'add', name: 'reviewer' } },
    }))
    expect(swarmDesc.action).toBe('Swarm')
    expect(swarmDesc.target).toBe('add reviewer')

    // Running pack progress counts executable tool steps instead of showing 2 / 4 because of thinking steps
    render(
      PackSummary({
        isRunning: true,
        pack: [
          makeMessage({ id: 't1', kind: 'thinking', role: 'assistant', text: 'Step 1 thought' }),
          makeMessage({ id: 'c1', tool: { name: 'read_file', status: 'completed', arguments: { Path: '/a.ts' } } }),
          makeMessage({ id: 't2', kind: 'thinking', role: 'assistant', text: 'Step 2 thought' }),
          makeMessage({ id: 'c2', tool: { name: 'edit_file', status: 'completed', arguments: { Path: '/a.ts' } } }),
        ],
      }),
    )
    expect(screen.getByLabelText('已处理 2 项，共 2 项')).toHaveTextContent('2 / 2')
  })
})
