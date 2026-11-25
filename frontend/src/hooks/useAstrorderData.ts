import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { useInfiniteQuery, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
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
import { loadActivityHistory, refreshActivityHistory, visibleHistoryItems, type HistoryPage } from '../domain/loadActivityHistory'
import { clientPerf } from '../perf/clientPerf'
import { isUserIdle, subscribeUserIdle } from './useUserIdle'

type LoadedHistoryPage = HistoryPage & {
  completeActivityGroup: boolean
  liveRevisionAtRequest: number
}

const EMPTY_STORED_MESSAGES: Message[] = []
const EMPTY_OLDER_PAGES: LoadedHistoryPage[] = []
const olderPagesByClient = new WeakMap<QueryClient, Map<string, LoadedHistoryPage[]>>()

function getOlderPagesMap(client: QueryClient): Map<string, LoadedHistoryPage[]> {
  let map = olderPagesByClient.get(client)
  if (!map) {
    map = new Map()
    olderPagesByClient.set(client, map)
  }
  return map
}

export function mergeHistoryPages(
  agentId: string,
  sessionId: string,
  pages: Array<{ items: Message[]; liveRevisionAtRequest?: number }>,
): void {
  const store = useAstrorderStore.getState()
  const revisions = store.messageEventRevisions[scopeKey(agentId, sessionId)] || {}
  // Preserve newer live events unless native history already contains their
  // entire text and additional output that is still queued on the stream.
  const current = store.messages[scopeKey(agentId, sessionId)] || {}
  const streams = store.messageStreams[scopeKey(agentId, sessionId)] || {}
  const safeItems = (page: typeof pages[number]) => {
    const ahead: Record<string, string> = {}
    const items = page.items.flatMap(item => {
      const live = current[item.id]
      if (live && streams[item.id] && item.role === 'assistant' && live.role === 'assistant' && item.kind === live.kind
        && item.text.length > live.text.length && item.text.startsWith(live.text)) {
        ahead[item.id] = item.text.slice(live.text.length)
        return [item]
      }
      if (page.liveRevisionAtRequest === undefined || (revisions[item.id] || 0) <= page.liveRevisionAtRequest) return [item]
      // Native start time remains authoritative even when the body snapshot is stale.
      return live && item.timestamp_source === 'native'
        ? [{ ...live, created_at: item.created_at, timestamp_source: item.timestamp_source, completed_at: item.completed_at, turn_id: item.turn_id || live.turn_id }]
        : []
    })
    return { items, ahead }
  }
  const [latest, ...older] = pages
  if (latest) {
    const safe = safeItems(latest)
    store.mergeMessages(agentId, sessionId, safe.items, safe.ahead)
  }
  for (const page of older) {
    const safe = safeItems(page)
    store.mergeMessagesPrepend(agentId, sessionId, safe.items, safe.ahead)
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
      const startedAt = typeof performance !== 'undefined' ? performance.now() : Date.now()
      const payload = await api.getBootstrap()
      try {
        await migrateSessionPins(payload.sessions, localStorage)
      } catch {
        // Unavailable storage must not block native session loading.
      }
      const endedAt = typeof performance !== 'undefined' ? performance.now() : Date.now()
      clientPerf.recordBootstrap(Math.max(0, endedAt - startedAt))
      return payload
    },
    enabled: authenticated,
    retry: false,
    staleTime: 10_000,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  })
  useEffect(() => {
    if (query.data) useAstrorderStore.getState().hydrateBootstrap(query.data)
  }, [query.data])
  return query
}

