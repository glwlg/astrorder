import { MantineProvider } from '@mantine/core'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ViewerContext } from './types'
import type { ArtifactRef } from '../../domain/artifact'
import type { Session } from '../../domain/types'
import { useSidecarStore } from './sidecarStore'

const viewerLifecycle = vi.hoisted(() => ({ mounts: new Map<string, number>(), unmounts: new Map<string, number>() }))

vi.mock('./registry', async () => {
  const React = await import('react')
  function StatefulViewer({ artifact, toolbarAction, onSave, isActive }: ViewerContext) {
    React.useEffect(() => {
      viewerLifecycle.mounts.set(artifact.id, (viewerLifecycle.mounts.get(artifact.id) || 0) + 1)
      return () => { viewerLifecycle.unmounts.set(artifact.id, (viewerLifecycle.unmounts.get(artifact.id) || 0) + 1) }
    }, [artifact.id])
    const [value, setValue] = React.useState(artifact.name)
    return <div data-testid={`viewer-${artifact.id}`} data-active={isActive}>{toolbarAction}<input aria-label={artifact.id} value={value} onChange={(event) => setValue(event.currentTarget.value)} /><button onClick={() => void onSave?.(value)}>保存测试文件</button></div>
  }
  return { artifactViewerRegistry: { findViewer: () => ({ component: StatefulViewer }) } }
})

import { SidecarHost } from './SidecarHost'

const session: Session = {
  id: 'session-keepalive',
  agent_id: 'agent-keepalive',
  title: 'Keep alive',
  workspace: 'P:/workspace',
  status: 'idle',
  updated_at: '2026-09-15T00:00:00Z',
}

const otherSession: Session = {
  ...session,
  id: 'session-other',
  title: 'Other session',
}

function artifact(id: string, owner: Session = session): ArtifactRef {
  return {
    id,
    name: id,
    kind: 'workspace_file',
    mediaType: 'text/plain',
    readUrl: `/files/${id}`,
    writable: true,
    sessionId: owner.id,
    agentId: owner.agent_id,
    path: `P:/workspace/${id}.txt`,
  }
}

function host(owner: Session) {
  return (
    <MantineProvider>
      <SidecarHost
        session={owner}
        commands={[]}
        messages={[]}
        approvals={[]}
        onApproval={() => undefined}
        onCloseSidecar={() => undefined}
      />
    </MantineProvider>
  )
}

function renderHost() {
  return render(host(session))
}

