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

describe('Multi-Artifact Viewer Suite', () => {
  it('detects and handles html files', () => {
    const artifact = resolveArtifactFromPath('report.html', mockSession)
    const viewer = artifactViewerRegistry.findViewer(artifact)
    expect(viewer).not.toBeNull()
    expect(viewer?.id).toBe('html-viewer')
  })

  it('detects and handles mermaid files', () => {
    const artifact = resolveArtifactFromPath('flowchart.mmd', mockSession)
    const viewer = artifactViewerRegistry.findViewer(artifact)
    expect(viewer).not.toBeNull()
    expect(viewer?.id).toBe('mermaid-viewer')
    expect(viewer?.capabilities.canEdit).toBe(true)
  })

  it('detects and handles excalidraw files', () => {
    const artifact = resolveArtifactFromPath('whiteboard.excalidraw', mockSession)
    const viewer = artifactViewerRegistry.findViewer(artifact)
    expect(viewer).not.toBeNull()
    expect(viewer?.id).toBe('excalidraw-viewer')
    expect(viewer?.capabilities.canEdit).toBe(true)
  })

  it('detects and handles diff/patch files', () => {
    const artifact = resolveArtifactFromPath('changes.diff', mockSession)
    const viewer = artifactViewerRegistry.findViewer(artifact)
    expect(viewer).not.toBeNull()
    expect(viewer?.id).toBe('diff-viewer')
  })

  it('detects and handles 3D model files', () => {
    const artifact = resolveArtifactFromPath('robot.stl', mockSession)
    const viewer = artifactViewerRegistry.findViewer(artifact)
    expect(viewer).not.toBeNull()
    expect(viewer?.id).toBe('three-viewer')
  })

  it('matches the agent decision graph sidecar tab', () => {
    const viewer = artifactViewerRegistry.findViewer({
      id: 'agentgraph:session-multi',
      name: '决策状态机',
      kind: 'workspace_file',
      mediaType: 'application/x-agent-graph',
      readUrl: '',
      writable: false,
      sessionId: mockSession.id,
      agentId: mockSession.agent_id,
    })
    expect(viewer?.id).toBe('agent-graph-viewer')
  })
})
