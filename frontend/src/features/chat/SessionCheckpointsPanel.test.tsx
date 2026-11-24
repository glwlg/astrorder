import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, type SessionCheckpoint } from '../../api/client'
import type { Session } from '../../domain/types'
import { CODE_CHECKPOINTS_PLUGIN_ID } from '../plugins/builtinPlugins'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { SessionCheckpointsPanel } from './SessionCheckpointsPanel'

const sampleSession: Session = {
  id: 'sess-1',
  agent_id: 'agent-1',
  title: 'Checkpoint Demo',
  workspace: '/workspace/demo',
  status: 'idle',
  updated_at: '2026-11-23T12:05:00Z',
}

const cp1: SessionCheckpoint = {
  id: 'cp-1',
  agent_id: 'agent-1',
  session_id: 'sess-1',
  turn_number: 1,
  kind: 'auto_before',
  label: '本轮开始前基线',
  workspace: '/workspace/demo',
  repo_root: '/workspace/demo',
  branch: 'master',
  head_sha: '1111111111111111111111111111111111111111',
  tree_sha: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
  commit_sha: 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
  ref_name: 'refs/astrorder/checkpoints/demo/cp-1',
  files_changed: 0,
  insertions: 0,
  deletions: 0,
  files: [],
  created_at: '2026-11-23T12:01:00Z',
}

const cp2: SessionCheckpoint = {
  id: 'cp-2',
  agent_id: 'agent-1',
  session_id: 'sess-1',
  turn_number: 2,
  kind: 'auto_after',
  label: '优化移动端图片灯箱',
  workspace: '/workspace/demo',
  repo_root: '/workspace/demo',
  branch: 'master',
  head_sha: '1111111111111111111111111111111111111111',
  tree_sha: 'cccccccccccccccccccccccccccccccccccccccc',
  commit_sha: 'dddddddddddddddddddddddddddddddddddddddd',
  ref_name: 'refs/astrorder/checkpoints/demo/cp-2',
  prev_tree_sha: cp1.tree_sha,
  files_changed: 1,
  insertions: 8,
  deletions: 2,
  files: [{ path: 'src/app.ts', status: 'M', insertions: 8, deletions: 2 }],
  created_at: '2026-11-23T12:04:00Z',
}

function renderPanel(session: Session = sampleSession) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <MantineProvider>
        <SessionCheckpointsPanel session={session} />
      </MantineProvider>
    </QueryClientProvider>,
  )
}

