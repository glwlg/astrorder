import { useCallback, useEffect, useRef, useState } from 'react'
import { IconRefresh } from '@tabler/icons-react'
import { api } from '../../api/client'
import { BlackboardItemRenderer } from '../monitor/BlackboardJsonRender'

const BLACKBOARD_FALLBACK_POLL_MS = 30_000
const BLACKBOARD_EVENT_DEBOUNCE_MS = 180

export function MobileBlackboardPanel({ namespace }: { namespace: string }) {
  const [items, setItems] = useState<Record<string, unknown>>({})
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(false)
  const mountedRef = useRef(true)
  const inFlightRef = useRef(false)
  const lastFetchedAtRef = useRef(0)

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  const refresh = useCallback(async (force = false) => {
    if (inFlightRef.current && !force) return
    inFlightRef.current = true
    setLoading(true)
    try {
      const response = await api.getBlackboard(namespace)
      if (!mountedRef.current) return
      lastFetchedAtRef.current = Date.now()
      setItems(response.items || {})
      setError(false)
    } catch {
      if (mountedRef.current) setError(true)
    } finally {
      inFlightRef.current = false
      if (mountedRef.current) setLoading(false)
    }
  }, [namespace])

  useEffect(() => {
    let debounceTimer: ReturnType<typeof setTimeout> | undefined
    void refresh(true)

    const onBlackboardChange = (event: Event) => {
      const detail = (event as CustomEvent<{ namespace?: string } | undefined>).detail
      if (detail?.namespace && detail.namespace !== namespace) return
      clearTimeout(debounceTimer)
      debounceTimer = setTimeout(() => {
        void refresh()
      }, BLACKBOARD_EVENT_DEBOUNCE_MS)
    }

    const onVisibilityChange = () => {
      if (document.visibilityState === 'visible' && Date.now() - lastFetchedAtRef.current >= 10_000) {
        void refresh()
      }
    }

    const timer = window.setInterval(() => {
      if (document.visibilityState === 'visible') {
        void refresh()
      }
    }, BLACKBOARD_FALLBACK_POLL_MS)

    window.addEventListener('astrorder:blackboard-change', onBlackboardChange)
    document.addEventListener('visibilitychange', onVisibilityChange)
    return () => {
      clearTimeout(debounceTimer)
      window.clearInterval(timer)
      window.removeEventListener('astrorder:blackboard-change', onBlackboardChange)
      document.removeEventListener('visibilitychange', onVisibilityChange)
    }
  }, [namespace, refresh])

  const entries = Object.entries(items).reverse()

  return (
    <div className="m-blackboard">
      <div className="m-blackboard-toolbar">
        <span>{entries.length} 项</span>
        <button aria-label="刷新黑板" disabled={loading} onClick={() => void refresh(true)}>
          <IconRefresh className={loading ? 'm-spin' : undefined} size={17} />
        </button>
      </div>
      {error ? (
        <div className="m-blackboard-state">黑板加载失败，请重试</div>
      ) : entries.length === 0 && !loading ? (
        <div className="m-blackboard-state">当前黑板暂无共享参数</div>
      ) : (
        <div className="m-blackboard-items">
          {entries.map(([key, value]) => (
            <BlackboardItemRenderer key={key} itemKey={key} itemValue={value} namespace={namespace} onUpdated={() => void refresh(true)} />
          ))}
        </div>
      )}
    </div>
  )
}
