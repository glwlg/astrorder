import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { GitWorktreeEntry, Message, Session } from '../../domain/types'
import { useGitWorktrees } from '../../hooks/useGitWorktrees'

const STORAGE_KEY = 'astrorder:active-worktree-view:v1'
const MAX_SCANNED_MESSAGES = 25

function normalizeFsPath(raw?: string | null): string {
  const trimmed = (raw || '').trim()
  if (!trimmed) return ''
  let normalized = trimmed.replace(/\\/g, '/')
  while (normalized.length > 1 && normalized.endsWith('/')) {
    if (/^[a-zA-Z]:\/$/.test(normalized)) break
    normalized = normalized.slice(0, -1)
  }
  if (/^[a-zA-Z]:\//.test(normalized)) {
    normalized = normalized.toLowerCase()
  }
  return normalized
}

export function sameWorktreePath(a?: string | null, b?: string | null): boolean {
  const na = normalizeFsPath(a)
  const nb = normalizeFsPath(b)
  return Boolean(na && nb && na === nb)
}

function isLeftPathBoundary(ch: string | undefined): boolean {
  if (!ch) return true
  return !/[A-Za-z0-9_.\-/\\]/.test(ch)
}

function isRightPathBoundary(ch: string | undefined): boolean {
  if (!ch) return true
  return !/[A-Za-z0-9_.-]/.test(ch)
}

function findLastPathMentionIndex(haystack: string, needle: string): number {
  if (!needle || !haystack) return -1
  let searchFrom = haystack.length
  while (searchFrom >= needle.length) {
    const idx = haystack.lastIndexOf(needle, searchFrom - needle.length)
    if (idx === -1) return -1
    const left = idx > 0 ? haystack[idx - 1] : undefined
    const right = idx + needle.length < haystack.length ? haystack[idx + needle.length] : undefined
    if (isLeftPathBoundary(left) && isRightPathBoundary(right)) {
      return idx
    }
    searchFrom = idx + needle.length - 1
  }
  return -1
}

function extractMessageSearchText(msg: Message): string {
  const parts: string[] = []
  if (msg.text) parts.push(msg.text)
  if (msg.tool) {
    try {
      parts.push(JSON.stringify(msg.tool))
    } catch {
      // ignore unserializable tool payload
    }
  }
  return parts.join('\n').replace(/\\/g, '/')
}

/**
 * Detects whether the Agent is actively operating inside a registered linked Git worktree
 * by matching paths referenced in recent messages/tool payloads against `git worktree list`.
 * Only active (non-bare, non-prunable) linked worktrees distinct from `currentWorkspace` match.
 */
export function detectActiveShadowWorktree(
  messages: Message[],
  worktrees: GitWorktreeEntry[],
  currentWorkspace?: string | null,
): GitWorktreeEntry | null {
  const linked = worktrees
    .filter((wt) => !wt.bare && !wt.prunable && Boolean(wt.path) && !sameWorktreePath(wt.path, currentWorkspace))
    .map((wt) => ({
      entry: wt,
      norm: normalizeFsPath(wt.path),
      caseInsensitive: /^[a-zA-Z]:[\\/]/.test(wt.path),
    }))
    .filter((item) => item.norm.length >= 2)
    .sort((a, b) => b.norm.length - a.norm.length)

  if (!linked.length || !messages.length) return null

  const start = Math.max(0, messages.length - MAX_SCANNED_MESSAGES)
  for (let i = messages.length - 1; i >= start; i -= 1) {
    const rawHaystack = extractMessageSearchText(messages[i])
    if (!rawHaystack) continue
    let lowerHaystack: string | null = null

    let bestEntry: GitWorktreeEntry | null = null
    let bestIndex = -1
    let bestLen = -1

    for (const item of linked) {
      let haystack = rawHaystack
      if (item.caseInsensitive) {
        if (lowerHaystack === null) lowerHaystack = rawHaystack.toLowerCase()
        haystack = lowerHaystack
      }
      const idx = findLastPathMentionIndex(haystack, item.norm)
      if (idx === -1) continue
      if (idx > bestIndex || (idx === bestIndex && item.norm.length > bestLen)) {
        bestEntry = item.entry
        bestIndex = idx
        bestLen = item.norm.length
      }
    }

    if (bestEntry) return bestEntry
  }
  return null
}

function readStoredOverrides(): Record<string, string> {
  if (typeof window === 'undefined') return {}
  try {
    const raw = window.sessionStorage.getItem(STORAGE_KEY)
    if (!raw) return {}
    const parsed = JSON.parse(raw) as unknown
    if (!parsed || typeof parsed !== 'object') return {}
    const result: Record<string, string> = {}
    for (const [k, v] of Object.entries(parsed as Record<string, unknown>)) {
      if (typeof v === 'string' && v.trim()) result[k] = v
    }
    return result
  } catch {
    return {}
  }
}

