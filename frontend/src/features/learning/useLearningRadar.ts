import { useQuery } from '@tanstack/react-query'
import {
  learningApi,
  type LearningOverviewResponse,
  type LearningTopicCategory,
  type LearningTopicPriority,
} from '../../api/learning'
import { useUserIdle } from '../../hooks/useUserIdle'

export const LEARNING_OVERVIEW_QUERY_KEY = ['astrorder', 'learning', 'overview'] as const

export const WEEKDAY_LABELS = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'] as const

export const LEITNER_ROUNDS = [
  { stage: 1, days: 1, label: '第 1 轮 · 1 天' },
  { stage: 2, days: 3, label: '第 2 轮 · 3 天' },
  { stage: 3, days: 7, label: '第 3 轮 · 7 天' },
  { stage: 4, days: 21, label: '第 4 轮 · 21 天' },
] as const

export function categoryMeta(category: LearningTopicCategory | string): {
  label: string
  shortLabel: string
  accent: string
  chipClass: string
} {
  switch (category) {
    case 'blackbox_code':
      return { label: 'AI 代写黑盒', shortLabel: '黑盒', accent: '#4f46e5', chipClass: 'is-blackbox' }
    case 'recurring_friction':
      return { label: '高频卡点', shortLabel: '卡点', accent: '#b45309', chipClass: 'is-friction' }
    case 'failure_pattern':
      return { label: '报错模式', shortLabel: '报错', accent: '#c92a2a', chipClass: 'is-failure' }
    case 'new_domain':
      return { label: '新涉足领域', shortLabel: '新域', accent: '#2563eb', chipClass: 'is-domain' }
    default:
      return { label: '知识盲区', shortLabel: '盲区', accent: '#525866', chipClass: 'is-default' }
  }
}

export function priorityMeta(priority: LearningTopicPriority | string): {
  label: string
  dotColor: string
} {
  switch (priority) {
    case 'high':
      return { label: '高优先级', dotColor: '#4f46e5' }
    case 'low':
      return { label: '低优先级', dotColor: '#868e96' }
    default:
      return { label: '中优先级', dotColor: '#b45309' }
  }
}

export function formatShortDate(iso?: string): string {
  if (!iso) return '--'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '--'
  const mm = String(d.getMonth() + 1).padStart(2, '0')
  const dd = String(d.getDate()).padStart(2, '0')
  return `${mm}-${dd}`
}

export function formatShortDateTime(iso?: string): string {
  if (!iso) return '--'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '--'
  const mm = String(d.getMonth() + 1).padStart(2, '0')
  const dd = String(d.getDate()).padStart(2, '0')
  const hh = String(d.getHours()).padStart(2, '0')
  const mi = String(d.getMinutes()).padStart(2, '0')
  return `${mm}-${dd} ${hh}:${mi}`
}

export function formatDaysUntil(iso?: string): string {
  if (!iso) return ''
  const ts = Date.parse(iso)
  if (Number.isNaN(ts)) return ''
  const diffMs = ts - Date.now()
  if (diffMs <= 0) return '已到期'
  const hours = Math.ceil(diffMs / (3600 * 1000))
  if (hours < 24) return `${hours} 小时后`
  const days = Math.ceil(diffMs / (24 * 3600 * 1000))
  return `${days} 天后`
}

export function splitSessionKey(key?: string): { agentId: string; sessionId: string } | null {
  if (!key) return null
  const idx = key.indexOf('::')
  if (idx <= 0) return null
  const agentId = key.slice(0, idx).trim()
  const sessionId = key.slice(idx + 2).trim()
  if (!agentId || !sessionId) return null
  return { agentId, sessionId }
}

export function useLearningOverview(enabled = true) {
  const idle = useUserIdle()
  return useQuery<LearningOverviewResponse>({
    queryKey: LEARNING_OVERVIEW_QUERY_KEY,
    queryFn: ({ signal }) => learningApi.getOverview(signal),
    enabled,
    retry: false,
    staleTime: 10_000,
    refetchIntervalInBackground: false,
    refetchInterval: idle
      ? false
      : (query) => {
          if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return false
          if (query.state.data?.active_run?.status === 'running') return 6_000
          return 90_000
        },
  })
}
