import { MantineProvider } from '@mantine/core'
import { cleanup, render } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ArtifactRef } from '../../../../domain/artifact'
import { useSidecarStore } from '../../sidecarStore'
import { XtermViewer } from './XtermViewer'

vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    cols = 80
    rows = 24
    loadAddon() {}
    open() {}
    writeln() {}
    write() {}
    focus() {}
    clear() {}
    dispose() {}
    onData() { return { dispose() {} } }
  },
}))

vi.mock('@xterm/addon-fit', () => ({
  FitAddon: class {
    fit() {}
  },
}))

const artifact: ArtifactRef = {
  id: 'terminal:session-close',
  name: 'Terminal',
  kind: 'workspace_file',
  mediaType: 'application/x-terminal',
  readUrl: '',
  writable: false,
  sessionId: 'session-close',
  agentId: 'agent-close',
}

class FakeWebSocket {
  static OPEN = 1
  static instances: FakeWebSocket[] = []
  readyState = FakeWebSocket.OPEN
  sent: string[] = []
  onopen: (() => void) | null = null
  onmessage: ((event: { data: string }) => void) | null = null
  onerror: (() => void) | null = null
  onclose: (() => void) | null = null

  constructor(_url: string) {
    FakeWebSocket.instances.push(this)
  }

  send(value: string) {
    this.sent.push(value)
  }

  close() {
    this.readyState = 3
  }
}

describe('XtermViewer lifecycle', () => {
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
    FakeWebSocket.instances = []
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

  it('requests daemon runtime shutdown only when the terminal tab is explicitly closed', () => {
    vi.stubGlobal('WebSocket', FakeWebSocket)
    useSidecarStore.getState().switchSession('agent-close:session-close')
    useSidecarStore.getState().openArtifact(artifact, 'xterm-viewer')

    render(
      <MantineProvider>
        <XtermViewer artifact={artifact} />
      </MantineProvider>,
    )

    useSidecarStore.getState().closeTab(artifact.id)

    expect(FakeWebSocket.instances[0].sent).toContain('{"type":"close"}')
  })
})
