import { useQuery } from '@tanstack/react-query'
import type { Agent, Session } from '../domain/types'
import { api } from '../api/client'
import { useSessionModel } from '../hooks/useSessionModel'
import { useAstrorderStore } from '../state/store'
import { AgentBrandIcon, agentKindLabel } from './AgentBrandIcon'
import { formatTokens } from '../features/analytics/AnalyticsPage'
import { notifications } from '@mantine/notifications'
import { Progress } from '@mantine/core'
import './sessionRuntimeFacts.css'

export function AgentKindBadge({ agent, iconOnly = false }: { agent?: Agent; iconOnly?: boolean }) {
  const label = agentKindLabel(agent?.kind)
  return <span className="agent-kind-badge" data-agent-kind={agent?.kind || 'unknown'} title={agent?.name} aria-label={iconOnly ? label : undefined}><AgentBrandIcon kind={agent?.kind} size={13} />{!iconOnly && label}</span>
}

function resetText(value?: number): string {
  if (!value) return ''
  const date = new Date(value < 10_000_000_000 ? value * 1000 : value)
  return `${date.toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' })} 重置`
}

type QuotaWindow = { label: string; percent: number; resetAt: number | undefined }

function QuotaProgress({ item }: { item: QuotaWindow }) {
  const value = Math.max(0, Math.min(100, item.percent))
  return <div className="quota-progress">
    <div className="quota-progress-head"><span>{item.label}</span><b>{Math.round(value)}%</b></div>
    <Progress value={value} size={5} radius="xl" color={value >= 90 ? 'red' : value >= 70 ? 'yellow' : 'teal'} />
    {item.resetAt && <small>{resetText(item.resetAt)}</small>}
  </div>
}

function reportWindows(report: { quota?: { fiveHourPercent?: number; fiveHourResetAt?: number; weeklyPercent?: number; weeklyResetAt?: number; monthlyPercent?: number; monthlyResetAt?: number; customWindows?: Array<{ label: string; percent: number; resetAt?: number }> } }): QuotaWindow[] {
  const quota = report.quota
  if (!quota) return []
  return [
    quota.fiveHourPercent === undefined ? null : { label: '5 小时', percent: quota.fiveHourPercent, resetAt: quota.fiveHourResetAt },
    quota.weeklyPercent === undefined ? null : { label: '每周', percent: quota.weeklyPercent, resetAt: quota.weeklyResetAt },
    quota.monthlyPercent === undefined ? null : { label: '30 天', percent: quota.monthlyPercent, resetAt: quota.monthlyResetAt },
    ...(quota.customWindows || []).map(item => ({ ...item, resetAt: item.resetAt })),
  ].filter((item): item is QuotaWindow => item !== null)
}

function accountWindows(account: { quota?: { shortPercent?: number; shortResetAt?: number; weeklyPercent?: number; weeklyResetAt?: number } | null }): QuotaWindow[] {
  const quota = account.quota
  if (!quota) return []
  return [
    quota.shortPercent === undefined ? null : { label: '5 小时', percent: quota.shortPercent, resetAt: quota.shortResetAt },
    quota.weeklyPercent === undefined ? null : { label: '每周', percent: quota.weeklyPercent, resetAt: quota.weeklyResetAt },
  ].filter((item): item is QuotaWindow => item !== null)
}

