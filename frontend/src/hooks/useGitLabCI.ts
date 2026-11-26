import { useQuery } from '@tanstack/react-query'
import { gitlabApi, type GitLabPipelinesResponse } from '../api/gitlab'
import type { Session } from '../domain/types'
import { useUserIdle } from './useUserIdle'

const ACTIVE_PIPELINE_STATUSES = new Set([
  'running',
  'pending',
  'created',
  'preparing',
  'waiting_for_resource',
  'scheduled',
])

export function isActiveGitLabStatus(status?: string | null): boolean {
  if (!status) return false
  return ACTIVE_PIPELINE_STATUSES.has(status.trim().toLowerCase())
}

export interface GitLabStatusMeta {
  label: string
  dotColor: string
  tone: 'success' | 'running' | 'pending' | 'failed' | 'manual' | 'muted'
  spinning: boolean
}

export function gitLabStatusMeta(status?: string | null): GitLabStatusMeta {
  switch ((status || '').trim().toLowerCase()) {
    case 'success':
      return { label: '通过', dotColor: '#2f9e44', tone: 'success', spinning: false }
    case 'running':
      return { label: '运行中', dotColor: '#228be6', tone: 'running', spinning: true }
    case 'pending':
    case 'created':
    case 'preparing':
    case 'waiting_for_resource':
    case 'scheduled':
      return { label: '排队中', dotColor: '#d97706', tone: 'pending', spinning: true }
    case 'failed':
      return { label: '失败', dotColor: '#e03131', tone: 'failed', spinning: false }
    case 'canceled':
    case 'canceling':
      return { label: '已取消', dotColor: '#868e96', tone: 'muted', spinning: false }
    case 'manual':
      return { label: '待手动', dotColor: '#5c7cfa', tone: 'manual', spinning: false }
    case 'skipped':
      return { label: '已跳过', dotColor: '#adb5bd', tone: 'muted', spinning: false }
    default:
      return { label: status || '未知', dotColor: '#868e96', tone: 'muted', spinning: false }
  }
}

export function formatPipelineDuration(seconds?: number): string {
  if (!seconds || seconds <= 0) return '--'
  const rounded = Math.round(seconds)
  if (rounded < 60) return `${rounded}s`
  const mins = Math.floor(rounded / 60)
  const rem = rounded % 60
  if (mins < 60) return rem > 0 ? `${mins}m ${rem}s` : `${mins}m`
  const hours = Math.floor(mins / 60)
  const remMins = mins % 60
  return remMins > 0 ? `${hours}h ${remMins}m` : `${hours}h`
}

export function formatRelativeTime(iso?: string): string {
  if (!iso) return ''
  const ts = Date.parse(iso)
  if (Number.isNaN(ts)) return ''
  const diffSec = Math.max(0, Math.floor((Date.now() - ts) / 1000))
  if (diffSec < 60) return '刚刚'
  const diffMin = Math.floor(diffSec / 60)
  if (diffMin < 60) return `${diffMin} 分钟前`
  const diffHour = Math.floor(diffMin / 60)
  if (diffHour < 24) return `${diffHour} 小时前`
  const diffDay = Math.floor(diffHour / 24)
  if (diffDay < 30) return `${diffDay} 天前`
  return new Date(ts).toLocaleDateString()
}

export function useGitLabPipelines(
  session: Pick<Session, 'id' | 'agent_id' | 'workspace' | 'connection_id'>,
  options?: { enabled?: boolean; limit?: number; ref?: string },
) {
  const idle = useUserIdle()
  const enabled = Boolean(session.workspace) && (options?.enabled ?? true)
  const limit = options?.limit ?? 8
  const ref = options?.ref ?? ''

  return useQuery<GitLabPipelinesResponse>({
    queryKey: ['astrorder', 'gitlab-pipelines', session.agent_id, session.id, session.workspace, session.connection_id || '', ref, limit],
    queryFn: ({ signal }) =>
      gitlabApi.listPipelines(
        {
          workspace: session.workspace || '',
          session_id: session.id,
          connection_id: session.connection_id || undefined,
          ref: ref || undefined,
          limit,
        },
        signal,
      ),
    enabled,
    retry: false,
    staleTime: 8_000,
    refetchIntervalInBackground: false,
    refetchInterval: idle
      ? false
      : (query) => {
          if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return false
          const data = query.state.data
          if (!data) return 60_000
          if (!data.configured) return 120_000
          if (!data.matched) return 120_000
          if (isActiveGitLabStatus(data.latest_pipeline?.status)) return 10_000
          return 45_000
        },
  })
}
