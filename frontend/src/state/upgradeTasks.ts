import { useEffect, useState } from 'react'
import { notifications } from '@mantine/notifications'
import { api, type UpgradeJob } from '../api/client'
import { useBackgroundTasks } from './backgroundTasks'

const POLL_INTERVAL_MS = 1200
const FOCUS_RECHECK_MS = 30_000
const UPGRADE_CHANNEL_NAME = 'astrorder:upgrade-jobs'

type RecoveryListener = (error: string) => void

let pollTimer: ReturnType<typeof setTimeout> | undefined
let inFlight = false
let lastCheckedAt = 0
let lastError = ''
let recoverySubscribers = 0
const errorListeners = new Set<RecoveryListener>()
let broadcastChannel: BroadcastChannel | null = null

function hasRunningUpgradeTask(): boolean {
  return Object.values(useBackgroundTasks.getState().tasks).some(
    (task) => Boolean(task.upgrade) && task.status === 'running',
  )
}

function clearPollTimer() {
  if (pollTimer !== undefined) {
    clearTimeout(pollTimer)
    pollTimer = undefined
  }
}

function setRecoveryError(message: string) {
  lastError = message
  for (const listener of errorListeners) listener(message)
}

function getBroadcastChannel(): BroadcastChannel | null {
  if (typeof BroadcastChannel === 'undefined') return null
  if (!broadcastChannel) {
    try {
      broadcastChannel = new BroadcastChannel(UPGRADE_CHANNEL_NAME)
      broadcastChannel.onmessage = (event) => {
        const reason = (event.data as { reason?: string } | null)?.reason
        if ((reason === 'started' && recoverySubscribers > 0) || hasRunningUpgradeTask()) {
          void refreshUpgradeJobs()
        }
      }
    } catch {
      return null
    }
  }
  return broadcastChannel
}

function notifyOtherWindows(reason: 'started' | 'cancelled' | 'finished') {
  try {
    getBroadcastChannel()?.postMessage({ reason, at: Date.now() })
  } catch {
    // Ignore channel errors in restricted environments.
  }
}

function schedulePoll(delayMs = POLL_INTERVAL_MS) {
  clearPollTimer()
  pollTimer = setTimeout(() => {
    pollTimer = undefined
    if (recoverySubscribers === 0 && !hasRunningUpgradeTask()) return
    void refreshUpgradeJobs()
  }, delayMs)
}

export async function refreshUpgradeJobs(): Promise<void> {
  if (inFlight) return
  inFlight = true
  lastCheckedAt = Date.now()
  let keepPolling = false
  try {
    const result = await api.getUpgradeJobs()
    for (const job of result.items) trackUpgradeJob(job, true)
    keepPolling = result.items.some((job) => job.status === 'running')
    setRecoveryError('')
  } catch (error) {
    keepPolling = recoverySubscribers > 0 || hasRunningUpgradeTask()
    setRecoveryError(error instanceof Error ? error.message : '升级任务状态读取失败')
  } finally {
    inFlight = false
    if (keepPolling && (recoverySubscribers > 0 || hasRunningUpgradeTask())) {
      schedulePoll(POLL_INTERVAL_MS)
    }
  }
}

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
        notifyOtherWindows('cancelled')
      },
    } } }
  })
  if (previous?.status === 'running' && job.status !== 'running') {
    notifications.show({ title: job.title, message: job.detail, color: job.status === 'completed' ? 'teal' : job.status === 'cancelled' ? 'gray' : 'red' })
    notifyOtherWindows('finished')
  }
  if (job.status === 'running') {
    getBroadcastChannel()
    if (!restore) notifyOtherWindows('started')
    if (!inFlight && pollTimer === undefined) {
      schedulePoll(POLL_INTERVAL_MS)
    }
  } else if (recoverySubscribers === 0 && !hasRunningUpgradeTask()) {
    clearPollTimer()
  }
}

export function useUpgradeTaskRecovery() {
  const [error, setError] = useState(lastError)
  useEffect(() => {
    recoverySubscribers += 1
    errorListeners.add(setError)
    getBroadcastChannel()

    const unsubscribe = useBackgroundTasks.subscribe(() => {
      if (hasRunningUpgradeTask() && !inFlight && pollTimer === undefined) {
        schedulePoll(POLL_INTERVAL_MS)
      }
    })

    const onFocus = () => {
      if (inFlight || pollTimer !== undefined) return
      if (Date.now() - lastCheckedAt >= FOCUS_RECHECK_MS) {
        void refreshUpgradeJobs()
      }
    }
    window.addEventListener('focus', onFocus)

    void refreshUpgradeJobs()
    return () => {
      recoverySubscribers = Math.max(0, recoverySubscribers - 1)
      errorListeners.delete(setError)
      unsubscribe()
      window.removeEventListener('focus', onFocus)
      if (recoverySubscribers === 0) {
        lastError = ''
        if (!hasRunningUpgradeTask()) {
          clearPollTimer()
        }
      }
    }
  }, [])
  return error
}
