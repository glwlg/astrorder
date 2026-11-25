import { useQuery } from '@tanstack/react-query'
import type { Agent, Session } from '../domain/types'
import { api, type OcxModelQuotaAccount, type OcxModelQuotaReport, type OcxQuotaWindow } from '../api/client'
import { useGitStatus } from '../hooks/useGitStatus'
import { useSessionModel } from '../hooks/useSessionModel'
import { useUserIdle } from '../hooks/useUserIdle'
import { useAstrorderStore } from '../state/store'
import { AgentBrandIcon, agentKindLabel } from './AgentBrandIcon'
import { sessionActivityStatus } from './sessionRailModel'
import { formatTokens } from '../features/analytics/usageFormat'
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
type QuotaWindowKind = 'short' | 'weekly' | 'monthly'

function QuotaProgress({ item }: { item: QuotaWindow }) {
  const value = Math.max(0, Math.min(100, item.percent))
  return <div className="quota-progress">
    <div className="quota-progress-head"><span>{item.label}</span><b>已用 {Math.round(value)}%</b></div>
    <Progress value={value} size={5} radius="xl" color={value >= 90 ? 'red' : value >= 70 ? 'yellow' : 'teal'} />
    {item.resetAt && <small>{resetText(item.resetAt)}</small>}
  </div>
}

function windowKind(label: string): QuotaWindowKind | null {
  switch (label.toLowerCase().replace(/\s+/g, '')) {
    case '5h': case '5hour': case '5hours': case '5小时': return 'short'
    case '7d': case '7day': case '7days': case 'week': case 'weekly': case '每周': return 'weekly'
    case '30d': case '30day': case '30days': case 'month': case 'monthly': case '30天': return 'monthly'
    default: return null
  }
}

function mergeWindows(standard: Array<(QuotaWindow & { kind: QuotaWindowKind }) | null>, custom: OcxQuotaWindow[] = []): QuotaWindow[] {
  const customWindows = custom.filter(item => Number.isFinite(item.percent))
  const customKinds = new Set(customWindows.map(item => windowKind(item.label)).filter((kind): kind is QuotaWindowKind => kind !== null))
  const result: QuotaWindow[] = standard.filter((item): item is QuotaWindow & { kind: QuotaWindowKind } => item !== null && !customKinds.has(item.kind))
  for (const item of customWindows) {
    const kind = windowKind(item.label)
    result.push({
      label: kind === 'short' ? '5 小时' : kind === 'weekly' ? '每周' : kind === 'monthly' ? '30 天' : item.label,
      percent: item.percent,
      resetAt: item.resetAt,
    })
  }
  return result
}

function reportWindows(report: OcxModelQuotaReport): QuotaWindow[] {
  const quota = report.quota
  if (!quota) return []
  return mergeWindows([
    quota.fiveHourPercent === undefined ? null : { kind: 'short', label: '5 小时', percent: quota.fiveHourPercent, resetAt: quota.fiveHourResetAt },
    quota.weeklyPercent === undefined ? null : { kind: 'weekly', label: '每周', percent: quota.weeklyPercent, resetAt: quota.weeklyResetAt },
    quota.monthlyPercent === undefined ? null : { kind: 'monthly', label: '30 天', percent: quota.monthlyPercent, resetAt: quota.monthlyResetAt },
  ], quota.customWindows)
}

function accountWindows(account: OcxModelQuotaAccount): QuotaWindow[] {
  const quota = account.quota
  if (!quota) return []
  return mergeWindows([
    quota.shortPercent === undefined ? null : { kind: 'short', label: '5 小时', percent: quota.shortPercent, resetAt: quota.shortResetAt },
    quota.weeklyPercent === undefined ? null : { kind: 'weekly', label: '每周', percent: quota.weeklyPercent, resetAt: quota.weeklyResetAt },
  ], quota.customWindows)
}

