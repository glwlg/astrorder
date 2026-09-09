import { useEffect, useMemo } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { api } from '../api/client'
import { migrateSessionPins } from '../domain/migrateSessionPins'
import type { Message, Session } from '../domain/types'
import { selectMessages, useAstrorderStore } from '../state/store'
import { visibleTranscript } from '../domain/visibleTranscript'

export function mergeHistoryPages(
  agentId: string,
  sessionId: string,
  pages: Array<{ items: Message[] }>,
): void {
  const store = useAstrorderStore.getState()
  const [latest, ...older] = pages
  if (latest) store.mergeMessages(agentId, sessionId, latest.items)
  for (const page of older) {
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
    queryFn: async () => {
      const payload = await api.getBootstrap()
      try {
        await migrateSessionPins(payload.sessions, localStorage)
      } catch {
        // Unavailable storage must not block native session loading.
      }
      return payload
    },
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
  const agentId = session?.agent_id
  const sessionId = session?.id
  const view = useMemo(() => ({
    id: crypto.randomUUID(),
    baseline: new Set(agentId && sessionId ? selectMessages(useAstrorderStore.getState(), agentId, sessionId).map(m => m.id) : []),
  }), [agentId, sessionId])
  const stored = useAstrorderStore(useShallow(state => agentId && sessionId ? selectMessages(state, agentId, sessionId) : []))
  const messages = useInfiniteQuery({
    queryKey: ['astrorder', 'messages', session?.agent_id, session?.id, view.id],
    queryFn: ({ pageParam }) => api.getMessages(session!.id, session!.agent_id, pageParam, pageParam ? 20 : 2),
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
  const tasks = useQuery({
    queryKey: ['astrorder', 'tasks', session?.agent_id, session?.id],
    queryFn: () => api.getTasks(session!.id, session!.agent_id),
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
  useEffect(() => {
    if (tasks.data) useAstrorderStore.getState().mergeTasks(tasks.data.items)
  }, [tasks.data])
  const visibleMessages = messages.data
    ? visibleTranscript(stored, messages.data.pages.flatMap(page => page.items), view.baseline)
    : stored.slice(-2)
  return { messages, commands, tasks, visibleMessages }
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

export function useConnections(authenticated: boolean) {
  return useQuery({
    queryKey: ['astrorder', 'connections'],
    queryFn: api.getConnections,
    enabled: authenticated,
    retry: false,
    staleTime: 2_000,
  })
}

export function useConnectionHistory(connectionId: string | null, authenticated: boolean) {
  return useInfiniteQuery({
    queryKey: ['astrorder', 'connection-history', connectionId],
    queryFn: ({ pageParam }) => api.getConnectionHistory(connectionId!, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.next_cursor || undefined,
    enabled: authenticated && Boolean(connectionId),
    retry: false,
    staleTime: 5_000,
  })
}
