import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type SessionModelBinding } from '../api/client'
import type { Session } from '../domain/types'

export function useSessionModel(session: Session | null) {
  const client = useQueryClient()
  const queryKey = ['astrorder', 'session-model', session?.agent_id, session?.id]
  const query = useQuery({
    queryKey,
    queryFn: () => api.getSessionModel(session!.id, session!.agent_id),
    enabled: Boolean(session),
    retry: false,
    staleTime: 30000,
    refetchOnMount: 'always',
  })
  const binding = query.data
  const label = binding?.model
    ? `${binding.provider ? binding.provider + '/' : ''}${binding.model}${binding.deferred ? ' · 下轮生效' : ''}`
    : query.isFetching ? '读取模型…' : '模型暂不可读'
  const change = async (provider: string, model: string) => {
    if (!session) throw new Error('尚未选择会话')
    await client.cancelQueries({ queryKey, exact: true })
    const result = await api.setSessionModel(session.id, session.agent_id, provider, model)
    await client.cancelQueries({ queryKey, exact: true })
    client.setQueryData<SessionModelBinding>(queryKey, previous => ({ ...previous, ...result }))
    return result
  }
  const changeEffort = async (effort: string) => {
    if (!session) throw new Error('尚未选择会话')
    const result = await api.setSessionReasoning(session.id, session.agent_id, effort)
    client.setQueryData<SessionModelBinding>(queryKey, previous => previous ? { ...previous, effort: result.effort } : previous)
    return result
  }
  return { ...query, label, change, changeEffort, effort: binding?.effort || null }
}
