import { useQuery } from '@tanstack/react-query'
import type { Agent, Session } from '../domain/types'
import { api } from '../api/client'
import { useSessionModel } from '../hooks/useSessionModel'
import { useAstrorderStore } from '../state/store'
import { AgentBrandIcon, agentKindLabel } from './AgentBrandIcon'
import { notifications } from '@mantine/notifications'
import './sessionRuntimeFacts.css'

export function AgentKindBadge({ agent, iconOnly = false }: { agent?: Agent; iconOnly?: boolean }) {
  const label = agentKindLabel(agent?.kind)
  return <span className="agent-kind-badge" data-agent-kind={agent?.kind || 'unknown'} title={agent?.name} aria-label={iconOnly ? label : undefined}><AgentBrandIcon kind={agent?.kind} size={13} />{!iconOnly && label}</span>
}

function formatTokenCount(num: number): string {
  if (!num || num <= 0) return '0'
  if (num >= 1000000) return (num / 1000000).toFixed(2) + 'M'
  if (num >= 1000) return (num / 1000).toFixed(1) + 'k'
  return String(num)
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

  const copyText = (textToCopy: string, title: string) => {
    void navigator.clipboard.writeText(textToCopy)
    notifications.show({ color: 'teal', message: `已复制${title}: ${textToCopy}` })
  }

  const astrorderId = `${session.agent_id}::${session.id}`

  return <dl className="session-runtime-facts" aria-label="运行信息">
    <dt>连接</dt><dd>{sourceName}<small className="inline-state">{connected ? '已连接' : sourceState === 'connecting' ? '连接中' : sourceState === 'error' ? '连接异常' : '未连接'}</small></dd>
    <dt>Agent</dt><dd><AgentKindBadge agent={agent} /><small className="inline-state">{agent?.name || session.agent_id}</small></dd>
    <dt>模型</dt><dd>{model.label}</dd>
    <dt>分支</dt><dd>{branch === undefined ? model.isFetching ? '读取中…' : '原生未返回' : branch || '非 Git 工作区'}</dd>
    <dt>工作区</dt><dd>{session.workspace || '原生未返回'}</dd>
    <dt>状态</dt><dd>{{ idle: '空闲', running: '运行中', waiting_approval: '等待审批', error: '异常' }[session.status]}</dd>
    <dt>事件流</dt><dd>{events === 'connected' ? '实时同步' : '连接恢复中'}</dd>
    <dt>原生 ID</dt><dd onClick={() => copyText(session.id, '原生 ID')} style={{ cursor: 'pointer' }} title="点击复制原生 ID"><span className="clickable-id">{session.id}</span></dd>
    <dt>星序 ID</dt><dd onClick={() => copyText(astrorderId, '星序 ID')} style={{ cursor: 'pointer' }} title="点击复制星序 ID"><span className="clickable-id nowrap-id">{astrorderId}</span></dd>
    <dt>上下文</dt><dd>{usage.data ? `${(usage.data.last_input_tokens / 1000).toFixed(1)}k / ${(usage.data.context_window / 1000).toFixed(0)}k (${usage.data.used_percentage}%)` : '统计中…'} {usage.data && <small>缓存命中率 {usage.data.cache_hit_rate}% · 累计产生 {formatTokenCount(usage.data.total_tokens)} tokens</small>}</dd>
  </dl>
}