function writeStoredOverrides(next: Record<string, string>) {
  if (typeof window === 'undefined') return
  try {
    window.sessionStorage.setItem(STORAGE_KEY, JSON.stringify(next))
  } catch {
    // ignore storage quota errors
  }
}

export function useSessionActiveWorktree(params: {
  session: Session | null
  messages: Message[]
  enabled?: boolean
}) {
  const { session, messages, enabled = true } = params
  const sessionKey = session ? `${session.agent_id}::${session.id}` : ''
  const queryEnabled = enabled && Boolean(session?.workspace?.trim())

  const worktreesQuery = useGitWorktrees({
    workspace: session?.workspace,
    connectionId: session?.connection_id,
    sessionId: session?.id,
    includeStatuses: false,
    enabled: queryEnabled,
  })

  // Refresh worktree list once whenever a recent message newly mentions "worktree"
  const lastWorktreeMentionIdRef = useRef<string>('')
  const latestWorktreeMentionId = useMemo(() => {
    if (!queryEnabled || !messages.length) return ''
    const start = Math.max(0, messages.length - 6)
    for (let i = messages.length - 1; i >= start; i -= 1) {
      const m = messages[i]
      if (/worktree/i.test(m.text || '')) return m.id
      if (m.tool && /worktree/i.test(JSON.stringify(m.tool))) return m.id
    }
    return ''
  }, [messages, queryEnabled])

  useEffect(() => {
    if (!queryEnabled || !latestWorktreeMentionId) return
    if (lastWorktreeMentionIdRef.current === latestWorktreeMentionId) return
    lastWorktreeMentionIdRef.current = latestWorktreeMentionId
    if (worktreesQuery.isSuccess) {
      void worktreesQuery.refetch()
    }
  }, [latestWorktreeMentionId, queryEnabled, worktreesQuery])

  const linkedWorktrees = useMemo(() => {
    const all = worktreesQuery.data?.worktrees || []
    return all.filter(
      (wt) => !wt.bare && !wt.prunable && Boolean(wt.path) && !sameWorktreePath(wt.path, session?.workspace),
    )
  }, [session?.workspace, worktreesQuery.data?.worktrees])

  const detectedWorktree = useMemo(
    () => (queryEnabled ? detectActiveShadowWorktree(messages, linkedWorktrees, session?.workspace) : null),
    [linkedWorktrees, messages, queryEnabled, session?.workspace],
  )

  const [overrideBySession, setOverrideBySession] = useState<Record<string, string>>(() => readStoredOverrides())
  const [dismissedBySession, setDismissedBySession] = useState<Record<string, string>>({})

  const overridePath = sessionKey ? overrideBySession[sessionKey] : undefined
  const activeWorktree = useMemo(() => {
    if (!overridePath) return null
    return linkedWorktrees.find((wt) => sameWorktreePath(wt.path, overridePath)) || null
  }, [linkedWorktrees, overridePath])

  const switchToWorktree = useCallback(
    (path: string) => {
      if (!sessionKey || !path) return
      setOverrideBySession((prev) => {
        const next = { ...prev, [sessionKey]: path }
        writeStoredOverrides(next)
        return next
      })
    },
    [sessionKey],
  )

  const resetToMain = useCallback(() => {
    if (!sessionKey) return
    setOverrideBySession((prev) => {
      if (!(sessionKey in prev)) return prev
      const next = { ...prev }
      delete next[sessionKey]
      writeStoredOverrides(next)
      return next
    })
  }, [sessionKey])

  const dismissBanner = useCallback(() => {
    if (!sessionKey || !detectedWorktree) return
    setDismissedBySession((prev) => ({
      ...prev,
      [sessionKey]: detectedWorktree.path,
    }))
  }, [detectedWorktree, sessionKey])

  const bannerDismissed = Boolean(
    sessionKey &&
      detectedWorktree &&
      dismissedBySession[sessionKey] &&
      sameWorktreePath(dismissedBySession[sessionKey], detectedWorktree.path),
  )

  const effectiveSession = useMemo(() => {
    if (!session || !activeWorktree) return session
    return {
      ...session,
      workspace: activeWorktree.path,
      worktree_root_workspace: session.worktree_root_workspace || session.workspace || undefined,
      worktree_branch: activeWorktree.branch || session.worktree_branch || undefined,
    }
  }, [activeWorktree, session])

  return {
    linkedWorktrees,
    detectedWorktree,
    activeWorktree,
    isSwitched: Boolean(activeWorktree),
    bannerDismissed,
    switchToWorktree,
    resetToMain,
    dismissBanner,
    effectiveSession,
  }
}
