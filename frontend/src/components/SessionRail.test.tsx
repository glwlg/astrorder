import { MantineProvider } from '@mantine/core'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { api } from '../api/client'
import { useAstrorderStore } from '../state/store'
import { scopeKey } from '../domain/semantics'
import { describe, expect, it, vi } from 'vitest'
import { SessionRail } from './SessionRail'
import type { Agent, Project, Session } from '../domain/types'

const agents: Record<string, Agent> = {
  local: {
    id: 'local',
    kind: 'hermes',
    name: '本机 Hermes',
    status: 'ready',
    capabilities: [],
    limitation: null,
    source_id: 'source-local',
    profile_name: 'hermes',
  },
  remote: {
    id: 'remote',
    kind: 'hermes',
    name: 'WSL 开发',
    status: 'ready',
    capabilities: [],
    limitation: null,
    source_id: 'source-remote',
    connection_id: 'ssh-wsl',
    profile_name: 'default',
  },
}

const sessions: Session[] = [
  {
    id: 'local-1',
    agent_id: 'local',
    title: '本机会话',
    workspace: '/repo',
    status: 'idle',
    updated_at: '2026-09-07T10:00:00Z',
    source_id: 'source-local',
    project_id: 'same-native-project-id',
    project_name: '同名项目',
    control_state: 'readonly',
  },
  {
    id: 'remote-1',
    agent_id: 'remote',
    title: '远程会话',
    workspace: '/repo',
    status: 'running',
    updated_at: '2026-09-07T11:00:00Z',
    source_id: 'source-remote',
    connection_id: 'ssh-wsl',
    project_id: 'same-native-project-id',
    project_name: '同名项目',
    control_state: 'readonly',
  },
]

const projects: Project[] = [
  {
    id: 'project-row-local',
    source_id: 'source-local',
    agent_id: 'local',
    project_id: 'same-native-project-id',
    project_name: '同名项目',
    workspace: '/repo',
    session_count: 1,
    updated_at: '2026-09-07T10:00:00Z',
  },
  {
    id: 'project-row-remote',
    source_id: 'source-remote',
    connection_id: 'ssh-wsl',
    agent_id: 'remote',
    project_id: 'same-native-project-id',
    project_name: '同名项目',
    workspace: '/repo',
    session_count: 1,
    updated_at: '2026-09-07T11:00:00Z',
  },
  {
    id: 'project-row-empty',
    source_id: 'source-remote',
    connection_id: 'ssh-wsl',
    agent_id: 'remote',
    project_id: 'empty-project-id',
    project_name: '空项目',
    workspace: '/empty',
    session_count: 0,
    updated_at: '2026-09-07T09:00:00Z',
  },
]