export function useSessionResources(session: Session | null, authenticated: boolean, viewing = true) {
  useEffect(() => {
    if (!authenticated || !session || !viewing) return
    const renew = () => {
      if (document.visibilityState === 'visible' && !isUserIdle()) {
        void api.renewSessionView(session.id, session.agent_id).catch(() => {})
      }
    }
    renew()
    const timer = window.setInterval(renew, 30_000)
    const unsubscribeIdle = subscribeUserIdle((idle) => {
      if (!idle) renew()
    })
    document.addEventListener('visibilitychange', renew)
    return () => {
      window.clearInterval(timer)
      unsubscribeIdle()
      document.removeEventListener('visibilitychange', renew)
    }
  }, [authenticated, session?.agent_id, session?.id, viewing])

  const agentId = session?.agent_id
  const sessionId = session?.id
  const sessionScopeKey = agentId && sessionId ? scopeKey(agentId, sessionId) : ''
  const stored = useAstrorderStore(useShallow((state) =>
    agentId && sessionId ? selectMessages(state, agentId, sessionId) : EMPTY_STORED_MESSAGES,
  ))
  useEffect(() => {
    if (!authenticated || !sessionScopeKey) return
    const cached = Object.keys(useAstrorderStore.getState().messages[sessionScopeKey] || {}).length > 0
    clientPerf.markSessionSwitchStart(sessionScopeKey, cached)
  }, [authenticated, sessionScopeKey])
  const sync = useQuery({
    queryKey: ['astrorder', 'session-sync', session?.agent_id, session?.id],
    queryFn: () => api.syncSession(session!.id, session!.agent_id),
    enabled: authenticated && Boolean(session),
    retry: false,
    staleTime: 0,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
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

  const queryClient = useQueryClient()
  const latestPageQuery = useQuery<LoadedHistoryPage>({
    queryKey: ['astrorder', 'messages', session?.agent_id, session?.id],
    queryFn: async () => {
      const liveRevisionAtRequest = useAstrorderStore.getState().messageRevision
      const previous = queryClient.getQueryData<LoadedHistoryPage>([
        'astrorder',
        'messages',
        session?.agent_id,
        session?.id,
      ])
      const page = await refreshActivityHistory(
        (before, limit) => api.getMessages(session!.id, session!.agent_id, before, limit),
        previous,
        INITIAL_HISTORY_LIMIT,
      )
      return { ...page, liveRevisionAtRequest }
    },
    enabled: ready,
    retry: false,
    staleTime: 30_000,
    gcTime: 1000 * 60 * 60 * 24,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  })

  const [olderPages, setOlderPages] = useState<LoadedHistoryPage[]>(() =>
    sessionScopeKey ? getOlderPagesMap(queryClient).get(sessionScopeKey) || EMPTY_OLDER_PAGES : EMPTY_OLDER_PAGES,
  )
  const [isFetchingNextPage, setIsFetchingNextPage] = useState(false)
  const olderFetchInFlightRef = useRef<Promise<void> | null>(null)
  const olderPagesRef = useRef(olderPages)
  olderPagesRef.current = olderPages

  useEffect(() => {
    setIsFetchingNextPage(false)
    olderFetchInFlightRef.current = null
    if (!sessionScopeKey) {
      setOlderPages(EMPTY_OLDER_PAGES)
      return
    }
    setOlderPages(getOlderPagesMap(queryClient).get(sessionScopeKey) || EMPTY_OLDER_PAGES)
  }, [queryClient, sessionScopeKey])

  const pages = useMemo<LoadedHistoryPage[]>(() => {
    if (!latestPageQuery.data) return olderPages
    return [latestPageQuery.data, ...olderPages]
  }, [latestPageQuery.data, olderPages])

  const pagesRef = useRef(pages)
  pagesRef.current = pages

  const lastCursor = pages.length > 0 ? pages[pages.length - 1].next_cursor : null
  const hasNextPage = Boolean(lastCursor)

  const fetchNextPage = useCallback(async () => {
    if (!ready || !session || !sessionScopeKey) return
    if (olderFetchInFlightRef.current) return olderFetchInFlightRef.current
    const currentPages = pagesRef.current
    const cursor = currentPages.length > 0 ? currentPages[currentPages.length - 1].next_cursor : null
    if (!cursor) return

    const scopeAtStart = sessionScopeKey
    const targetSessionId = session.id
    const targetAgentId = session.agent_id
    setIsFetchingNextPage(true)
    let pending: Promise<void> | null = null
    pending = (async () => {
      try {
        const liveRevisionAtRequest = useAstrorderStore.getState().messageRevision
        const page = await loadActivityHistory(
          (before, limit) => api.getMessages(targetSessionId, targetAgentId, before, limit),
          cursor,
          OLDER_HISTORY_LIMIT,
        )
        const loaded: LoadedHistoryPage = { ...page, liveRevisionAtRequest }
        const storeMap = getOlderPagesMap(queryClient)
        const prev = storeMap.get(scopeAtStart) || EMPTY_OLDER_PAGES
        const next = [...prev, loaded]
        storeMap.set(scopeAtStart, next)
        if (scopeAtStart === sessionScopeKey) {
          setOlderPages(next)
        }
      } finally {
        if (olderFetchInFlightRef.current === pending) {
          olderFetchInFlightRef.current = null
        }
        setIsFetchingNextPage(false)
      }
    })()
    olderFetchInFlightRef.current = pending
    return pending
  }, [queryClient, ready, session, sessionScopeKey])

  const messages = useMemo(() => ({
    ...latestPageQuery,
    data: pages.length > 0 ? { pages } : undefined,
    hasNextPage,
    isFetchingNextPage,
    fetchNextPage,
  }), [fetchNextPage, hasNextPage, isFetchingNextPage, latestPageQuery, pages])

  const autoFetchedRef = useRef<Record<string, number>>({})
  const fetchNextPageRef = useRef(fetchNextPage)
  fetchNextPageRef.current = fetchNextPage
  useEffect(() => {
    if (!session || pages.length === 0 || isFetchingNextPage) return
    const sKey = `${session.agent_id}::${session.id}`
    const fetchedCount = autoFetchedRef.current[sKey] || 0
    const allLoaded = pages.flatMap((page) => page.items)
    if (pages.some(page => page.completeActivityGroup)) return
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
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  })
  const tasks = useQuery({
    queryKey: ['astrorder', 'tasks', session?.agent_id, session?.id],
    queryFn: () => api.getTasks(session!.id, session!.agent_id),
    enabled: ready,
    retry: false,
    staleTime: 30_000,
    gcTime: 1000 * 60 * 60 * 24,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  })
  useEffect(() => {
    if (agentId && sessionId && pages.length > 0) {
      mergeHistoryPages(agentId, sessionId, pages)
    }
  }, [pages, agentId, sessionId])
  useEffect(() => {
    if (commands.data) useAstrorderStore.getState().mergeCommands(commands.data.items)
  }, [commands.data])
  useEffect(() => {
    if (tasks.data) useAstrorderStore.getState().mergeTasks(tasks.data.items)
  }, [tasks.data])

  const visibleMessages = useMemo(() => {
    if (!session) return []
    if (pages.length > 0) {
      const seenLoaded = new Map<string, Message>()
      for (let i = pages.length - 1; i >= 0; i--) {
        for (const item of pages[i].items) {
          seenLoaded.set(item.id, item)
        }
      }
      const loadedItems = [...seenLoaded.values()]
      const revisionAtRequest = pages[0].liveRevisionAtRequest
      const revisions = useAstrorderStore.getState().messageEventRevisions[scopeKey(session.agent_id, session.id)] || {}
      // Cached history is not a live extension of the current native window.
      // Codex often has no item timestamps (the wire uses the Unix epoch), so
      // time comparisons cannot distinguish an old cached answer from new output.
      const baseline = new Set(stored.filter(message =>
        (revisions[message.id] || 0) <= revisionAtRequest,
      ).map(message => message.id))
      return visibleTranscript(stored, loadedItems, baseline)
    }
    return stored
  }, [session, pages, stored])

  const activityBreaks = useMemo(() => {
    const breaks: string[] = []
    const seen = new Set<string>()
    for (const page of pages) {
      const first = visibleHistoryItems(page.items)[0]
      if (first && !seen.has(first.id)) {
        seen.add(first.id)
        breaks.push(first.id)
      }
    }
    return breaks
  }, [pages])
  useEffect(() => {
    if (!authenticated || !sessionScopeKey) return
    if (messages.isSuccess || stored.length > 0) {
      clientPerf.markSessionSwitchReady(sessionScopeKey, visibleMessages.length)
    }
  }, [authenticated, sessionScopeKey, messages.isSuccess, stored.length, visibleMessages.length])
  return { messages, commands, tasks, visibleMessages, activityBreaks }
}

export function useRuntime(authenticated: boolean) {
  return useQuery({
    queryKey: ['astrorder', 'runtime'],
    queryFn: api.getRuntime,
    enabled: authenticated,
    retry: false,
    staleTime: 10_000,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
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
