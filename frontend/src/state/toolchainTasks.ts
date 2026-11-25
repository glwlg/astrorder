import { notifications } from '@mantine/notifications'
import { api, type ToolchainInstallJob } from '../api/client'
import { useBackgroundTasks } from './backgroundTasks'

const activePollers = new Map<string, ReturnType<typeof setTimeout>>()

export function trackToolchainInstallJob(
  job: ToolchainInstallJob,
  onDone?: (finished: ToolchainInstallJob) => void,
) {
  const previous = useBackgroundTasks.getState().tasks[job.id]
  useBackgroundTasks.setState((state) => ({
    tasks: {
      ...state.tasks,
      [job.id]: {
        id: job.id,
        title: job.title,
        detail: job.detail,
        status: job.status,
        createdAt: Date.parse(job.created_at) || Date.now(),
        log: job.log,
        actionLabel: '查看日志',
        upgrade: { current: job.current, total: job.total, target: job.target },
        cancel: async () => {
          await api.cancelToolchainInstallJob(job.id)
        },
      },
    },
  }))

  if (previous?.status === 'running' && job.status !== 'running') {
    notifications.show({
      title: job.title,
      message: job.detail,
      color: job.status === 'completed' ? 'teal' : job.status === 'cancelled' ? 'gray' : 'red',
    })
  }

  if (job.status !== 'running') {
    const existing = activePollers.get(job.id)
    if (existing !== undefined) {
      clearTimeout(existing)
      activePollers.delete(job.id)
    }
    onDone?.(job)
    return
  }

  if (!activePollers.has(job.id)) {
    const poll = () => {
      const timer = setTimeout(async () => {
        activePollers.delete(job.id)
        try {
          const latest = await api.getToolchainInstallJob(job.id)
          trackToolchainInstallJob(latest, onDone)
        } catch {
          // Retry briefly on transient network hiccup
          if (useBackgroundTasks.getState().tasks[job.id]?.status === 'running') {
            poll()
          }
        }
      }, 1000)
      activePollers.set(job.id, timer)
    }
    poll()
  }
}
