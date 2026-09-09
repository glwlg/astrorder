import { expect, it } from 'vitest'
import type { Project, Session } from '../domain/types'
import { buildProjectGroups } from './sessionRailModel'
const row: Session = { id: 'native', agent_id: 'local-codex', source_id: 'local-codex', title: 'The following is the Codex agent history', workspace: '/home/luwei/workspace/OpsCore', status: 'idle', updated_at: '2026-09-01T00:00:00Z' }
const project: Project = { id: 'p', project_id: 'p', project_name: 'OpsCore', source_id: 'local-hermes-default', agent_id: 'local-hermes-default', workspace: row.workspace, session_count: 0, updated_at: row.updated_at }
it('merges local Agents at the same path', () => {
  const groups = buildProjectGroups([{ ...row, project_id: 'codex-project' }], {}, [project, { ...project, id: 'c', project_id: 'codex-project', source_id: 'local-codex', agent_id: 'local-codex' }])
  expect(groups).toHaveLength(1)
  expect(groups[0].sessions.map(s => s.id)).toEqual(['native'])
})
it('merges Agents on the same explicit remote connection and not another connection', () => {
  const groups = buildProjectGroups([{ ...row, agent_id: 'remote-codex', source_id: 'remote-codex', connection_id: 'wsl' }], {}, [{ ...project, connection_id: 'wsl', source_id: 'remote-hermes' }])
  expect(groups).toHaveLength(1)
  const separated = buildProjectGroups([{ ...row, connection_id: 'other' }], {}, [{ ...project, connection_id: 'wsl' }])
  expect(separated).toHaveLength(2)
})
it('filters native subagents, never ordinary conversations by their title', () => {
  const groups = buildProjectGroups([row, { ...row, id: 'internal', native_kind: 'subagent' }], {})
  expect(groups.flatMap(g => g.sessions).map(s => s.id)).toEqual(['native'])
})
