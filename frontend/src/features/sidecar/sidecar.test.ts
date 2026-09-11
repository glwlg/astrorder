import { describe, expect, it } from 'vitest'
import { artifactViewerRegistry } from './registry'
import { resolveArtifactFromPath } from './resolver'
import type { Session } from '../../domain/types'

describe('Artifact & Drawio Viewer Integration', () => {
  const mockSession: Session = {
    id: 'session-123',
    agent_id: 'local-hermes-default',
    title: '测试架构会话',
    workspace: 'C:/projects/astrorder',
    status: 'idle',
    updated_at: new Date().toISOString(),
  }

  it('should resolve drawio path to an artifact ref', () => {
    const filePath = 'C:/projects/astrorder/architecture.drawio'
    const artifact = resolveArtifactFromPath(filePath, mockSession)

    expect(artifact.id).toBe('local:session-123:C:/projects/astrorder/architecture.drawio')
    expect(artifact.name).toBe('architecture.drawio')
    expect(artifact.kind).toBe('workspace_file')
    expect(artifact.path).toBe(filePath)
    expect(artifact.readUrl).toContain('/api/v1/files/raw?')
    expect(artifact.readUrl).toContain('path=')
  })

  it('should match drawio viewer with high score for .drawio file', () => {
    const artifact = resolveArtifactFromPath('workflow.drawio.xml', mockSession)
    const viewer = artifactViewerRegistry.findViewer(artifact)

    expect(viewer).not.toBeNull()
    expect(viewer?.id).toBe('drawio-viewer')
    expect(viewer?.capabilities.canEdit).toBe(true)
  })

  it('should return null for unmatched arbitrary extension file', () => {
    const artifact = resolveArtifactFromPath('C:/projects/astrorder/data.unknown_bin_ext', mockSession)
    const viewer = artifactViewerRegistry.findViewer(artifact)

    expect(viewer).toBeNull()
  })
})