describe('SessionCheckpointsPanel', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    usePluginSettingsStore.getState().reset()
  })

  afterEach(() => {
    cleanup()
    usePluginSettingsStore.getState().reset()
  })

  it('lists checkpoints, compares turn diff, captures manual snapshot, and safely restores with confirmation', async () => {
    const listSpy = vi.spyOn(api, 'listCheckpoints').mockResolvedValue({ items: [cp2, cp1] })
    const diffSpy = vi.spyOn(api, 'diffCheckpoints').mockResolvedValue({
      from_tree: cp1.tree_sha,
      to_tree: cp2.tree_sha,
      files_changed: 1,
      insertions: 8,
      deletions: 2,
      files: [{ path: 'src/app.ts', status: 'M', insertions: 8, deletions: 2 }],
      diff: 'diff --git a/src/app.ts b/src/app.ts\n@@ -1 +1 @@\n-old\n+new',
    })
    const createSpy = vi.spyOn(api, 'createCheckpoint').mockResolvedValue({
      checkpoint: { ...cp2, id: 'cp-3', turn_number: 3, kind: 'manual', label: '发版前手动留档' },
      created: true,
    })
    const restoreSpy = vi.spyOn(api, 'restoreCheckpoint').mockResolvedValue({
      ok: true,
      restored: cp1,
      removed_files: 1,
      backup: { ...cp2, id: 'cp-backup', turn_number: 4, kind: 'pre_restore', label: '回滚至 #1 前自动备份' },
    })

    renderPanel()

    await waitFor(() => {
      expect(screen.getByText('优化移动端图片灯箱')).toBeTruthy()
      expect(screen.getByText('本轮开始前基线')).toBeTruthy()
      expect(screen.getByText('本轮开始前工作区基线（可供安全回滚）')).toBeTruthy()
    })
    expect(listSpy).toHaveBeenCalledWith('sess-1', 'agent-1')

    // Compare turn diff on cp2
    fireEvent.click(screen.getByRole('button', { name: '本轮变更' }))
    await waitFor(() => {
      expect(diffSpy).toHaveBeenCalledWith('sess-1', {
        agent_id: 'agent-1',
        from: cp1.tree_sha,
        to: cp2.tree_sha,
        path: undefined,
      })
      expect(screen.getByText('+new')).toBeTruthy()
    })

    // Filter diff by clicking file chip
    fireEvent.click(screen.getByTitle('src/app.ts'))
    await waitFor(() => {
      expect(diffSpy).toHaveBeenCalledWith('sess-1', {
        agent_id: 'agent-1',
        from: cp1.tree_sha,
        to: cp2.tree_sha,
        path: 'src/app.ts',
      })
    })

    // Manual snapshot capture
    fireEvent.change(screen.getByLabelText('检查点备注'), { target: { value: '发版前手动留档' } })
    fireEvent.click(screen.getByRole('button', { name: '保存快照' }))
    await waitFor(() => {
      expect(createSpy).toHaveBeenCalledWith('sess-1', {
        agent_id: 'agent-1',
        label: '发版前手动留档',
        kind: 'manual',
      })
    })

    // Single-file restore on cp2's diff chip
    fireEvent.click(screen.getByLabelText('仅恢复 src/app.ts 至 #2'))
    fireEvent.click(screen.getByRole('button', { name: '确认恢复单文件' }))
    await waitFor(() => {
      expect(restoreSpy).toHaveBeenCalledWith('sess-1', 'cp-2', 'agent-1', 'src/app.ts')
    })

    // Safe two-step full restore on cp1
    const restoreButtons = screen.getAllByRole('button', { name: '回滚到此' })
    fireEvent.click(restoreButtons[1])
    expect(screen.getByRole('alertdialog')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '确认安全回滚' }))
    await waitFor(() => {
      expect(restoreSpy).toHaveBeenCalledWith('sess-1', 'cp-1', 'agent-1', undefined)
    })
  })

  it('shows empty guidance when session has no workspace', () => {
    renderPanel({ ...sampleSession, workspace: null })
    expect(screen.getByText(/未关联本地或远程工作区目录/)).toBeTruthy()
  })

  it('shows disabled notice and skips fetching when code-checkpoints plugin is disabled', () => {
    const listSpy = vi.spyOn(api, 'listCheckpoints')
    usePluginSettingsStore.setState({ enabledPlugins: { [CODE_CHECKPOINTS_PLUGIN_ID]: false } })
    renderPanel()
    expect(screen.getByText(/「代码检查点」插件已停用/)).toBeTruthy()
    expect(listSpy).not.toHaveBeenCalled()
  })

  it('virtualizes large checkpoint diffs to keep DOM node count bounded', async () => {
    const hugeLines = Array.from({ length: 10_000 }, (_, i) => `+line-${i}`)
    vi.spyOn(api, 'listCheckpoints').mockResolvedValue({ items: [cp2, cp1] })
    vi.spyOn(api, 'diffCheckpoints').mockResolvedValue({
      from_tree: cp1.tree_sha,
      to_tree: cp2.tree_sha,
      files_changed: 1,
      insertions: 10_000,
      deletions: 0,
      files: [{ path: 'src/big.ts', status: 'M', insertions: 10_000, deletions: 0 }],
      diff: hugeLines.join('\n'),
    })

    const { container } = renderPanel()
    await waitFor(() => expect(screen.getByText('优化移动端图片灯箱')).toBeTruthy())
    fireEvent.click(screen.getByRole('button', { name: '本轮变更' }))

    const pre = await screen.findByLabelText('代码差异内容')
    expect(pre.getAttribute('data-total-lines')).toBe('10000')
    const renderedLines = container.querySelectorAll('.cp-diff-line')
    expect(renderedLines.length).toBeGreaterThan(20)
    expect(renderedLines.length).toBeLessThan(150)
    expect(screen.getByText('+line-0')).toBeTruthy()
    expect(screen.queryByText('+line-9999')).toBeNull()
  })
})
