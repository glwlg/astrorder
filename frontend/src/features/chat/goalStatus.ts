import type { Task } from '../../domain/types'

// Native goals are projected by the App Server into the task API as
// `<source>:goal:current` with `progress.type === "goal"` (see
// app-server-go/internal/projection/goal.go). The pill must recognise every
// agent's goal through that contract instead of one hard-coded task id.

export type GoalTone = 'running' | 'waiting' | 'completed' | 'failed'

export interface GoalView {
  task: Task
  tone: GoalTone
  label: string
  title: string
}

function progressOf(task: Task): Record<string, unknown> {
  return task.progress && typeof task.progress === 'object' ? task.progress : {}
}

export function isGoalTask(task: Task): boolean {
  return progressOf(task).type === 'goal'
}

function count(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : null
}

function formatTokens(value: number): string {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1).replace(/\.0$/, '')}M`
  if (value >= 1_000) return `${(value / 1_000).toFixed(1).replace(/\.0$/, '')}k`
  return String(value)
}

function stateOf(task: Task, progress: Record<string, unknown>): { tone: GoalTone; label: string } {
  const native = typeof progress.native_status === 'string' ? progress.native_status : ''
  if (task.status === 'failed' || progress.last_verdict === 'blocked') return { tone: 'failed', label: '目标受阻' }
  if (task.status === 'completed') return { tone: 'completed', label: '目标已完成' }
  if (progress.wait_barrier || native === 'waiting') return { tone: 'waiting', label: '目标等待中' }
  if (native === 'usageLimited') return { tone: 'waiting', label: '目标受限 · 用量上限' }
  if (native === 'budgetLimited') return { tone: 'waiting', label: '目标受限 · 预算上限' }
  if (task.status === 'pending') return { tone: 'waiting', label: '目标已暂停' }
  return { tone: 'running', label: '目标进行中' }
}

function progressSuffix(tone: GoalTone, progress: Record<string, unknown>): string | null {
  const turns = count(progress.turns_used)
  const maxTurns = count(progress.max_turns)
  if (turns !== null) {
    if (tone === 'completed') return `共 ${turns} 轮`
    if (maxTurns !== null && maxTurns > 0) return `第 ${turns}/${maxTurns} 轮`
    if (turns > 0) return `第 ${turns} 轮`
    return null
  }
  const tokens = count(progress.tokensUsed)
  const budget = count(progress.tokenBudget)
  if (tokens !== null && budget !== null && budget > 0) return `${formatTokens(tokens)}/${formatTokens(budget)} tokens`
  if (tokens !== null && tokens > 0) return `${formatTokens(tokens)} tokens`
  return null
}

export function describeGoal(task: Task): GoalView {
  const progress = progressOf(task)
  const { tone, label } = stateOf(task, progress)
  const suffix = progressSuffix(tone, progress)
  return {
    task,
    tone,
    label: suffix ? `${label} · ${suffix}` : label,
    title: task.title.replace(/^目标:\s*/, ''),
  }
}

/** Latest visible native goal for a session's tasks; cleared goals are hidden. */
export function selectSessionGoal(tasks: readonly Task[]): GoalView | null {
  let current: Task | null = null
  for (const task of tasks) {
    if (!isGoalTask(task) || task.status === 'cancelled') continue
    if (!current || task.updated_at > current.updated_at) current = task
  }
  return current ? describeGoal(current) : null
}
