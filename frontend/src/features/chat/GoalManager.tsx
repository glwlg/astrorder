import { Alert, Button, Group, Modal, Stack, Text, Textarea } from '@mantine/core'
import { IconCheck, IconPencil, IconPlayerPause, IconPlayerPlay, IconTrash, IconX } from '@tabler/icons-react'
import { useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { api } from '../../api/client'
import { ConfirmPopover } from '../../components/ConfirmPopover'
import type { Agent, Session, Task } from '../../domain/types'
import { useAstrorderStore } from '../../state/store'
import { selectSessionGoal } from './goalStatus'

export function GoalManager({ session, agent, tasks, disabled = false }: { session: Session; agent?: Agent; tasks: Task[]; disabled?: boolean }) {
  const goal = selectSessionGoal(tasks)
  const [editing, setEditing] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [deleteCoords, setDeleteCoords] = useState<{ x: number; y: number } | undefined>()
  const [objective, setObjective] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const busy = useRef(false)
  const client = useQueryClient()
  const supported = agent?.kind === 'codex' || agent?.kind === 'hermes'
  const hermes = agent?.kind === 'hermes'
  const active = session.status === 'running' || session.status === 'waiting_approval'
  const nativeStatus = String(goal?.task.progress?.native_status ?? goal?.task.progress?.status ?? '')
  const paused = nativeStatus === 'paused' || nativeStatus === 'usageLimited' || nativeStatus === 'budgetLimited' || nativeStatus === 'blocked'
  const resumable = paused && !(hermes && active)
  const pausable = nativeStatus === 'active' || nativeStatus === 'waiting'
  const mutate = async (clear: boolean, requestedStatus?: 'paused' | 'active') => {
    if (busy.current || disabled || !supported) return
    busy.current = true
    setPending(true)
    setError(null)
    try {
      const result = requestedStatus ? await api.setSessionGoalStatus(session.id, session.agent_id, requestedStatus) : clear
        ? await api.clearSessionGoal(session.id, session.agent_id)
        : await api.setSessionGoal(session.id, session.agent_id, objective.trim())
      const native = result.goal
      const state = native?.status
      const status: Task['status'] = native === null ? 'cancelled'
        : state === 'done' || state === 'complete' ? 'completed'
        : state === 'blocked' ? 'failed'
        : state === 'paused' || state === 'usageLimited' || state === 'budgetLimited' ? 'pending' : 'running'
      // Update from the confirmed native readback; never hide a failed deletion.
      const now = new Date().toISOString()
      useAstrorderStore.getState().mergeTasks([{
        ...(goal?.task ?? {
          id: `${result.source}:goal:current`, agent_id: session.agent_id, session_id: session.id,
          kind: 'todo' as const, command: null, target_id: null, logs: [], created_at: now,
        }),
        title: native ? `目标: ${String(native.objective ?? native.title ?? objective.trim())}` : goal?.task.title ?? '目标',
        status,
        progress: { ...native, type: 'goal', source: result.source, native_status: state, blocking: false },
        updated_at: now,
      }])
      // WAL projection may arrive after the HTTP receipt. A concurrent GET
      // would overwrite this confirmed result with the previous projection.
      void client.invalidateQueries({ queryKey: ['astrorder', 'tasks', session.agent_id, session.id], refetchType: 'none' })
      setEditing(false)
      setDeleting(false)
    } catch (reason) {
      setDeleting(false)
      setError(reason instanceof Error ? reason.message : String(reason))
    } finally {
      busy.current = false
      setPending(false)
    }
  }

  if (!goal) return null

  return <>
    <div className={`composer-goal-pill is-${goal.tone}`}>
      <span className="goal-pill-status" role="status" aria-label={`${goal.label}：${goal.title}`}>
        {goal.tone === 'running' || goal.tone === 'waiting'
          ? <span className={`goal-status-dot is-${goal.tone}`} aria-hidden="true" />
          : goal.tone === 'completed' ? <IconCheck size={13} aria-hidden="true" /> : <IconX size={13} aria-hidden="true" />}
        <span className="goal-status-text">{goal.label}</span>
      </span>
      <span className="goal-pill-title" title={goal.title}>{goal.title}</span>
      {supported && <Group gap={2} wrap="nowrap" style={{ flexShrink: 0 }}>
        {(pausable || paused) && <Button size="compact-xs" variant="subtle" aria-label={paused ? '恢复目标' : '暂停目标'} leftSection={paused ? <IconPlayerPlay size={13} /> : <IconPlayerPause size={13} />} disabled={disabled || pending || (paused && !resumable)} title={paused && hermes && active ? '当前回合结束后可恢复目标' : paused && hermes ? '保留目标和完成条件；Hermes 会重置已用轮次并继续执行' : paused ? '保留目标和进度，继续执行' : '暂停后续自动推进，当前回合继续执行'} onClick={() => void mutate(false, paused ? 'active' : 'paused')}>{paused ? '恢复' : '暂停'}</Button>}
        <Button size="compact-xs" variant="subtle" aria-label="修改目标" leftSection={<IconPencil size={13} />} disabled={disabled || pending || (hermes && active)} title={hermes && active ? '请先停止当前执行，再修改目标' : '修改目标'} onClick={() => { setObjective(goal.title); setError(null); setEditing(true) }}>修改</Button>
        <Button size="compact-xs" variant="subtle" color="red" aria-label="删除目标" leftSection={<IconTrash size={13} />} disabled={disabled || pending} onClick={event => { setDeleteCoords(event.detail ? { x: event.clientX, y: event.clientY } : undefined); setError(null); setDeleting(true) }}>删除</Button>
      </Group>}
    </div>
    {hermes && paused && <Text size="xs" c="dimmed">恢复会保留目标和完成条件，并按 Hermes 原生行为重置已用轮次。</Text>}
    {error && !editing && <Alert color="red" role="alert" mb="xs">{error}</Alert>}
    <ConfirmPopover opened={deleting} coords={deleteCoords} title="删除目标？" message="将清除 Agent 的原生目标和后续自动推进，保留聊天记录；当前正在执行的工具不会因此中断。" confirmLabel="删除目标" loading={pending} onCancel={() => { if (!pending) setDeleting(false) }} onConfirm={() => mutate(true)} />
    <Modal opened={editing} onClose={() => { if (!pending) setEditing(false) }} title="修改目标" centered closeOnClickOutside={!pending} closeOnEscape={!pending} withCloseButton={!pending}>
      <Stack>
        <Textarea label="目标内容" value={objective} onChange={event => setObjective(event.currentTarget.value)} autosize minRows={3} maxRows={12} disabled={disabled || pending} />
        <Text size="sm" c="dimmed">{hermes ? 'Hermes 会重新设置目标、重置轮次预算并立即开始执行。原目标的完成条件、子目标及检查门禁也会重置；需要保留的完成条件请写入目标内容。' : '保留当前目标状态及 token 预算，仅更新目标内容。'}</Text>
        {error && <Alert color="red" role="alert">{error}</Alert>}
        <Group justify="flex-end">
          <Button variant="default" disabled={pending} onClick={() => setEditing(false)}>取消</Button>
          <Button loading={pending} disabled={disabled || !objective.trim() || (hermes && active)} onClick={() => void mutate(false)}>保存目标</Button>
        </Group>
      </Stack>
    </Modal>
  </>
}
