import { MantineProvider } from '@mantine/core'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ArtifactRef } from '../../domain/artifact'
import type { Session } from '../../domain/types'
import { useSidecarStore } from './sidecarStore'

const viewerLifecycle = vi.hoisted(() => ({ mounts: new Map<string, number>(), unmounts: new Map<string, number>() }))

vi.mock('./registry', async () => {
  const React = await import('react')
  function StatefulViewer({ artifact }: { artifact: ArtifactRef }) {
    React.useEffect(() => {
      viewerLifecycle.mounts.set(artifact.id, (viewerLifecycle.mounts.get(artifact.id) || 0) + 1)
      return () => { viewerLifecycle.unmounts.set(artifact.id, (viewerLifecycle.unmounts.get(artifact.id) || 0) + 1) }
    }, [artifact.id])
    const [value, setValue] = React.useState(artifact.name)
    return <input aria-label={artifact.id} value={value} onChange={(event) => setValue(event.currentTarget.value)} />
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

  it('keeps a viewer mounted while switching away from and back to its session', () => {
    const first = artifact('artifact:first-session')
    const second = artifact('artifact:other-session', otherSession)
    useSidecarStore.getState().switchSession(`${session.agent_id}:${session.id}`)
    useSidecarStore.getState().openArtifact(first, 'test')

    const view = renderHost()
    fireEvent.change(screen.getByLabelText(first.id), { target: { value: 'preserved across sessions' } })

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
})