describe('SessionRail project-first grouping', () => {
  it('preserves the catalog and concurrent sessions throughout creation', async () => {
    useAstrorderStore.getState().resetRuntime()
    useAstrorderStore.getState().hydrateBootstrap({
      protocol_version: 1, cursor: 7, agents: Object.values(agents), projects, sessions,
    })
    const catalog = useAstrorderStore.getState().projects
    const created = { ...sessions[0], id: 'created', title: '新建测试' }
    const concurrent = { ...sessions[1], id: 'concurrent' }
    let resolve!: (value: Session) => void
    const request = vi.spyOn(api, 'createSession').mockImplementation(() => new Promise<Session>((done) => { resolve = done }))
    const snapshots: string[][] = []
    const unsubscribe = useAstrorderStore.subscribe((state) => snapshots.push(Object.keys(state.projects)))
    function ConnectedRail() {
      const state = useAstrorderStore()
      return <SessionRail sessions={Object.values(state.sessions)} agents={state.agents} projects={Object.values(state.projects)} onSelect={() => {}} />
    }
    const view = render(<MantineProvider><ConnectedRail /></MantineProvider>)
    try {
      fireEvent.click(view.container.querySelectorAll('[aria-label="新建会话"]')[0])
      fireEvent.change(await screen.findByLabelText('选择 Agent'), { target: { value: sessions[0].agent_id } })
      fireEvent.click(screen.getByRole('button', { name: '创建会话' }))
      await waitFor(() => expect(request).toHaveBeenCalledTimes(1))
      act(() => useAstrorderStore.setState((state) => ({ sessions: { ...state.sessions, [scopeKey(concurrent.agent_id, concurrent.id)]: concurrent } })))
      await act(async () => resolve(created))
      await waitFor(() => expect(useAstrorderStore.getState().sessions[scopeKey(created.agent_id, created.id)]).toEqual(created))
      expect(useAstrorderStore.getState().projects).toBe(catalog)
      expect(snapshots.every((keys) => keys.length === projects.length)).toBe(true)
      expect(view.container.querySelectorAll('.session-project-group')).toHaveLength(3)
      expect(useAstrorderStore.getState().sessions[scopeKey(concurrent.agent_id, concurrent.id)]).toEqual(concurrent)
    } finally {
      unsubscribe()
      request.mockRestore()
      view.unmount()
      useAstrorderStore.getState().resetRuntime()
    }
  })

  it('keeps same-name projects isolated by stable source without rendering a source parent', () => {
    render(
      <MantineProvider>
        <SessionRail sessions={sessions} agents={agents} projects={projects} onSelect={vi.fn()} />
      </MantineProvider>,
    )

    expect(screen.getAllByRole('button', { name: /同名项目.*个会话/ })).toHaveLength(2)
    const remoteProject = screen.getByRole('button', { name: /同名项目.*WSL 开发/ })
    expect(remoteProject).toBeInTheDocument()
    const source = remoteProject.querySelector('.session-project-source')
    const count = remoteProject.querySelector('.session-count')
    expect(source).toBeInTheDocument()
    expect(count).toBeInTheDocument()
    expect(source!.compareDocumentPosition(count!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(screen.getByRole('button', { name: /空项目.*0 个会话/ })).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: /本机 Hermes 会话/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('region', { name: /WSL 开发 会话/ })).not.toBeInTheDocument()
    expect(screen.queryByText('Astrorder-owned')).not.toBeInTheDocument()
    expect(screen.queryByText('本机 Hermes')).not.toBeInTheDocument()
  })

  it('renders relative time and status dots on the right side of session rows', () => {
    render(
      <MantineProvider>
        <SessionRail sessions={sessions} agents={agents} projects={projects} onSelect={vi.fn()} />
      </MantineProvider>,
    )

    const titles = screen.getAllByText('本机会话')
    expect(titles.length).toBeGreaterThan(0)
    // 确保有相对时间展示区
    const times = document.querySelectorAll('.session-row-time')
    expect(times.length).toBeGreaterThan(0)
    expect(titles[0].closest('.session-project-sessions')).toBeInTheDocument()
    const dots = document.querySelectorAll('.session-row .status-dot')
    expect(dots.length).toBeGreaterThan(0)
  })

  it('allows deleting a project and cascades its sessions upon user confirmation', async () => {
    useAstrorderStore.getState().resetRuntime()
    useAstrorderStore.getState().hydrateBootstrap({
      protocol_version: 1,
      cursor: 10,
      agents: Object.values(agents),
      projects,
      sessions,
    })

    const deleteSpy = vi.spyOn(api, 'deleteProject').mockResolvedValue({
      ok: true,
      deleted_sessions: [{ agent_id: 'local', id: 'local-1' }],
    })
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)

    function ConnectedRail() {
      const state = useAstrorderStore()
      return (
        <SessionRail
          sessions={Object.values(state.sessions)}
          agents={state.agents}
          projects={Object.values(state.projects)}
          onSelect={() => {}}
        />
      )
    }

    const view = render(
      <MantineProvider>
        <ConnectedRail />
      </MantineProvider>,
    )

    try {
      const actionButtons = view.container.querySelectorAll('.session-project-actions button[aria-label=\"项目操作\"]')
      expect(actionButtons.length).toBeGreaterThan(0)
      fireEvent.click(actionButtons[0])

      const deleteItem = await screen.findByRole('menuitem', { name: '删除项目' })
      expect(deleteItem).toBeInTheDocument()
      fireEvent.click(deleteItem)

      await waitFor(() => expect(confirmSpy).toHaveBeenCalled())
      await waitFor(() => expect(deleteSpy).toHaveBeenCalledTimes(1))

      // 验证 store 中该会话被清理
      await waitFor(() => {
        expect(useAstrorderStore.getState().sessions[scopeKey('local', 'local-1')]).toBeUndefined()
      })
    } finally {
      deleteSpy.mockRestore()
      confirmSpy.mockRestore()
      view.unmount()
      useAstrorderStore.getState().resetRuntime()
    }
  })
})
