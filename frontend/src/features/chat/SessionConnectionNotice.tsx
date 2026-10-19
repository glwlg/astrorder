import { useRef, useState } from 'react'
import { Button } from '@mantine/core'
import { IconPlugConnected, IconRefresh } from '@tabler/icons-react'
import { useQueryClient } from '@tanstack/react-query'
import { ApiError, api } from '../../api/client'
import { AgentBrandIcon, agentKindLabel } from '../../components/AgentBrandIcon'
import { InlineNotice } from '../../components/InlineNotice'
import type { Session } from '../../domain/types'
import { useAstrorderStore } from '../../state/store'

export function isAgentDisconnectedError(error?: string) {
  return Boolean(error?.includes('主动断联'))
}

export function SessionConnectionNotice({ session, error, onRetry }: { session: Session; error?: string; onRetry?: () => unknown }) {
  const agent = useAstrorderStore(state => state.agents[session.agent_id])
  const client = useQueryClient()
  const inFlight = useRef(false)
  const [connecting, setConnecting] = useState(false)
  const [failure, setFailure] = useState('')
  const disconnected = agent?.status === 'disconnected' || isAgentDisconnectedError(error)
  const connect = async () => {
    if (!agent || inFlight.current) return
    inFlight.current = true
    setConnecting(true)
    setFailure('')
    const connection = session.connection_id || agent.connection_id || 'local'
    try {
      await api.changeEnvironmentAgent(connection, agent.kind, true)
      const snapshot = await api.getEnvironments()
      const target = snapshot.items.find(item => item.id === connection)?.agents.find(item => item.kind === agent.kind)
      if (target?.state !== 'connected') throw new Error(target?.detail || '连接尚未就绪，请重试。')
      const current = await api.getAgents()
      useAstrorderStore.setState({ agents: Object.fromEntries(current.items.map(item => [item.id, item])) })
      await client.invalidateQueries({ queryKey: ['astrorder'] })
      await onRetry?.()
    } catch (err) {
      setFailure(err instanceof ApiError ? err.detail : err instanceof Error ? err.message : '连接失败，请重试。')
    } finally {
      inFlight.current = false
      setConnecting(false)
    }
  }
  if (!disconnected && !error && !failure) return null
  return <InlineNotice className="session-connection-notice" title={disconnected ? `${agentKindLabel(agent?.kind)} 未连接` : '暂时无法读取会话'} icon={disconnected ? <AgentBrandIcon kind={agent?.kind} size={20} /> : undefined} alert={Boolean(failure) || !disconnected} actions={disconnected && agent
    ? <Button variant="default" size="compact-sm" leftSection={<IconPlugConnected size={14} />} loading={connecting} onClick={() => void connect()}>{connecting ? '连接中…' : '连接'}</Button>
    : onRetry && <Button variant="default" size="compact-sm" leftSection={<IconRefresh size={14} />} onClick={() => void onRetry()}>重试</Button>}>
    {disconnected ? `${session.connection_id && session.connection_id !== 'local' && agent?.name ? agent.name + ' · ' : ''}连接后即可在这里继续。` : error}
    {failure && <div role="alert">{failure}</div>}
  </InlineNotice>
}
