import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { notifications } from '@mantine/notifications'
import { IconBolt, IconCheck, IconCopy } from '@tabler/icons-react'
import type { Agent, Session } from '../domain/types'
import { api, type OcxModelQuotaAccount, type OcxModelQuotaReport, type OcxQuotaWindow } from '../api/client'
import { useGitStatus } from '../hooks/useGitStatus'
import { useSessionModel } from '../hooks/useSessionModel'
import { useUserIdle } from '../hooks/useUserIdle'
import { useAstrorderStore } from '../state/store'
import { AgentBrandIcon, agentKindLabel } from './AgentBrandIcon'
import { sessionActivityStatus } from './sessionRailModel'
import { formatTokens } from '../features/analytics/usageFormat'
import './sessionRuntimeFacts.css'

export function AgentKindBadge({ agent, iconOnly = false }: { agent?: Agent; iconOnly?: boolean }) {
  const label = agentKindLabel(agent?.kind)
  return (
    <span
      className="agent-kind-badge"
      data-agent-kind={agent?.kind || 'unknown'}
      title={agent?.name}
      aria-label={iconOnly ? label : undefined}
    >
      <AgentBrandIcon kind={agent?.kind} size={13} />
      {!iconOnly && label}
    </span>
  )
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
  const barTone = value >= 90 ? 'is-danger' : value >= 70 ? 'is-warn' : 'is-teal'
  return (
    <div className="quota-progress">
      <div className="quota-progress-head">
        <span className="quota-progress-head-left">
          <span>{item.label}</span>
          <b>已用 {Math.round(value)}%</b>
        </span>
        {item.resetAt && <span className="quota-progress-reset">{resetText(item.resetAt)}</span>}
      </div>
      <div className="srf-bar-track" role="progressbar" aria-valuenow={Math.round(value)} aria-valuemin={0} aria-valuemax={100} aria-label={`${item.label}配额`}>
        <div className={`srf-bar-fill ${barTone}`} style={{ width: `${value}%` }} />
      </div>
    </div>
  )
}

function windowKind(label: string): QuotaWindowKind | null {
  switch (label.toLowerCase().replace(/\s+/g, '')) {
    case '5h': case '5hour': case '5hours': case '5小时': return 'short'
    case '7d': case '7day': case '7days': case 'week': case 'weekly': case '每周': return 'weekly'
    case '30d': case '30day': case '30days': case 'month': case 'monthly': case '30天': return 'monthly'
    default: return null
  }
}

