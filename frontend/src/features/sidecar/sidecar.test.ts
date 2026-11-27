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

  it.each(['/design', '/dev', '/data/projects/review'])('does not prefix an already resolved path beneath %s again', (workspace) => {
    const artifact = resolveArtifactFromPath(`${workspace}/plans/technical.md`, { ...mockSession, workspace, connection_id: 'ssh-work' })
    expect(artifact.path).toBe(`${workspace}/plans/technical.md`)
    const url = new URL(artifact.readUrl, 'http://localhost')
    expect(url.searchParams.get('path')).toBe(artifact.path)
    expect(url.searchParams.get('connection_id')).toBe('ssh-work')
  })

  it.each(['workflow.drawio.xml', 'workflow.drawio.svg'])('does not statically bundle drawio-viewer and matches dynamically registered drawio viewer for %s', (name) => {
    expect(artifactViewerRegistry.getViewerById('drawio-viewer')).toBeNull()
    const disposable = artifactViewerRegistry.register({
      id: 'drawio-viewer',
      pluginId: 'drawio-viewer',
      source: 'esm',
      title: '矢量流程图',
      icon: () => null,
      extensions: ['.drawio', '.drawio.xml', '.drawio.svg'],
      mimeTypes: ['application/vnd.jgraph.mxfile'],
      component: () => null,
      capabilities: { canEdit: true },
    })
    try {
      const artifact = resolveArtifactFromPath(name, mockSession)
      const viewer = artifactViewerRegistry.findViewer(artifact)

      expect(viewer).not.toBeNull()
      expect(viewer?.id).toBe('drawio-viewer')
      expect(viewer?.capabilities.canEdit).toBe(true)
    } finally {
      disposable.dispose()
    }
    expect(artifactViewerRegistry.getViewerById('drawio-viewer')).toBeNull()
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
    const artifact = resolveArtifactFromPath(`/home/fixture/${name}`, { ...mockSession, workspace: '/home/fixture', connection_id: 'ssh-images' })
    expect(artifactViewerRegistry.findViewer(artifact)?.id).toBe('image-viewer')
    expect(artifact.writable).toBe(false)
    const url = new URL(artifact.readUrl, 'http://localhost')
    expect(url.searchParams.get('session_id')).toBe(mockSession.id)
    expect(url.searchParams.get('connection_id')).toBe('ssh-images')
    expect(url.searchParams.get('path')).toBe(`/home/fixture/${name}`)
  })

  it('resolves POSIX file:/// image URIs cleanly without losing leading slash or crashing on bare percent', () => {
    const session = { ...mockSession, workspace: '/home/luwei/workspace/OpsCore', connection_id: 'ssh-ops' }
    const fromFileUri = resolveArtifactFromPath(
      'file:///home/luwei/workspace/OpsCore/docs/designs/security-drill/03-drill-arena.png',
      session,
    )
    expect(fromFileUri.path).toBe('/home/luwei/workspace/OpsCore/docs/designs/security-drill/03-drill-arena.png')
    expect(artifactViewerRegistry.findViewer(fromFileUri)?.id).toBe('image-viewer')

    const withPercent = resolveArtifactFromPath('/home/luwei/workspace/OpsCore/100% pass.png', session)
    expect(withPercent.path).toBe('/home/luwei/workspace/OpsCore/100% pass.png')
  })
})
