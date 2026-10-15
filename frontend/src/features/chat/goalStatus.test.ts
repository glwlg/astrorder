import { describe, expect, it } from 'vitest'
import type { Task } from '../../domain/types'
import { describeGoal, isGoalTask, selectSessionGoal } from './goalStatus'

function task(overrides: Partial<Task> & { progress?: Record<string, unknown> | null }): Task {
  return {
    id: 'hermes:goal:current', session_id: 's', agent_id: 'a', kind: 'todo', title: '目标: 修复目标条',
    status: 'running', progress: { type: 'goal', source: 'hermes', blocking: false }, command: null,
    logs: [], target_id: null, created_at: '2026-10-13T00:00:00Z', updated_at: '2026-10-13T00:00:00Z',
    ...overrides,
  }
}

describe('native goal status', () => {
  it('recognises goals by progress.type for every agent, not by a fixed id', () => {
    expect(isGoalTask(task({}))).toBe(true)
    expect(isGoalTask(task({ id: 'codex:goal:current', progress: { type: 'goal', source: 'codex' } }))).toBe(true)
    expect(isGoalTask(task({ id: 'codex:goal:current', progress: { step: 1 } }))).toBe(false)
    expect(isGoalTask(task({ progress: null }))).toBe(false)
  })

  it('renders Hermes turn progress like the native desktop', () => {
    const progress = { type: 'goal', source: 'hermes', native_status: 'active', turns_used: 0, max_turns: 20 }
    expect(describeGoal(task({ progress }))).toMatchObject({ tone: 'running', label: '目标进行中 · 第 0/20 轮', title: '修复目标条' })
    expect(describeGoal(task({ status: 'completed', progress: { ...progress, native_status: 'done', turns_used: 3 } })).label).toBe('目标已完成 · 共 3 轮')
    expect(describeGoal(task({ status: 'pending', progress: { ...progress, native_status: 'paused', turns_used: 2 } })).label).toBe('目标已暂停 · 第 2/20 轮')
    expect(describeGoal(task({ progress: { ...progress, native_status: 'waiting' } })).tone).toBe('waiting')
    expect(describeGoal(task({ status: 'failed', progress: { ...progress, native_status: 'blocked' } })).label).toBe('目标受阻 · 第 0/20 轮')
  })

  it('renders Codex token progress and limited states', () => {
    const progress = { type: 'goal', source: 'codex', native_status: 'active', tokensUsed: 1500, tokenBudget: 20000 }
    expect(describeGoal(task({ id: 'codex:goal:current', progress })).label).toBe('目标进行中 · 1.5k/20k tokens')
    expect(describeGoal(task({ status: 'pending', progress: { ...progress, native_status: 'budgetLimited', tokenBudget: null } })).label).toBe('目标受限 · 预算上限 · 1.5k tokens')
    expect(describeGoal(task({ status: 'pending', progress: { ...progress, native_status: 'usageLimited', tokensUsed: 0, tokenBudget: null } })).label).toBe('目标受限 · 用量上限')
  })

  it('hides cleared goals and prefers the most recently updated goal', () => {
    expect(selectSessionGoal([task({ status: 'cancelled' })])).toBeNull()
    const older = task({ id: 'codex:goal:current', title: '目标: 旧', updated_at: '2026-10-13T00:00:00Z' })
    const newer = task({ title: '目标: 新', updated_at: '2026-10-13T00:01:00Z' })
    const todo = task({ id: 'todo-1', title: '普通待办', progress: null, updated_at: '2026-10-13T00:02:00Z' })
    expect(selectSessionGoal([older, newer, todo])?.title).toBe('新')
  })
})