function mergeWindows(
  standard: Array<(QuotaWindow & { kind: QuotaWindowKind }) | null>,
  custom: OcxQuotaWindow[] = [],
): QuotaWindow[] {
  const customWindows = custom.filter((item) => Number.isFinite(item.percent))
  const customKinds = new Set(
    customWindows
      .map((item) => windowKind(item.label))
      .filter((kind): kind is QuotaWindowKind => kind !== null),
  )
  const result: QuotaWindow[] = standard.filter(
    (item): item is QuotaWindow & { kind: QuotaWindowKind } => item !== null && !customKinds.has(item.kind),
  )
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
  return (
    <div className="quota-group">
      <strong>
        <span>
          {account.email || account.logLabel || '账号'}
          {account.plan ? ` · ${account.plan}` : ''}
        </span>
        {account.last && <small className="inline-state">网关最近使用</small>}
      </strong>
      {windows.map((item, index) => (
        <QuotaProgress item={item} key={`${item.label}:${index}`} />
      ))}
      {account.error ? (
        <small>额度读取失败</small>
      ) : account.readDeferred ? (
        <small>本次未刷新</small>
      ) : windows.length === 0 ? (
        <small>暂无额度数据</small>
      ) : null}
      {readAt && Number.isFinite(readAt.getTime()) && (
        <small>
          {account.asOf ? '缓存读数 · ' : '读取于 '}
          {readAt.toLocaleString('zh-CN', { year: 'numeric', month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' })}
        </small>
      )}
    </div>
  )
}

export function SessionRuntimeFacts({
  session,
  agent,
  hideHeroDuplicates = false,
}: {
  session: Session
  agent?: Agent
  hideHeroDuplicates?: boolean
}) {
  const model = useSessionModel(session)
  const events = useAstrorderStore((state) => state.connection)
  const connections = useQuery({ queryKey: ['astrorder', 'connections'], queryFn: api.getConnections, retry: false, staleTime: 10000 })
  const connectionId = session.connection_id || agent?.connection_id
  const remote = connections.data?.ssh?.items?.find((item) => item.id === connectionId)
  const local = connections.data?.local
  const sourceName =
    remote?.display_name ||
    (connectionId
      ? `SSH · ${connectionId}`
      : local?.agent_id === session.agent_id
        ? `本机 · ${local.profile_name || agent?.profile_name || 'default'}`
        : agent?.name || session.agent_id)
  const sourceState =
    connectionId && agent?.kind && agent.kind !== 'hermes'
      ? agent.status
      : remote?.state || (local?.agent_id === session.agent_id ? local.state : agent?.status)
  const connected = sourceState === 'connected' || sourceState === 'ready'
  const git = useGitStatus(session)
  const userIdle = useUserIdle()
  const branch = session.workspace
    ? git.isPending
      ? '读取中…'
      : git.isError
        ? '分支不可用'
        : git.data?.branch || '分支未知'
    : model.data?.branch === undefined
      ? model.isFetching
        ? '读取中…'
        : '原生未返回'
      : model.data.branch || '非 Git 工作区'

  const usage = useQuery({
    queryKey: ['astrorder', 'session_usage', session.id, session.agent_id],
    queryFn: () => api.getSessionUsage(session.id, session.agent_id),
    retry: false,
    staleTime: 5000,
    refetchIntervalInBackground: false,
    refetchInterval: !userIdle && session.status === 'running' ? 3000 : false,
  })
  const modelRoute = model.data?.model ? `${model.data.provider ? `${model.data.provider}/` : ''}${model.data.model}` : ''
  const quota = useQuery({
    queryKey: ['astrorder', 'model-quota', modelRoute, session.agent_id],
    queryFn: () => api.getModelQuota(modelRoute, session.agent_id),
    enabled: Boolean(modelRoute),
    retry: false,
    staleTime: 60000,
    refetchIntervalInBackground: false,
    refetchInterval: !userIdle && session.status === 'running' ? 60000 : false,
  })
  const reports = quota.data?.reports || []
  const accounts = (quota.data?.accounts || []).filter((account) => !account.paused)

  const [copiedKey, setCopiedKey] = useState<'native' | 'astrorder' | null>(null)
  const copyText = async (textToCopy: string, title: string, key: 'native' | 'astrorder') => {
    try {
      await navigator.clipboard.writeText(textToCopy)
      setCopiedKey(key)
      window.setTimeout(() => setCopiedKey((curr) => (curr === key ? null : curr)), 1600)
      notifications.show({ color: 'teal', message: `已复制${title}` })
    } catch {
      notifications.show({ color: 'red', message: `无法复制${title}，请检查剪贴板权限。` })
    }
  }

  const astrorderId = `${session.agent_id}::${session.id}`
  const statusLabel = { idle: '空闲', running: '运行中', waiting_approval: '等待审批', error: '异常' }[sessionActivityStatus(session)]

  const contextPct =
    usage.data && usage.data.context_available !== false && usage.data.context_window && usage.data.context_window > 0
      ? Math.max(0, Math.min(100, usage.data.used_percentage))
      : null
  const contextText = usage.data
    ? usage.data.context_available === false
      ? '未取得'
      : usage.data.context_window && usage.data.context_window > 0
        ? `${(usage.data.last_input_tokens / 1000).toFixed(1)}k / ${(usage.data.context_window / 1000).toFixed(0)}k (${usage.data.used_percentage.toFixed(1)}%)`
        : `${(usage.data.last_input_tokens / 1000).toFixed(1)}k`
    : '统计中…'
  const contextBarTone = contextPct !== null && contextPct >= 90 ? 'is-danger' : contextPct !== null && contextPct >= 75 ? 'is-warn' : ''
  const connectionStatusText = connected ? '已连接' : sourceState === 'connecting' ? '连接中' : sourceState === 'error' ? '连接异常' : '未连接'
  const connectionDotTone = connected ? 'is-ok' : sourceState === 'connecting' ? 'is-warn' : sourceState === 'error' ? 'is-err' : ''

  return (
    <div className="srf-root" aria-label="运行信息">
      {/* 1. 上下文窗口内嵌卡片 */}
      <section className="srf-context-well" aria-label="上下文窗口">
        <div className="srf-context-head">
          <span className="srf-context-label">上下文窗口</span>
          <span className="srf-context-value">{contextText}</span>
        </div>
        {contextPct !== null && (
          <div className="srf-bar-track" role="progressbar" aria-valuenow={Math.round(contextPct)} aria-valuemin={0} aria-valuemax={100} aria-label="上下文窗口使用率">
            <div className={`srf-bar-fill ${contextBarTone}`} style={{ width: `${contextPct}%` }} />
          </div>
        )}
        {(usage.data?.total_tokens != null || usage.data?.source === 'native_last_request') && (
          <div className="srf-context-foot">
            <span>
              {usage.data?.total_tokens != null ? `缓存命中率 ${usage.data.cache_hit_rate.toFixed(1)}%` : ''}
              {usage.data?.source === 'native_last_request' ? (usage.data?.total_tokens != null ? ' · 上次请求输入' : '上次请求输入用量') : ''}
            </span>
            {usage.data?.total_tokens != null && (
              <span>
                累计产生 {formatTokens(usage.data.total_tokens)} Token
                {usage.data.speed ? ` · ${usage.data.speed} tok/s` : ''}
              </span>
            )}
          </div>
        )}
      </section>

      {/* 2. 模型与额度 */}
      <section className="srf-section" aria-label="模型与额度">
        <div className="srf-section-head">
          <span className="srf-section-title">模型与额度</span>
          <span className="srf-section-badges">
            <AgentKindBadge agent={agent} />
            <span className="srf-env-pill" title={sourceName}>{sourceName}</span>
          </span>
        </div>

        <div className="srf-model-well" title={model.label}>
          <IconBolt size={14} className="srf-model-icon" aria-hidden="true" />
          <span className="srf-model-text">{model.label}</span>
        </div>

        {quota.isFetching && !quota.data ? (
          <div className="srf-quota-empty">额度读取中…</div>
        ) : reports.length || accounts.length ? (
          <div className="quota-groups">
            {reports.map((report) => (
              <div className="quota-group" key={report.provider}>
                <strong>{report.label}</strong>
                {reportWindows(report).map((item, index) => (
                  <QuotaProgress item={item} key={`${item.label}:${index}`} />
                ))}
              </div>
            ))}
            {accounts.map((account) => (
              <AccountQuotaGroup account={account} key={account.id} />
            ))}
          </div>
        ) : (
          <div className="srf-quota-empty">{quota.isError ? '额度读取失败' : '暂无额度数据'}</div>
        )}
      </section>

      {/* 3. 运行与标识极简发丝列表 */}
      <section className="srf-section" aria-label="运行与标识">
        <div className="srf-section-head">
          <span className="srf-section-title">运行与标识</span>
        </div>
        <dl className="srf-kv-list">
          <div className="srf-kv-row">
            <dt>连接</dt>
            <dd>
              <span className={`srf-status-dot ${connectionDotTone}`} aria-hidden="true" />
              <span>{sourceName} · {connectionStatusText}</span>
            </dd>
          </div>
          {!hideHeroDuplicates && (
            <div className="srf-kv-row">
              <dt>分支</dt>
              <dd>{branch}</dd>
            </div>
          )}
          {!hideHeroDuplicates && (
            <div className="srf-kv-row">
              <dt>状态</dt>
              <dd>{statusLabel}</dd>
            </div>
          )}
          <div className="srf-kv-row">
            <dt>事件流</dt>
            <dd>
              <span className={`srf-status-dot ${events === 'connected' ? 'is-ok' : 'is-warn'}`} aria-hidden="true" />
              <span>{events === 'connected' ? '实时同步' : '连接恢复中'}</span>
            </dd>
          </div>
          <div className="srf-kv-row">
            <dt>原生 ID</dt>
            <dd>
              <button
                type="button"
                className="srf-id-btn"
                onClick={() => void copyText(session.id, '原生 ID', 'native')}
                title={`点击复制原生 ID：${session.id}`}
              >
                <span className="srf-id-text">{session.id}</span>
                {copiedKey === 'native' ? (
                  <IconCheck size={13} className="srf-id-icon" aria-hidden="true" />
                ) : (
                  <IconCopy size={13} className="srf-id-icon" aria-hidden="true" />
                )}
              </button>
            </dd>
          </div>
          <div className="srf-kv-row">
            <dt>星序 ID</dt>
            <dd>
              <button
                type="button"
                className="srf-id-btn"
                onClick={() => void copyText(astrorderId, '星序 ID', 'astrorder')}
                title={`点击复制星序 ID：${astrorderId}`}
              >
                <span className="srf-id-text">{astrorderId}</span>
                {copiedKey === 'astrorder' ? (
                  <IconCheck size={13} className="srf-id-icon" aria-hidden="true" />
                ) : (
                  <IconCopy size={13} className="srf-id-icon" aria-hidden="true" />
                )}
              </button>
            </dd>
          </div>
        </dl>
      </section>
    </div>
  )
}