function AccountQuotaGroup({ account }: { account: OcxModelQuotaAccount }) {
  const windows = accountWindows(account)
  const recordedAt = account.asOf || account.readAt
  const readAt = recordedAt ? new Date(recordedAt) : null
  return <div className="quota-group">
    <strong>
      {account.email || account.logLabel || '账号'}{account.plan ? ` · ${account.plan}` : ''}
      {account.last && <small className="inline-state">网关最近使用</small>}
    </strong>
    {windows.map((item, index) => <QuotaProgress item={item} key={`${item.label}:${index}`} />)}
    {account.error ? <small>额度读取失败</small> : account.readDeferred ? <small>本次未刷新</small> : windows.length === 0 ? <small>暂无额度数据</small> : null}
    {readAt && Number.isFinite(readAt.getTime()) && <small>{account.asOf ? '缓存读数 · ' : '读取于 '}{readAt.toLocaleString('zh-CN', { year: 'numeric', month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</small>}
  </div>
}

export function SessionRuntimeFacts({ session, agent }: { session: Session; agent?: Agent }) {
  const model = useSessionModel(session)
  const events = useAstrorderStore(state => state.connection)
  const connections = useQuery({ queryKey: ['astrorder', 'connections'], queryFn: api.getConnections, retry: false, staleTime: 10000 })
  const connectionId = session.connection_id || agent?.connection_id
  const remote = connections.data?.ssh?.items?.find(item => item.id === connectionId)
  const local = connections.data?.local
  const sourceName = remote?.display_name || (connectionId ? `SSH · ${connectionId}` : local?.agent_id === session.agent_id ? `本机 · ${local.profile_name || agent?.profile_name || 'default'}` : agent?.name || session.agent_id)
  const sourceState = connectionId && agent?.kind && agent.kind !== 'hermes'
    ? agent.status
    : remote?.state || (local?.agent_id === session.agent_id ? local.state : agent?.status)
  const connected = sourceState === 'connected' || sourceState === 'ready'
  const git = useGitStatus(session)
  const userIdle = useUserIdle()
  const branch = session.workspace
    ? git.isPending ? '读取中…' : git.isError ? '分支不可用' : git.data?.branch || '分支未知'
    : model.data?.branch === undefined ? model.isFetching ? '读取中…' : '原生未返回' : model.data.branch || '非 Git 工作区'
  const usage = useQuery({ queryKey: ['astrorder', 'session_usage', session.id, session.agent_id], queryFn: () => api.getSessionUsage(session.id, session.agent_id), retry: false, staleTime: 5000, refetchIntervalInBackground: false, refetchInterval: !userIdle && session.status === 'running' ? 3000 : false })
  const modelRoute = model.data?.model ? `${model.data.provider ? `${model.data.provider}/` : ''}${model.data.model}` : ''
  const quota = useQuery({ queryKey: ['astrorder', 'model-quota', modelRoute, session.agent_id], queryFn: () => api.getModelQuota(modelRoute, session.agent_id), enabled: Boolean(modelRoute), retry: false, staleTime: 60000, refetchIntervalInBackground: false, refetchInterval: !userIdle && session.status === 'running' ? 60000 : false })
  const reports = quota.data?.reports || []
  const accounts = (quota.data?.accounts || []).filter(account => !account.paused)

  const copyText = async (textToCopy: string, title: string) => {
    try {
      await navigator.clipboard.writeText(textToCopy)
      notifications.show({ color: 'teal', message: `已复制${title}` })
    } catch {
      notifications.show({ color: 'red', message: `无法复制${title}，请检查剪贴板权限。` })
    }
  }

  const astrorderId = `${session.agent_id}::${session.id}`

  return <dl className="session-runtime-facts" aria-label="运行信息">
    <dt>连接</dt><dd>{sourceName}<small className="inline-state">{connected ? '已连接' : sourceState === 'connecting' ? '连接中' : sourceState === 'error' ? '连接异常' : '未连接'}</small></dd>
    <dt>Agent</dt><dd><AgentKindBadge agent={agent} /><small className="inline-state">{agent?.name || session.agent_id}</small></dd>
    <dt>模型</dt><dd>{model.label}</dd>
    <dt>额度</dt><dd>
      {quota.isFetching && !quota.data ? '读取中…' : reports.length || accounts.length
        ? <div className="quota-groups">
          {reports.map(report => <div className="quota-group" key={report.provider}>
            <strong>{report.label}</strong>
            {reportWindows(report).map((item, index) => <QuotaProgress item={item} key={`${item.label}:${index}`} />)}
          </div>)}
          {accounts.map(account => <AccountQuotaGroup account={account} key={account.id} />)}
        </div>
        : quota.isError ? '额度读取失败' : '暂无额度数据'}
    </dd>
    <dt>分支</dt><dd>{branch}</dd>
    <dt>工作区</dt><dd>{session.workspace || '原生未返回'}</dd>
    <dt>状态</dt><dd>{{ idle: '空闲', running: '运行中', waiting_approval: '等待审批', error: '异常' }[sessionActivityStatus(session)]}</dd>
    <dt>事件流</dt><dd>{events === 'connected' ? '实时同步' : '连接恢复中'}</dd>
    <dt>原生 ID</dt><dd onClick={() => copyText(session.id, '原生 ID')} style={{ cursor: 'pointer' }} title="点击复制原生 ID"><span className="clickable-id">{session.id}</span></dd>
    <dt>星序 ID</dt><dd onClick={() => copyText(astrorderId, '星序 ID')} style={{ cursor: 'pointer' }} title="点击复制星序 ID"><span className="clickable-id nowrap-id">{astrorderId}</span></dd>
    <dt>上下文</dt><dd>{usage.data ? (
      usage.data.context_available === false ? '未取得' : usage.data.context_window && usage.data.context_window > 0
        ? `${(usage.data.last_input_tokens / 1000).toFixed(1)}k / ${(usage.data.context_window / 1000).toFixed(0)}k (${usage.data.used_percentage.toFixed(1)}%)`
        : `${(usage.data.last_input_tokens / 1000).toFixed(1)}k`
    ) : '统计中…'} {usage.data?.source === 'native_last_request' && <small>上次请求输入用量</small>} {usage.data?.total_tokens != null && <small>缓存命中率 {usage.data.cache_hit_rate.toFixed(1)}% · 累计产生 {formatTokens(usage.data.total_tokens)} Token{usage.data.speed ? ` · 速度 ${usage.data.speed} tok/s` : ''}</small>}</dd>
  </dl>
}
