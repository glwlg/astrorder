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

  it('reuses the browser tab and navigates it to the latest tool URL', () => {
    const store = useSidecarStore.getState()
    store.switchSession('agent-browser:session-browser')
    store.openBrowser('session-browser', 'agent-browser', 'https://example.test/start')
    useSidecarStore.getState().openBrowser('session-browser', 'agent-browser', 'https://example.test/results')

    const state = useSidecarStore.getState()
    expect(state.tabs).toHaveLength(1)
    expect(state.tabs[0].artifact?.readUrl).toBe('https://example.test/results')
    expect(state.tabs[0].viewerId).toBe('html-viewer')
    expect(state.isOpen).toBe(true)
  })
})