describe('SidecarHost tab keep-alive', () => {
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
    localStorage.removeItem('astrorder:token')
    viewerLifecycle.mounts.clear()
    viewerLifecycle.unmounts.clear()
    useSidecarStore.setState({
      isOpen: false,
      activeTabId: '',
      tabs: [],
      dirtyTabs: {},
      activeSessionKey: null,
      sessionMemories: {},
    })
  })

  it('keeps every open viewer mounted while switching tabs', () => {
    const first = artifact('artifact:first')
    const second = artifact('artifact:second')
    useSidecarStore.setState({
      isOpen: true,
      activeTabId: first.id,
      activeSessionKey: 'agent-keepalive:session-keepalive',
      tabs: [
        { id: first.id, type: 'artifact', title: 'First', artifact: first, viewerId: 'test', closable: true },
        { id: second.id, type: 'artifact', title: 'Second', artifact: second, viewerId: 'test', closable: true },
      ],
    })

    renderHost()
    fireEvent.change(screen.getByLabelText(first.id), { target: { value: 'preserved editor state' } })

    act(() => useSidecarStore.getState().setActiveTabId(second.id))

    expect(screen.getByLabelText(first.id)).toHaveValue('preserved editor state')
    expect(screen.getByLabelText(second.id)).toBeInTheDocument()
    expect(viewerLifecycle.mounts.get(first.id)).toBe(1)
    expect(viewerLifecycle.mounts.get(second.id)).toBe(1)
    expect(viewerLifecycle.unmounts.get(first.id) || 0).toBe(0)
  })

  it('renders file location inside the active viewer instead of the tab bar', () => {
    const file = artifact('artifact:located')
    useSidecarStore.setState({
      isOpen: true,
      activeTabId: file.id,
      activeSessionKey: 'agent-keepalive:session-keepalive',
      tabs: [{ id: file.id, type: 'artifact', title: 'Located', artifact: file, viewerId: 'test', closable: true }],
    })

    renderHost()
    const button = screen.getByRole('button', { name: '在文件树中定位' })
    expect(button.closest('.sidecar-header-bar')).toBeNull()
    expect(screen.getByTestId(`viewer-${file.id}`)).toContainElement(button)
  })

  it('keeps a dirty viewer mounted while switching away from and back to its session', () => {
    const first = artifact('artifact:first-session')
    const second = artifact('artifact:other-session', otherSession)
    useSidecarStore.getState().switchSession(`${session.agent_id}:${session.id}`)
    useSidecarStore.getState().openArtifact(first, 'test')

    const view = renderHost()
    fireEvent.change(screen.getByLabelText(first.id), { target: { value: 'preserved across sessions' } })
    act(() => useSidecarStore.getState().setTabDirty(first.id, true))

    act(() => {
      useSidecarStore.getState().switchSession(`${otherSession.agent_id}:${otherSession.id}`)
      useSidecarStore.getState().openArtifact(second, 'test')
    })
    view.rerender(host(otherSession))

    expect(viewerLifecycle.unmounts.get(first.id) || 0).toBe(0)

    act(() => useSidecarStore.getState().switchSession(`${session.agent_id}:${session.id}`))
    view.rerender(host(session))

    expect(screen.getByLabelText(first.id)).toHaveValue('preserved across sessions')
    expect(viewerLifecycle.mounts.get(first.id)).toBe(1)
    expect(viewerLifecycle.unmounts.get(first.id) || 0).toBe(0)
  })

  it('authenticates a mobile file save and retains the artifact session identity', async () => {
    const file = { ...artifact('artifact:save'), connectionId: 'remote-artifact' }
    localStorage.setItem('astrorder:token', 'isolated-test-credential')
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), { status: 200 }))
    vi.stubGlobal('fetch', fetcher)
    useSidecarStore.setState({ activeTabId: file.id, activeSessionKey: `${session.agent_id}:${session.id}`, tabs: [{ id: file.id, type: 'artifact', title: 'Save', artifact: file, viewerId: 'test', closable: true }] })
    render(<MantineProvider><SidecarHost session={session} commands={[]} messages={[]} approvals={[]} onApproval={() => undefined} onCloseSidecar={() => undefined} surface="mobile" /></MantineProvider>)
    fireEvent.change(screen.getByLabelText(file.id), { target: { value: 'saved mobile content' } })
    fireEvent.click(screen.getByRole('button', { name: '保存测试文件' }))
    await waitFor(() => expect(fetcher).toHaveBeenCalledWith('/api/v1/system/write-file', expect.objectContaining({ method: 'POST', credentials: 'include', body: JSON.stringify({ path: file.path, content: 'saved mobile content', session_id: session.id, connection_id: 'remote-artifact' }) })))
    const headers = new Headers(fetcher.mock.calls[0][1].headers)
    expect(headers.get('Authorization')).toBe('Bearer isolated-test-credential')
  })

  it('saves a retained local draft without borrowing the selected remote connection', async () => {
    const file = artifact('artifact:local-draft')
    const remoteSession = { ...otherSession, connection_id: 'remote-selected' }
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ status: 'ok' }), { status: 200 }))
    vi.stubGlobal('fetch', fetcher)
    useSidecarStore.getState().switchSession(`${session.agent_id}:${session.id}`)
    useSidecarStore.getState().openArtifact(file, 'test')
    const view = renderHost()
    fireEvent.change(screen.getByLabelText(file.id), { target: { value: 'local draft content' } })
    act(() => useSidecarStore.getState().setTabDirty(file.id, true))
    act(() => useSidecarStore.getState().switchSession(`${remoteSession.agent_id}:${remoteSession.id}`))
    view.rerender(host(remoteSession))
    const save = screen.getByTestId(`viewer-${file.id}`).querySelector('button:last-child')
    expect(save).not.toBeNull()
    fireEvent.click(save!)
    await waitFor(() => expect(fetcher).toHaveBeenCalled())
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual({ path: file.path, content: 'local draft content', session_id: session.id })
  })

  it('pauses a hidden mobile viewer without losing unsaved content', () => {
    const file = artifact('artifact:mobile-draft')
    useSidecarStore.setState({ activeTabId: file.id, activeSessionKey: `${session.agent_id}:${session.id}`, tabs: [{ id: file.id, type: 'artifact', title: 'Draft', artifact: file, viewerId: 'test', closable: true }] })
    const mobile = (isVisible: boolean) => <MantineProvider><SidecarHost session={session} commands={[]} messages={[]} approvals={[]} onApproval={() => undefined} onCloseSidecar={() => undefined} surface="mobile" isVisible={isVisible} /></MantineProvider>
    const view = render(mobile(true))
    fireEvent.change(screen.getByLabelText(file.id), { target: { value: 'unsaved mobile draft' } })
    expect(screen.getByTestId(`viewer-${file.id}`)).toHaveAttribute('data-active', 'true')
    view.rerender(mobile(false))
    expect(screen.getByTestId(`viewer-${file.id}`)).toHaveAttribute('data-active', 'false')
    expect(screen.getByLabelText(file.id)).toHaveValue('unsaved mobile draft')
    view.rerender(mobile(true))
    expect(screen.getByTestId(`viewer-${file.id}`)).toHaveAttribute('data-active', 'true')
    expect(screen.getByLabelText(file.id)).toHaveValue('unsaved mobile draft')
    expect(viewerLifecycle.mounts.get(file.id)).toBe(1)
  })

  it('unmounts clean viewers from inactive sessions', () => {
    const first = artifact('artifact:clean-session')
    const second = artifact('artifact:next-session', otherSession)
    useSidecarStore.getState().switchSession(`${session.agent_id}:${session.id}`)
    useSidecarStore.getState().openArtifact(first, 'test')
    const view = renderHost()

    act(() => {
      useSidecarStore.getState().switchSession(`${otherSession.agent_id}:${otherSession.id}`)
      useSidecarStore.getState().openArtifact(second, 'test')
    })
    view.rerender(host(otherSession))

    expect(viewerLifecycle.unmounts.get(first.id)).toBe(1)
  })
})
