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

  it('does not statically bundle excalidraw-viewer and supports dynamic ESM hot registration', () => {
    expect(artifactViewerRegistry.getViewerById('excalidraw-viewer')).toBeNull()
    const disposable = artifactViewerRegistry.register({
      id: 'excalidraw-viewer',
      pluginId: 'excalidraw-viewer',
      source: 'esm',
      title: '手绘白板',
      icon: () => null,
      extensions: ['.excalidraw', '.excalidraw.json', '.excalidraw.svg'],
      mimeTypes: ['application/vnd.excalidraw+json'],
      component: () => null,
      capabilities: { canEdit: true },
    })
    try {
      const artifact = resolveArtifactFromPath('whiteboard.excalidraw', mockSession)
      const viewer = artifactViewerRegistry.findViewer(artifact)
      expect(viewer).not.toBeNull()
      expect(viewer?.id).toBe('excalidraw-viewer')
      expect(viewer?.capabilities.canEdit).toBe(true)
    } finally {
      disposable.dispose()
    }
    expect(artifactViewerRegistry.getViewerById('excalidraw-viewer')).toBeNull()
  })

  it('detects and handles diff/patch files', () => {
    const artifact = resolveArtifactFromPath('changes.diff', mockSession)
    const viewer = artifactViewerRegistry.findViewer(artifact)
    expect(viewer).not.toBeNull()
    expect(viewer?.id).toBe('diff-viewer')
  })

  it('does not statically bundle three-viewer and supports dynamic ESM hot registration for 3D models', () => {
    const artifact = resolveArtifactFromPath('robot.stl', mockSession)
    expect(artifactViewerRegistry.getViewerById('three-viewer')).toBeNull()
    expect(artifactViewerRegistry.findViewer(artifact)?.id).not.toBe('three-viewer')

    const disposable = artifactViewerRegistry.register({
      id: 'three-viewer',
      pluginId: 'three-viewer',
      source: 'esm',
      title: '3D 模型/CAD 预览',
      icon: () => null,
      extensions: ['.stl', '.obj', '.gltf', '.glb'],
      mimeTypes: ['model/gltf+json', 'model/stl'],
      component: () => null,
      capabilities: { canEdit: false },
    })

    try {
      const matched = artifactViewerRegistry.findViewer(artifact)
      expect(matched).not.toBeNull()
      expect(matched?.id).toBe('three-viewer')
      expect(matched?.source).toBe('esm')
    } finally {
      disposable.dispose()
    }

    expect(artifactViewerRegistry.getViewerById('three-viewer')).toBeNull()
  })

  it('matches the agent decision graph sidecar tab', () => {
    const viewer = artifactViewerRegistry.findViewer({
      id: 'agentgraph:session-multi',
      name: '决策与轨迹',
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
