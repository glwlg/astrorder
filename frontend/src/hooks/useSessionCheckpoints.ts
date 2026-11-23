import { useQuery } from '@tanstack/react-query'
import { api, type SessionCheckpoint } from '../api/client'
import type { Session } from '../domain/types'

export function checkpointsQueryKey(agentId: string, sessionId: string) {
  return ['astrorder', 'checkpoints', agentId, sessionId] as const
}

export function useSessionCheckpoints(session: Session | null | undefined, enabled = true) {
  const agentId = session?.agent_id ?? ''
  const sessionId = session?.id ?? ''
  const hasWorkspace = Boolean(session?.workspace?.trim())
  const query = useQuery<{ items: SessionCheckpoint[] }>({
    queryKey: checkpointsQueryKey(agentId, sessionId),
    queryFn: () => api.listCheckpoints(sessionId, agentId),
    enabled: Boolean(enabled && agentId && sessionId && hasWorkspace),
    staleTime: 15_000,
    retry: false,
  })
  return {
    ...query,
    items: query.data?.items ?? [],
    hasWorkspace,
  }
}
