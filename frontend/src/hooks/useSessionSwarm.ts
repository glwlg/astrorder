import { useQuery } from '@tanstack/react-query'
import { api } from '../api/client'
import type { Agent, Session } from '../domain/types'

export function isAntigravityAgent(agent?: Pick<Agent, 'kind'> | null): boolean {
  return agent?.kind === 'antigravity' || agent?.kind === 'antigravity-ssh'
}

export function useSessionSwarm(
  session: Session | null | undefined,
  agent: Pick<Agent, 'kind'> | null | undefined,
  panelOpen = false,
) {
  const supported = Boolean(session && isAntigravityAgent(agent))
  return useQuery({
    queryKey: ['astrorder', 'session-swarm', session?.agent_id, session?.id],
    queryFn: () => api.getSessionSwarm(session!.id, session!.agent_id),
    enabled: supported,
    staleTime: 4000,
    retry: false,
    refetchOnWindowFocus: false,
    refetchIntervalInBackground: false,
    refetchInterval: (query) => {
      if (!supported) return false
      if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return false
      const data = query.state.data
      if (panelOpen || data?.active) return 5000
      if (session?.status === 'running') return 30000
      return false
    },
  })
}

export function useSessionSwarmMemberPreview(
  session: Session | null | undefined,
  agent: Pick<Agent, 'kind'> | null | undefined,
  memberConversationId: string | null | undefined,
  enabled = true,
) {
  const active = Boolean(enabled && session && isAntigravityAgent(agent) && memberConversationId)
  return useQuery({
    queryKey: ['astrorder', 'session-swarm-member', session?.agent_id, session?.id, memberConversationId],
    queryFn: () =>
      api.getSessionSwarm(session!.id, session!.agent_id, {
        memberConversationId: memberConversationId!,
        memberLimit: 24,
      }),
    enabled: active,
    staleTime: 2000,
    retry: false,
    refetchOnWindowFocus: false,
    refetchInterval: active ? 4000 : false,
  })
}
