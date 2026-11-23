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
    let inFlight = false
    let lastCheckedAt = 0
    let timer: ReturnType<typeof setTimeout> | undefined

    const schedule = (delayMs: number) => {
      if (!active) return
      if (timer !== undefined) clearTimeout(timer)
      timer = setTimeout(() => {
        timer = undefined
        void refresh()
      }, delayMs)
    }

    const refresh = async () => {
      if (!active || inFlight) return
      inFlight = true
      lastCheckedAt = Date.now()
      let keepPolling = false
      try {
        const result = await api.getUpgradeJobs()
        if (!active) return
        for (const job of result.items) trackUpgradeJob(job, true)
        keepPolling = result.items.some((job) => job.status === 'running')
        setError('')
      } catch (error) {
        keepPolling = true
        if (active) setError(error instanceof Error ? error.message : '升级任务状态读取失败')
      } finally {
        inFlight = false
        if (active && keepPolling) {
          schedule(1200)
        }
      }
    }

    const unsubscribe = useBackgroundTasks.subscribe((state) => {
      if (!active) return
      const hasRunningUpgrade = Object.values(state.tasks).some(
        (task) => Boolean(task.upgrade) && task.status === 'running',
      )
      if (hasRunningUpgrade && !inFlight && timer === undefined) {
        schedule(1200)
      }
    })

    const onFocus = () => {
      if (!active || inFlight || timer !== undefined) return
      if (Date.now() - lastCheckedAt >= 30_000) {
        void refresh()
      }
    }
    window.addEventListener('focus', onFocus)

    void refresh()
    return () => {
      active = false
      if (timer !== undefined) clearTimeout(timer)
      unsubscribe()
      window.removeEventListener('focus', onFocus)
    }
  }, [])
  return error
}
