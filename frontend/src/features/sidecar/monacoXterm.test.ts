import { describe, expect, it } from 'vitest'
import { artifactViewerRegistry } from './registry'
import { resolveArtifactFromPath } from './resolver'
import type { Session } from '../../domain/types'

const mockSession: Session = {
  id: 'session-multi',
  agent_id: 'local-hermes',
  title: '测试会话',
  workspace: 'C:/workspace/test',
  status: 'idle',
  updated_at: '2026-09-10T12:00:00Z',
}

describe('Extended Viewer Suite: Monaco and Xterm', () => {
  it('detects and handles code files with monaco editor', () => {
    const artifact = resolveArtifactFromPath('server.py', mockSession)
    const viewer = artifactViewerRegistry.findViewer(artifact)
    expect(viewer).not.toBeNull()
    expect(viewer?.id).toBe('monaco-viewer')
    expect(viewer?.capabilities.canEdit).toBe(true)
  })

  it('detects and handles script files like .sh and .ps1 with monaco editor', () => {
    const artifactSh = resolveArtifactFromPath('deploy.sh', mockSession)
    const viewerSh = artifactViewerRegistry.findViewer(artifactSh)
    expect(viewerSh).not.toBeNull()
    expect(viewerSh?.id).toBe('monaco-viewer')
    expect(viewerSh?.capabilities.canEdit).toBe(true)

    const artifactPs1 = resolveArtifactFromPath('setup.ps1', mockSession)
    const viewerPs1 = artifactViewerRegistry.findViewer(artifactPs1)
    expect(viewerPs1).not.toBeNull()
    expect(viewerPs1?.id).toBe('monaco-viewer')
    expect(viewerPs1?.capabilities.canEdit).toBe(true)
  })
})
