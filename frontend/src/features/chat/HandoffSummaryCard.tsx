import { IconCheck, IconChevronRight, IconCopy, IconLoader2, IconRefresh, IconTransfer } from '@tabler/icons-react'
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { api } from '../../api/client'
import { agentKindLabel } from '../../components/AgentBrandIcon'
import type { Session, SessionHandoffContext } from '../../domain/types'
import { useAstrorderStore } from '../../state/store'

const handoffSummaryCache = new Map<string, SessionHandoffContext>()

export function clearHandoffSummaryCache() {
  handoffSummaryCache.clear()
}

function handoffModeLabel(mode?: string): string {
  switch (mode) {
    case 'native_summary':
      return '源 Agent 原生交接摘要'
    case 'transcript_excerpt':
      return '源会话历史摘录'
    default:
      return '交接上下文'
  }
}

export interface HandoffSummaryCardProps {
  session?: Session | null
  compact?: boolean
  renderMarkdown: (summary: string) => ReactNode
}

export function HandoffSummaryCard({ session, compact = false, renderMarkdown }: HandoffSummaryCardProps) {
  const hasHandoffContext = Boolean(session?.handoff_context?.trim())
  const cacheKey = session && hasHandoffContext
    ? `${session.agent_id}:${session.id}:${session.handoff_context}`
    : ''

  const sourceAgent = useAstrorderStore((state) =>
    session?.handoff_from_agent_id ? state.agents[session.handoff_from_agent_id] : undefined,
  )

  const [open, setOpen] = useState(false)
  const [data, setData] = useState<SessionHandoffContext | null>(() =>
    cacheKey ? handoffSummaryCache.get(cacheKey) ?? null : null,
  )
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)
  const mountedRef = useRef(true)
  const copyTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      if (copyTimerRef.current) clearTimeout(copyTimerRef.current)
    }
  }, [])

  useEffect(() => {
    setOpen(false)
    setError(null)
    setLoading(false)
    setCopied(false)
    setData(cacheKey ? handoffSummaryCache.get(cacheKey) ?? null : null)
  }, [cacheKey])

  const loadContext = useCallback(async (force = false) => {
    if (!session || !cacheKey) return
    if (!force) {
      const cached = handoffSummaryCache.get(cacheKey)
      if (cached) {
        setData(cached)
        setError(null)
        return
      }
    }
    setLoading(true)
    setError(null)
    try {
      const result = await api.getSessionHandoffContext(session.id, session.agent_id)
      if (!mountedRef.current) return
      handoffSummaryCache.set(cacheKey, result)
      setData(result)
    } catch (err) {
      if (!mountedRef.current) return
      setError(err instanceof Error ? err.message : '读取交接摘要失败')
    } finally {
      if (mountedRef.current) setLoading(false)
    }
  }, [cacheKey, session])

  useEffect(() => {
    if (open && !data && !loading && !error) {
      void loadContext(false)
    }
  }, [open, data, loading, error, loadContext])

  if (!session || !hasHandoffContext) return null

  const sourceKind = data?.source_agent_kind || sourceAgent?.kind
  const sourceLabel = sourceKind ? agentKindLabel(sourceKind) : (data?.source_agent_name || '')
  const consumed = Boolean(session.handoff_context_consumed || data?.consumed)

  const handleCopy = () => {
    const text = data?.summary?.trim()
    if (!text) return
    void navigator.clipboard?.writeText(text).then(() => {
      if (!mountedRef.current) return
      setCopied(true)
      if (copyTimerRef.current) clearTimeout(copyTimerRef.current)
      copyTimerRef.current = setTimeout(() => {
        if (mountedRef.current) setCopied(false)
      }, 1800)
    }).catch(() => undefined)
  }

  return (
    <section
      className={`handoff-summary-card${compact ? ' is-compact' : ''}${open ? ' is-open' : ''}`}
      aria-label="交接摘要"
      data-testid="handoff-summary-card"
    >
      <button
        type="button"
        className="handoff-summary-toggle"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        <span className="handoff-summary-chevron" aria-hidden="true">
          <IconChevronRight size={14} />
        </span>
        <span className="handoff-summary-icon" aria-hidden="true">
          <IconTransfer size={15} />
        </span>
        <span className="handoff-summary-title">交接摘要</span>
        {sourceLabel && <span className="handoff-summary-source">来自 {sourceLabel}</span>}
        <span className={`handoff-summary-status${consumed ? ' is-consumed' : ' is-pending'}`}>
          {consumed ? '已注入上下文' : '待随首条消息注入'}
        </span>
      </button>

      {open && (
        <div
          className="handoff-summary-panel"
          onTouchStart={compact ? (event) => event.stopPropagation() : undefined}
        >
          {loading && !data ? (
            <div className="handoff-summary-state" role="status">
              <IconLoader2 className="handoff-summary-spin" size={15} aria-hidden="true" />
              <span>正在读取交接摘要…</span>
            </div>
          ) : error ? (
            <div className="handoff-summary-state is-error" role="alert">
              <span>{error}</span>
              <button
                type="button"
                className="handoff-summary-action"
                onClick={() => void loadContext(true)}
              >
                <IconRefresh size={13} aria-hidden="true" />
                <span>重试</span>
              </button>
            </div>
          ) : data && data.available && data.summary?.trim() ? (
            <>
              <div className="handoff-summary-toolbar">
                <div className="handoff-summary-meta">
                  <span>{handoffModeLabel(data.mode)}</span>
                  {data.source_session_title && (
                    <span className="handoff-summary-meta-sep" title={data.source_session_title}>
                      原会话：{data.source_session_title}
                    </span>
                  )}
                </div>
                <button
                  type="button"
                  className="handoff-summary-action"
                  aria-label="复制交接摘要"
                  onClick={handleCopy}
                >
                  {copied ? <IconCheck size={13} aria-hidden="true" /> : <IconCopy size={13} aria-hidden="true" />}
                  <span>{copied ? '已复制' : '复制摘要'}</span>
                </button>
              </div>
              <div className="handoff-summary-body">
                {renderMarkdown(data.summary)}
              </div>
            </>
          ) : (
            <div className="handoff-summary-state">当前会话没有可展示的交接摘要。</div>
          )}
        </div>
      )}
    </section>
  )
}
