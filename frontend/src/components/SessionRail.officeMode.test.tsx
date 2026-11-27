import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../api/client'
import type { Agent, Project, Session } from '../domain/types'
import { clearStoredAppProfileForTest, setAppProfile } from '../features/office/appProfile'
import { usePluginSettingsStore } from '../features/plugins/pluginSettingsStore'
import { useAstrorderStore } from '../state/store'
import { SessionRail } from './SessionRail'

const agents: Record<string, Agent> = {
  codex: {
    id: 'codex',
    kind: 'codex',
    name: 'OpenAI Codex',
    status: 'ready',
    capabilities: ['chat'],
    limitation: null,
    source_id: 'source-codex',
  },
}

const worktreeSession: Session = {
  id: 'wt-sess-1',
  agent_id: 'codex',
  title: '年度方案初稿',
  workspace: '/workspace/docs-wt-draft',
  worktree_root_workspace: '/workspace/docs',
  worktree_branch: 'feat/q4-draft',
  status: 'idle',
  updated_at: '2026-11-27T10:00:00Z',
  source_id: 'source-codex',
  project_id: 'proj-docs',
  project_name: '年度规划',
}

const projects: Project[] = [
  {
    id: 'project:source-codex\u0000proj-docs',
    project_id: 'proj-docs',
    source_id: 'source-codex',
    agent_id: 'codex',
    workspace: '/workspace/docs',
    project_name: '年度规划',
    session_count: 1,
    updated_at: '2026-11-27T10:00:00Z',
  },
]

describe('SessionRail office profile sealing', () => {
  beforeEach(() => {
    localStorage.clear()
    clearStoredAppProfileForTest()
    usePluginSettingsStore.getState().reset()
    useAstrorderStore.setState({ notifications: {} })
    const preferences = { appearance: {}, session_pins: {}, pinned_projects: [], project_order: [] }
    vi.spyOn(api, 'getPreferences').mockResolvedValue(preferences)
    vi.spyOn(api, 'importPreferences').mockImplementation(async (values) => ({
      ...preferences,
      ...values,
      appearance: (values.appearance as typeof preferences.appearance) || {},
    }))
    vi.spyOn(api, 'updatePreferences').mockImplementation(async (values) => ({
      ...preferences,
      ...values,
      appearance: (values.appearance as typeof preferences.appearance) || {},
    }))
  })

  afterEach(() => {
    cleanup()
    clearStoredAppProfileForTest()
    usePluginSettingsStore.getState().reset()
    vi.restoreAllMocks()
  })

  it('hides worktree badge, delete worktree checkbox, redundant 办公 badge, and ghost 切换回开发项目 button in global office profile', async () => {
    setAppProfile('office', { syncPluginStore: false })
    const listWorktreesSpy = vi.spyOn(api, 'listGitWorktrees')

    render(
      <MantineProvider>
        <SessionRail sessions={[worktreeSession]} agents={agents} projects={projects} onSelect={vi.fn()} />
      </MantineProvider>,
    )

    expect(screen.getByText('年度规划')).toBeInTheDocument()
    expect(screen.getByText('年度方案初稿')).toBeInTheDocument()
    // Worktree branch badge & redundant project-level 办公 badge must both be hidden
    expect(screen.queryByText('feat/q4-draft')).toBeNull()
    expect(screen.queryByText('办公')).toBeNull()

    // Project menu must NOT show the ghost 切换回开发项目 or 切换为办公空间 button
    fireEvent.click(screen.getByRole('button', { name: '项目操作' }))
    expect(screen.queryByText('切换回开发项目')).toBeNull()
    expect(screen.queryByText('切换为办公空间')).toBeNull()
    expect(screen.queryByText('Worktree 管理')).toBeNull()

    // Session delete dialog must NOT show Worktree removal checkbox or call listGitWorktrees
    fireEvent.click(screen.getByRole('button', { name: '更多操作' }))
    fireEvent.click(await screen.findByText('删除会话'))
    expect(await screen.findByText('删除会话？')).toBeInTheDocument()
    expect(screen.queryByText(/同时移除 Worktree 目录/)).toBeNull()
    expect(listWorktreesSpy).not.toHaveBeenCalled()
  })
})
