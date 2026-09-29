import { expect, it } from 'vitest'
import type { Project, Session } from '../domain/types'
import { buildProjectGroups } from './sessionRailModel'
const source = 'hermes-local-2d1d217b0499555631cf7cce'
const project: Project = { id: 'catalog', project_id: 'p_0d99a36e', source_id: source, agent_id: 'local-hermes-default', project_name: 'Ariadne', workspace: 'P:\\workspace\\glwlg\\app\\Ariadne', session_count: 0, updated_at: '2026-09-09T00:00:00Z' }
const session = (id: string, workspace: string, source_id = 'local-codex'): Session => ({ id, workspace, source_id, agent_id: source_id === source ? 'local-hermes-default' : source_id, title: id, status: 'idle', updated_at: project.updated_at })
it('merges actual Hermes source identity and Windows Codex paths without mutating native paths', () => {
  const rows = [session('hermes', 'P:/workspace/glwlg/app/Ariadne', source), session('codex', 'p:\\workspace\\glwlg\\app\\ariadne'), session('extended', '\\\\?\\P:\\workspace\\glwlg\\app\\Ariadne')]
  const original = rows.map(s => s.workspace)
  const groups = buildProjectGroups(rows, {}, [project])
  expect(groups).toHaveLength(1)
  expect(groups[0].sessions.map(s => s.id)).toEqual(['hermes', 'codex', 'extended'])
  expect(rows.map(s => s.workspace)).toEqual(original)
})
it('does not treat Linux backslash filenames or different case as equivalent directories', () => {
  const groups = buildProjectGroups([session('a', '/home/luwei/Ariadne', 'linux'), session('b', '/home/luwei/ariadne', 'linux'), session('c', '/home/luwei\\Ariadne', 'linux')], {})
  expect(groups).toHaveLength(3)
})
it('keeps native Codex sessions alongside Hermes in astrorder using the real extended Windows path', () => {
  const native = '\\\\?\\P:\\workspace\\glwlg\\ai\\astrorder'
  const groups = buildProjectGroups([
    { ...session('codex-1', native), title: '修复会话列表' },
    { ...session('codex-2', native), title: '项目同步' },
    { ...session('hermes', 'P:/workspace/glwlg/ai/astrorder', source), title: '会话标题' },
  ], {}, [{ ...project, project_id: 'astrorder-id', project_name: 'astrorder', workspace: 'P:/workspace/glwlg/ai/astrorder' }])
  expect(groups).toHaveLength(1)
  expect(groups[0].sessions.map(row => row.title)).toEqual(['修复会话列表', '项目同步', '会话标题'])
})
