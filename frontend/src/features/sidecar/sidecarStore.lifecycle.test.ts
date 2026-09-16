import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ArtifactRef } from '../../domain/artifact'
import { useSidecarStore } from './sidecarStore'

const terminalArtifact: ArtifactRef = {
  id: 'terminal:session-close',
  name: 'Terminal',
  kind: 'workspace_file',
  mediaType: 'application/x-terminal',
  readUrl: '',
  writable: false,
  sessionId: 'session-close',
  agentId: 'agent-close',
}

describe('sidecar tab lifecycle', () => {
  afterEach(() => {
    useSidecarStore.setState({
      isOpen: false,
      activeTabId: '',
      tabs: [],
      dirtyTabs: {},
      activeSessionKey: null,
      sessionMemories: {},
      mountedTabs: {},
      tabCloseHandlers: {},
    })
  })

  it('runs a terminal close handler only when its tab is explicitly closed', () => {
    const closeRuntime = vi.fn()
    useSidecarStore.getState().switchSession('agent-close:session-close')
    useSidecarStore.getState().openArtifact(terminalArtifact, 'xterm-viewer')
    const unregister = useSidecarStore.getState().registerTabCloseHandler(
      terminalArtifact.agentId,
      terminalArtifact.id,
      closeRuntime,
    )

    useSidecarStore.getState().switchSession('agent-other:session-other')
    expect(closeRuntime).not.toHaveBeenCalled()

    useSidecarStore.getState().switchSession('agent-close:session-close')
    useSidecarStore.getState().closeTab(terminalArtifact.id)
    expect(closeRuntime).toHaveBeenCalledOnce()

    unregister()
  })
})
