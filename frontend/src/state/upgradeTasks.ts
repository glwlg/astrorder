import { useEffect, useState } from 'react'
import { notifications } from '@mantine/notifications'
import { api, type UpgradeJob } from '../api/client'
import { useBackgroundTasks } from './backgroundTasks'

export function trackUpgradeJob(job: UpgradeJob, restore = false) {
  const previous = useBackgroundTasks.getState().tasks[job.id]
  useBackgroundTasks.setState((state) => {
    // Finished jobs stay dismissed; a refresh always restores active jobs.
    if (restore && job.status !== 'running' && !state.tasks[job.id]) return state
    return { tasks: { ...state.tasks, [job.id]: {
      id: job.id,
      title: job.title,
      detail: job.detail,
      status: job.status,
      createdAt: Date.parse(job.created_at),
      log: job.log,
      actionLabel: '查看日志',
      upgrade: { current: job.current, total: job.total, target: job.target },
      cancel: async () => {
        const result = await api.cancelUpgradeJob(job.id)
        if (!result.cancelled) throw new Error('升级任务已结束，无法取消')
      },
    } } }
  })
  if (previous?.status === 'running' && job.status !== 'running') {
    notifications.show({ title: job.title, message: job.detail, color: job.status === 'completed' ? 'teal' : job.status === 'cancelled' ? 'gray' : 'red' })
  }
}

export function useUpgradeTaskRecovery() {
  const [error, setError] = useState('')
  useEffect(() => {
    let active = true
    let timer: ReturnType<typeof setTimeout>
    const refresh = async () => {
      try {
        const result = await api.getUpgradeJobs()
        if (!active) return
        for (const job of result.items) trackUpgradeJob(job, true)
        setError('')
      } catch (error) {
        if (active) setError(error instanceof Error ? error.message : '升级任务状态读取失败')
      } finally {
        if (active) timer = setTimeout(refresh, 1200)
      }
    }
    void refresh()
    return () => { active = false; clearTimeout(timer) }
  }, [])
  return error
}
