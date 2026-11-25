import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '../api/client'
import type { Agent } from '../domain/types'
import { agentEnvironment, agentLabel, NewSessionDialog } from './NewSessionDialog'
const agents: Record<string, Agent> = Object.fromEntries(['hermes', 'codex'].map(kind => [kind, { id: kind, kind, name: kind, source_id: 'local-' + kind, status: 'ready', capabilities: ['chat'], limitation: null }])) as Record<string, Agent>
afterEach(() => { cleanup(); vi.restoreAllMocks() })
it('requires explicit Agent selection and uses the returned native ID', async () => {
 const created = { id: 'native-id', agent_id: 'codex', title: 'new', workspace: '/work', status: 'idle' as const, updated_at: '2026-09-09T00:00:00Z' }
 const create = vi.spyOn(api, 'createSession').mockResolvedValue(created)
 const onCreated = vi.fn()
 const { container } = render(<MantineProvider><NewSessionDialog agents={agents} project={null} onClose={() => {}} onCreated={onCreated} /></MantineProvider>)
 expect(container.querySelectorAll('select')).toHaveLength(0)
 expect(await screen.findByRole('button', { name: '创建会话' })).toBeDisabled()
 await userEvent.click(screen.getByRole('button', { name: '选择 Agent' }))
 await userEvent.click(await screen.findByRole('menuitem', { name: /Codex · codex · 本机/ }))
 fireEvent.change(screen.getByLabelText('工作区'), { target: { value: '/work' } })
 fireEvent.click(screen.getByRole('button', { name: '创建会话' }))
 await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({ agent_id: 'codex', workspace: '/work' })))
 expect(onCreated).toHaveBeenCalledWith(created)
})

it('offers exactly the Grok from the project environment', async () => {
 const all = Object.fromEntries(['local', 'wsl', 'debian'].map(environment => {
  const id = environment + '-grok'
  return [id, { id, kind: 'grok', name: 'Grok Build', status: 'ready', capabilities: ['chat'], limitation: null, connection_id: environment === 'local' ? null : environment }]
 })) as Record<string, Agent>
 for (const environment of ['local', 'wsl', 'debian']) {
  const owner = environment + '-grok'
  const project = { key: environment, agentId: owner, label: environment, workspace: '/work', sessions: [], sessionCount: 0, latestUpdatedAt: '' }
  const view = render(<MantineProvider><NewSessionDialog agents={all} project={project} onClose={() => {}} onCreated={() => {}} /></MantineProvider>)
  await userEvent.click(screen.getByRole('button', { name: '选择 Agent' }))
  const choices = await screen.findAllByRole('menuitem')
  expect(choices).toHaveLength(1)
  expect(choices[0]).toHaveTextContent(agentLabel(all[owner]))
  view.unmount()
 }
})

it('uses generic labels and treats Agents without a connection as local', () => {
 const agent: Agent = { id: 'claude', kind: 'claude-code', name: 'Claude', status: 'ready', capabilities: ['chat'], limitation: null }
 expect(agentLabel(agent)).toBe('Claude Code · Claude · 本机')
 expect(agentEnvironment(agent)).toBe('local')
})

it('allows creating a new Git worktree with ignored env sync when creating a session', async () => {
 vi.spyOn(api, 'listGitWorktrees').mockResolvedValue({
  is_git_repo: true,
  workspace: '/repo/demo',
  main_workspace: '/repo/demo',
  is_worktree: false,
  current_branch: 'main',
  default_branch: 'main',
  default_sibling_base: '/repo/demo-worktrees',
  default_inside_base: '/repo/demo/.worktrees',
  worktrees: [
   { path: '/repo/demo', head: 'abc1234', branch: 'main', detached: false, bare: false, is_main: true, is_current: true, locked: false, prunable: false, status_loaded: true, dirty: false, changed_files: 0, ahead: 0, behind: 0 },
   { path: '/repo/demo-worktrees/feat/existing', head: 'def5678', branch: 'feat/existing', detached: false, bare: false, is_main: false, is_current: false, locked: false, prunable: false, status_loaded: true, dirty: true, changed_files: 2, ahead: 1, behind: 0 },
  ],
  branches: [
   { name: 'main', is_current: true, occupied_by_path: '/repo/demo' },
   { name: 'feat/existing', is_current: false, occupied_by_path: '/repo/demo-worktrees/feat/existing' },
  ],
  copyable_files: [
   { path: '.env', size: 128, source: 'default' },
  ],
 })
 const created = {
  id: 'wt-1',
  agent_id: 'codex',
  title: 'Worktree Task',
  workspace: '/repo/demo/.worktrees/feat/wt-new',
  worktree_root_workspace: '/repo/demo',
  worktree_branch: 'feat/wt-new',
  worktree_managed: true,
  status: 'idle' as const,
  updated_at: '2026-11-25T00:00:00Z',
 }
 const createSpy = vi.spyOn(api, 'createSession').mockResolvedValue(created)
 const onCreated = vi.fn()
 const project = { key: 'ws', agentId: 'codex', label: 'demo', workspace: '/repo/demo', sessions: [], sessionCount: 0, latestUpdatedAt: '' }

 render(<MantineProvider><NewSessionDialog agents={agents} project={project} initialAgentId="codex" onClose={() => {}} onCreated={onCreated} /></MantineProvider>)

 expect(await screen.findByText('Git Worktree 工作区')).toBeInTheDocument()
 fireEvent.click(screen.getByText('新建 Worktree'))
 const branchInput = await screen.findByLabelText(/新分支名称/)
 fireEvent.change(branchInput, { target: { value: 'feat/wt-new' } })
 expect(screen.getByText('.env')).toBeInTheDocument()
 fireEvent.click(screen.getByRole('button', { name: '创建 Worktree 并新建会话' }))

 await waitFor(() => expect(createSpy).toHaveBeenCalledWith(expect.objectContaining({
  agent_id: 'codex',
  workspace: '/repo/demo',
  worktree: expect.objectContaining({
   mode: 'create',
   branch: 'feat/wt-new',
   create_branch: true,
   layout: 'inside',
   path: '/repo/demo/.worktrees/feat/wt-new',
   copy_files: ['.env'],
  }),
 })))
 expect(onCreated).toHaveBeenCalledWith(created)
})
