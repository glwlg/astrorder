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

  it.each(['workflow.drawio.xml', 'workflow.drawio.svg'])('should match drawio viewer with high score for %s', (name) => {
    const artifact = resolveArtifactFromPath(name, mockSession)
    const viewer = artifactViewerRegistry.findViewer(artifact)

    expect(viewer).not.toBeNull()
    expect(viewer?.id).toBe('drawio-viewer')
    expect(viewer?.capabilities.canEdit).toBe(true)
  })

  it.each(['go.mod', 'go.sum', 'Dockerfile', 'Makefile', 'LICENSE', '.npmrc', 'config.custom', 'data.unknown_bin_ext'])('opens %s with the text editor when no dedicated viewer matches', (name) => {
    const artifact = resolveArtifactFromPath(`C:/projects/astrorder/${name}`, mockSession)
    const viewer = artifactViewerRegistry.findViewer(artifact)

    expect(viewer?.id).toBe('monaco-viewer')
  })

  it.each(['app.exe', 'archive.zip', 'library.dll', 'module.wasm'])('does not open %s in the text editor', (name) => {
    expect(artifactViewerRegistry.findViewer(resolveArtifactFromPath(name, mockSession))).toBeNull()
  })

  it.each(['creatures.png', 'photo.JPG', 'favicon.svg', 'animation.gif', 'picture.avif'])('opens %s in the image viewer with its remote context', (name) => {
    const artifact = resolveArtifactFromPath(`/home/luwei/${name}`, { ...mockSession, workspace: '/home/luwei', connection_id: 'ssh-images' })
    expect(artifactViewerRegistry.findViewer(artifact)?.id).toBe('image-viewer')
    expect(artifact.writable).toBe(false)
    const url = new URL(artifact.readUrl, 'http://localhost')
    expect(url.searchParams.get('session_id')).toBe(mockSession.id)
    expect(url.searchParams.get('connection_id')).toBe('ssh-images')
    expect(url.searchParams.get('path')).toBe(`/home/luwei/${name}`)
  })
})
