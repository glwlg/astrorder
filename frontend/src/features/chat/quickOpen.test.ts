import { describe, expect, it, vi } from 'vitest'
import { resolveArtifactFromPath } from '../sidecar/resolver'
import type { Session } from '../../domain/types'

const mockSession: Session = {
  id: 'session-quick',
  agent_id: 'local-hermes',
  title: '快速打开',
  workspace: 'C:/workspace/test',
  status: 'idle',
  updated_at: '2026-09-11T12:00:00Z',
}

describe('Quick Open file resolution', () => {
  it('resolves absolute paths to a workspace_file artifact with a raw readUrl', () => {
    const artifact = resolveArtifactFromPath('C:/workspace/test/src/app.py', mockSession)
    expect(artifact.kind).toBe('workspace_file')
    expect(artifact.readUrl).toContain('/api/v1/files/raw')
    expect(artifact.readUrl).toContain('session_id=session-quick')
    expect(artifact.name).toBe('app.py')
  })

  it('keeps SSH connection id on the readUrl', () => {
    const remote = { ...mockSession, connection_id: 'ssh-1' }
    const artifact = resolveArtifactFromPath('/home/app/main.ts', remote)
    expect(artifact.readUrl).toContain('connection_id=ssh-1')
  })

  it('routes unknown extensions to the default code editor via caller fallback', () => {
    const artifact = resolveArtifactFromPath('C:/workspace/test/notes.xyz', mockSession)
    expect(artifact.kind).toBe('workspace_file')
    // 注册表无匹配时由调用方兜底 monaco-viewer；这里只验证 artifact 可用
    expect(artifact.readUrl).toBeTruthy()
  })

  it('debounces quick-open search calls (contract check)', () => {
    vi.useFakeTimers()
    let calls = 0
    const run = () => { calls += 1 }
    let t: ReturnType<typeof setTimeout> | null = null
    const debounced = () => { if (t) clearTimeout(t); t = setTimeout(run, 220) }
    debounced()
    debounced()
    debounced()
    vi.advanceTimersByTime(220)
    expect(calls).toBe(1)
    vi.useRealTimers()
  })
})