export function SessionRuntimeFacts({ session, agent }: { session: Session; agent?: Agent }) {
  const model = useSessionModel(session)
  const events = useAstrorderStore(state => state.connection)
  const connections = useQuery({ queryKey: ['astrorder', 'connections'], queryFn: api.getConnections, retry: false, staleTime: 10000 })
  const connectionId = session.connection_id || agent?.connection_id
  const remote = connections.data?.ssh?.items?.find(item => item.id === connectionId)
  const local = connections.data?.local
  const sourceName = remote?.display_name || (connectionId ? `SSH · ${connectionId}` : local?.agent_id === session.agent_id ? `本机 · ${local.profile_name || agent?.profile_name || 'default'}` : agent?.name || session.agent_id)
  const sourceState = remote?.state || (local?.agent_id === session.agent_id ? local.state : agent?.status)
  const connected = sourceState === 'connected' || sourceState === 'ready'
  const branch = model.data?.branch
  const usage = useQuery({ queryKey: ['astrorder', 'session_usage', session.id, session.agent_id], queryFn: () => api.getSessionUsage(session.id, session.agent_id), retry: false, staleTime: 5000, refetchInterval: session.status === 'running' ? 3000 : false })
  const modelRoute = model.data?.model ? `${model.data.provider ? `${model.data.provider}/` : ''}${model.data.model}` : ''
  const quota = useQuery({ queryKey: ['astrorder', 'model-quota', modelRoute], queryFn: () => api.getModelQuota(modelRoute), enabled: Boolean(modelRoute), retry: false, staleTime: 60000 })

  const copyText = (textToCopy: string, title: string) => {
    void navigator.clipboard.writeText(textToCopy)
    notifications.show({ color: 'teal', message: `已复制${title}: ${textToCopy}` })
  }

  const astrorderId = `${session.agent_id}::${session.id}`

  return <dl className="session-runtime-facts" aria-label="运行信息">
    <dt>连接</dt><dd>{sourceName}<small className="inline-state">{connected ? '已连接' : sourceState === 'connecting' ? '连接中' : sourceState === 'error' ? '连接异常' : '未连接'}</small></dd>
    <dt>Agent</dt><dd><AgentKindBadge agent={agent} /><small className="inline-state">{agent?.name || session.agent_id}</small></dd>
    <dt>模型</dt><dd>{model.label}</dd>
    <dt>额度</dt><dd>{quota.isFetching ? '读取中…' : quota.data && (quota.data.reports.length || quota.data.accounts.length) ? <div className="quota-groups">{quota.data.reports.map(report => <div className="quota-group" key={report.provider}><strong>{report.label}</strong>{reportWindows(report).map(item => <QuotaProgress item={item} key={item.label} />)}</div>)}{quota.data.accounts.filter(account => !account.paused).map(account => <div className="quota-group" key={account.id}><strong>{account.email || account.logLabel || 'Codex 账号'}{account.plan ? ` · ${account.plan}` : ''}</strong>{accountWindows(account).map(item => <QuotaProgress item={item} key={item.label} />)}</div>)}</div> : '暂无额度数据'}</dd>
    <dt>分支</dt><dd>{branch === undefined ? model.isFetching ? '读取中…' : '原生未返回' : branch || '非 Git 工作区'}</dd>
    <dt>工作区</dt><dd>{session.workspace || '原生未返回'}</dd>
    <dt>状态</dt><dd>{{ idle: '空闲', running: '运行中', waiting_approval: '等待审批', error: '异常' }[session.status]}</dd>
    <dt>事件流</dt><dd>{events === 'connected' ? '实时同步' : '连接恢复中'}</dd>
    <dt>原生 ID</dt><dd onClick={() => copyText(session.id, '原生 ID')} style={{ cursor: 'pointer' }} title="点击复制原生 ID"><span className="clickable-id">{session.id}</span></dd>
    <dt>星序 ID</dt><dd onClick={() => copyText(astrorderId, '星序 ID')} style={{ cursor: 'pointer' }} title="点击复制星序 ID"><span className="clickable-id nowrap-id">{astrorderId}</span></dd>
    <dt>上下文</dt><dd>{usage.data ? `${(usage.data.last_input_tokens / 1000).toFixed(1)}k / ${(usage.data.context_window / 1000).toFixed(0)}k (${usage.data.used_percentage}%)` : '统计中…'} {usage.data && <small>缓存命中率 {usage.data.cache_hit_rate}% · 累计产生 {formatTokens(usage.data.total_tokens)} Token{usage.data.speed ? ` · 速度 ${usage.data.speed} tok/s` : ''}</small>}</dd>
  </dl>
}
