import { useEffect, useMemo, useRef } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { api } from '../api/client'
import { migrateSessionPins } from '../domain/migrateSessionPins'
import { scopeKey } from '../domain/semantics'
import {
  INITIAL_HISTORY_LIMIT,
  OLDER_HISTORY_LIMIT,
  needsUserTurnBackfill,
} from '../domain/sessionHistoryPolicy'
import type { Message, Session } from '../domain/types'
import { selectMessages, useAstrorderStore } from '../state/store'
import { visibleTranscript } from '../domain/visibleTranscript'

const EMPTY_STORED_MESSAGES: Message[] = []

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
    staleTime: 60_000,
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
  const stored = useAstrorderStore(useShallow((state) =>
    agentId && sessionId ? selectMessages(state, agentId, sessionId) : EMPTY_STORED_MESSAGES,
  ))
  const sync = useQuery({
    queryKey: ['astrorder', 'session-sync', session?.agent_id, session?.id],
    queryFn: () => api.syncSession(session!.id, session!.agent_id),
    enabled: authenticated && Boolean(session),
    retry: false,
    staleTime: 0,
  })
  const ready = authenticated && Boolean(session)
  useEffect(() => {
    if (sync.data) {
      useAstrorderStore.setState((state) => ({
        sessions: {
          ...state.sessions,
          [scopeKey(sync.data.agent_id, sync.data.id)]: sync.data,
        },
      }))
    }
  }, [sync.data])

  const messages = useInfiniteQuery({
    queryKey: ['astrorder', 'messages', session?.agent_id, session?.id],
    queryFn: ({ pageParam }) => api.getMessages(
      session!.id,
      session!.agent_id,
      pageParam,
      pageParam ? OLDER_HISTORY_LIMIT : INITIAL_HISTORY_LIMIT,
    ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.next_cursor || undefined,
    enabled: ready,
    retry: false,
    staleTime: 30_000,
    gcTime: 1000 * 60 * 60 * 24,
  })

  const autoFetchedRef = useRef<Record<string, number>>({})
  const fetchNextPageRef = useRef(messages.fetchNextPage)
  fetchNextPageRef.current = messages.fetchNextPage
  const hasNextPage = messages.hasNextPage
  const isFetchingNextPage = messages.isFetchingNextPage
  const pages = messages.data?.pages
  useEffect(() => {
    if (!session || !pages || pages.length === 0 || isFetchingNextPage) return
    const sKey = `${session.agent_id}::${session.id}`
    const fetchedCount = autoFetchedRef.current[sKey] || 0
    const allLoaded = pages.flatMap((page) => page.items)
    if (!needsUserTurnBackfill({ items: allLoaded, hasNextPage, autoFetchedPages: fetchedCount })) return
    autoFetchedRef.current[sKey] = fetchedCount + 1
    void fetchNextPageRef.current()
  }, [session, pages, hasNextPage, isFetchingNextPage])

  const commands = useQuery({
    queryKey: ['astrorder', 'commands', session?.agent_id, session?.id],
    queryFn: () => api.getCommands(session!.id, session!.agent_id),
    enabled: ready,
    retry: false,
    staleTime: 30_000,
    gcTime: 1000 * 60 * 60 * 24,
  })
  const tasks = useQuery({
    queryKey: ['astrorder', 'tasks', session?.agent_id, session?.id],
    queryFn: () => api.getTasks(session!.id, session!.agent_id),
    enabled: ready,
    retry: false,
    staleTime: 30_000,
    gcTime: 1000 * 60 * 60 * 24,
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

  const visibleMessages = useMemo(() => {
    if (!session) return []
    if (messages.data && messages.data.pages.length > 0) {
      const loadedItems = messages.data.pages.flatMap(page => page.items)
      return visibleTranscript(stored, loadedItems, new Set())
    }
    return stored
  }, [session, messages.data, stored])

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
