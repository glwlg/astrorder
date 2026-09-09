import { useQuery } from '@tanstack/react-query'
import { IconRobot, IconTerminal2 } from '@tabler/icons-react'
import type { Agent, Session } from '../domain/types'
import { api } from '../api/client'
import { useSessionModel } from '../hooks/useSessionModel'
import { useAstrorderStore } from '../state/store'
import './sessionRuntimeFacts.css'

export function AgentKindBadge({ agent }: { agent?: Agent }) {
  const label = agent?.kind === 'hermes' ? 'Hermes' : agent?.kind === 'codex' ? 'Codex' : 'Agent 未识别'
  return <span className="agent-kind-badge" data-agent-kind={agent?.kind || 'unknown'} title={agent?.name}>{agent?.kind === 'codex' ? <IconTerminal2 size={13} /> : <IconRobot size={13} />}{label}</span>
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
  return <dl className="session-runtime-facts" aria-label="运行信息">
    <dt>连接</dt><dd>{sourceName}<small>{connected ? '已连接' : sourceState === 'connecting' ? '连接中' : sourceState === 'error' ? '连接异常' : '未连接'}</small></dd>
    <dt>Agent</dt><dd><AgentKindBadge agent={agent} /><small>{agent?.name || session.agent_id}</small></dd>
    <dt>模型</dt><dd>{model.label}</dd>
    <dt>分支</dt><dd>{branch === undefined ? model.isFetching ? '读取中…' : '原生未返回' : branch || '非 Git 工作区'}</dd>
    <dt>工作区</dt><dd>{session.workspace || '原生未返回'}</dd>
    <dt>状态</dt><dd>{{ idle: '空闲', running: '运行中', waiting_approval: '等待审批', error: '异常' }[session.status]}</dd>
    <dt>事件流</dt><dd>{events === 'connected' ? '实时同步' : '连接恢复中'}</dd>
    <dt>原生 ID</dt><dd>{session.id}</dd>
  </dl>
}
