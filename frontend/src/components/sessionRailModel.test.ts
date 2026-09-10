import { expect, it } from 'vitest'
import type { Project, Session } from '../domain/types'
import { buildProjectGroups } from './sessionRailModel'

const session = (id: string, workspace: string, source = 'local-codex'): Session => ({ id, agent_id: source, source_id: source, title: id, workspace, project_name: 'OpsCore', status: 'idle', updated_at: '2026-09-01T00:00:00Z' })
const project: Project = { id: 'wsl-project', source_id: 'ssh-wsl', agent_id: 'ssh-wsl', project_id: 'opscore', project_name: 'OpsCore', workspace: '/home/luwei/workspace/OpsCore', session_count: 0, updated_at: '2026-09-01T00:00:00Z' }

it('never puts local Codex sessions into a WSL catalog project with the same path', () => {
  const rows = [session('local', project.workspace!), session('remote', project.workspace!, 'ssh-wsl')]
  const groups = buildProjectGroups(rows, {}, [project])
  expect(groups.find(g => g.key === 'project:ssh-wsl\u0000opscore')?.sessions.map(s => s.id)).toEqual(['remote'])
  expect(groups.find(g => g.sessions.some(s => s.id === 'local'))?.agentId).toBe('local-codex')
})
it('keeps uncatalogued workspaces separate within a source and preserves Windows drives', () => {
  const rows = [session('p', 'P:/workspace/OpsCore'), session('c', 'C:/workspace/OpsCore'), session('linux', '/workspace/OpsCore')]
  const groups = buildProjectGroups(rows, {})
  expect(groups).toHaveLength(3)
  expect(groups.every(g => g.sessions.length === 1)).toBe(true)
})
it('does not fold case-sensitive Linux paths, but matches equivalent Windows separators', () => {
  const rows = [session('upper', '/home/OpsCore'), session('lower', '/home/opscore'), session('win-one', 'P:/Work/OpsCore'), session('win-two', 'p:\\work\\opscore')]
  const groups = buildProjectGroups(rows, {})
  expect(groups).toHaveLength(3)
  expect(groups.find(g => g.sessions.some(s => s.id === 'win-one'))?.sessions.map(s => s.id)).toEqual(['win-one', 'win-two'])
})
it('treats live activity as running for the rail filter without requiring status running', () => {
  const live = { ...session('live', 'P:/work'), live: true }
  const idle = session('idle', 'P:/other')
  const groups = buildProjectGroups([live, idle], {}, [], '', 'running')
  expect(groups.flatMap(g => g.sessions).map(s => s.id)).toEqual(['live'])
})
