import { expect, it } from 'vitest'
import { buildProjectGroups } from './sessionRailModel'
import { moveProject, reconcileProjectOrder } from './projectOrder'
import type { Project } from '../domain/types'

it('preserves hidden source slots and appends new projects without moving existing keys', () => {
  const initial = reconcileProjectOrder([], ['a', 'b', 'c'])
  const dragged = moveProject(initial, 'c', 'a')
  expect(dragged).toEqual(['c', 'a', 'b'])
  expect(reconcileProjectOrder(dragged, ['b', 'new'])).toEqual(['c', 'a', 'b', 'new'])
  expect(moveProject(dragged, 'c', 'b')).toEqual(['a', 'b', 'c'])
})

it('uses alphabetic project order, never activity or pin priority', () => {
  const projects: Project[] = ['Zulu', 'alpha', 'Beta'].map((name, i) => ({ id: name, project_id: name, project_name: name, source_id: 'source', agent_id: 'agent', session_count: 0, updated_at: `2026-09-0${9-i}T00:00:00Z` }))
  const first = buildProjectGroups([], {}, projects)
  expect(first.map(p => p.label)).toEqual(['alpha', 'Beta', 'Zulu'])
  const updated = projects.map(p => ({ ...p, updated_at: '2027-01-01T00:00:00Z' }))
  expect(buildProjectGroups([], {}, updated, '', 'all', [first[2].key]).map(p => p.key)).toEqual(first.map(p => p.key))
})
