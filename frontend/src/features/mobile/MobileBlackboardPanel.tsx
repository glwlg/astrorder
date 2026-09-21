import { useCallback, useEffect, useState } from 'react'
import { IconRefresh } from '@tabler/icons-react'
import { api } from '../../api/client'
import { BlackboardItemRenderer } from '../monitor/BlackboardJsonRender'

export function MobileBlackboardPanel({ namespace }: { namespace: string }) {
  const [items, setItems] = useState<Record<string, unknown>>({})
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(false)

  const refresh = useCallback(async () => {
    setLoading(true)
    try {
      const response = await api.getBlackboard(namespace)
      setItems(response.items || {})
      setError(false)
    } catch {
      setError(true)
    } finally {
      setLoading(false)
    }
  }, [namespace])

  useEffect(() => {
    void refresh()
    const timer = window.setInterval(() => void refresh(), 4000)
    return () => window.clearInterval(timer)
  }, [refresh])

  const entries = Object.entries(items).reverse()

  return (
    <div className="m-blackboard">
      <div className="m-blackboard-toolbar">
        <span>{entries.length} 项</span>
        <button aria-label="刷新黑板" disabled={loading} onClick={() => void refresh()}>
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
            <BlackboardItemRenderer key={key} itemKey={key} itemValue={value} namespace={namespace} onUpdated={refresh} />
          ))}
        </div>
      )}
    </div>
  )
}
