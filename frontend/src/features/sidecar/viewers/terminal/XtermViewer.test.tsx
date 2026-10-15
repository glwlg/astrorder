import { MantineProvider } from '@mantine/core'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ArtifactRef } from '../../../../domain/artifact'
import { useSidecarStore } from '../../sidecarStore'
import { XtermViewer } from './XtermViewer'

const terminalSpies = vi.hoisted(() => ({ focus: vi.fn(), dispose: vi.fn(), write: vi.fn(), fit: vi.fn() }))

vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    cols = 80
    rows = 24
    loadAddon() {}
    open() {}
    writeln() {}
    write = terminalSpies.write
    focus = terminalSpies.focus
    clear() {}
    dispose = terminalSpies.dispose
    onData() { return { dispose() {} } }
  },
}))

vi.mock('@xterm/addon-fit', () => ({
  FitAddon: class {
    fit = terminalSpies.fit
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
  binaryType = 'blob'
  onmessage: ((event: { data: string | ArrayBuffer }) => void) | null = null
  onerror: (() => void) | null = null
  onclose: (() => void) | null = null

  readonly url: string

  constructor(url: string) {
    this.url = url
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
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  })
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
    vi.clearAllMocks()
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

  it('scopes the terminal to the exact agent and decodes binary daemon output', () => {
    vi.stubGlobal('WebSocket', FakeWebSocket)
    render(<MantineProvider><XtermViewer artifact={artifact} /></MantineProvider>)
    const socket = FakeWebSocket.instances[0]
    const url = new URL(socket.url)
    expect(url.searchParams.get('session_id')).toBe(artifact.sessionId)
    expect(url.searchParams.get('agent_id')).toBe(artifact.agentId)
    expect(socket.binaryType).toBe('arraybuffer')
    const data = new TextEncoder().encode('native output').buffer
    act(() => socket.onmessage?.({ data }))
    expect(terminalSpies.write).toHaveBeenCalledWith(new Uint8Array(data))
  })

  it('sends a fresh size on reopen without focusing or resizing a hidden terminal', () => {
    vi.stubGlobal('WebSocket', FakeWebSocket)
    const terminal = (isActive: boolean) => <MantineProvider><XtermViewer artifact={artifact} isActive={isActive} /></MantineProvider>
    const view = render(terminal(false))
    const socket = FakeWebSocket.instances[0]
    act(() => socket.onopen?.())
    expect(terminalSpies.focus).not.toHaveBeenCalled()
    socket.sent = []
    terminalSpies.fit.mockClear()
    fireEvent(window, new Event('resize'))
    expect(socket.sent).toEqual([])
    expect(terminalSpies.fit).not.toHaveBeenCalled()
    view.rerender(terminal(true))
    expect(terminalSpies.fit).toHaveBeenCalled()
    expect(socket.sent).toContain(JSON.stringify({ type: 'resize', cols: 80, rows: 24 }))
  })

  it('disposes the previous attachment before reconnecting and ignores its late callbacks', () => {
    vi.stubGlobal('WebSocket', FakeWebSocket)
    render(<MantineProvider><XtermViewer artifact={artifact} /></MantineProvider>)
    const previous = FakeWebSocket.instances[0]
    const lateClose = previous.onclose
    fireEvent.click(screen.getByRole('button', { name: '重连' }))
    expect(previous.readyState).toBe(3)
    expect(terminalSpies.dispose).toHaveBeenCalledOnce()
    expect(FakeWebSocket.instances).toHaveLength(2)
    act(() => FakeWebSocket.instances[1].onopen?.())
    expect(screen.getByText('在线')).toBeVisible()
    act(() => lateClose?.())
    expect(screen.getByText('在线')).toBeVisible()
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
