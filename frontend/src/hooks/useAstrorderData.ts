import { useEffect } from 'react'
import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { api } from '../api/client'
import type { Message, Session } from '../domain/types'
import { useAstrorderStore } from '../state/store'

export function mergeHistoryPages(
  agentId: string,
  sessionId: string,
  pages: Array<{ items: Message[] }>,
): void {
  const store = useAstrorderStore.getState()
  const [latest, ...older] = pages
  if (latest) store.mergeMessages(agentId, sessionId, latest.items)
  for (const page of [...older].reverse()) {
    store.mergeMessagesPrepend(agentId, sessionId, page.items)
  }
}

export function useAuthSession() {
  return useQuery({
    queryKey: ['astrorder', 'auth-session'],
    queryFn: api.getAuthSession,
    retry: false,
    staleTime: 0,
  })
}

export function useBootstrap(authenticated: boolean) {
  const query = useQuery({
    queryKey: ['astrorder', 'bootstrap'],
    queryFn: api.getBootstrap,
    enabled: authenticated,
    retry: false,
    staleTime: 10_000,
  })
  useEffect(() => {
    if (query.data) useAstrorderStore.getState().hydrateBootstrap(query.data)
  }, [query.data])
  return query
}

export function useSessionResources(session: Session | null, authenticated: boolean) {
  const messages = useInfiniteQuery({
    queryKey: ['astrorder', 'messages', session?.agent_id, session?.id],
    queryFn: ({ pageParam }) => api.getMessages(session!.id, session!.agent_id, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.next_cursor || undefined,
    enabled: authenticated && Boolean(session),
    retry: false,
    staleTime: 5_000,
  })
  const commands = useQuery({
    queryKey: ['astrorder', 'commands', session?.agent_id, session?.id],
    queryFn: () => api.getCommands(session!.id, session!.agent_id),
    enabled: authenticated && Boolean(session),
    retry: false,
    staleTime: 5_000,
  })
  useEffect(() => {
    if (session && messages.data) {
      mergeHistoryPages(session.agent_id, session.id, messages.data.pages)
    }
  }, [messages.data, session])
  useEffect(() => {
    if (commands.data) useAstrorderStore.getState().mergeCommands(commands.data.items)
  }, [commands.data])
  return { messages, commands }
}

export function useRuntime(authenticated: boolean) {
  return useQuery({
    queryKey: ['astrorder', 'runtime'],
    queryFn: api.getRuntime,
    enabled: authenticated,
    retry: false,
    staleTime: 10_000,
  })
}
