import { useState, type ReactNode } from 'react'
import { Button } from '@mantine/core'
import { IconLock } from '@tabler/icons-react'
import { useQueryClient } from '@tanstack/react-query'
import { api } from '../../api/client'
import type { Session } from '../../domain/types'
import { useSessionModel } from '../../hooks/useSessionModel'
import './SessionComposerGate.css'

export function SessionComposerGate({ session, onFork, children }: {
  session: Session
  onFork?: () => void
  children: ReactNode
}) {
  const model = useSessionModel(session)
  const client = useQueryClient()
  const [retrying, setRetrying] = useState(false)
  const [error, setError] = useState('')
  const blocked = model.data?.read_only === true || session.control_state === 'readonly'
  if (!blocked) return <>{children}</>
  const retry = async () => {
    setRetrying(true)
    setError('')
    try {
      await api.syncSession(session.id, session.agent_id)
      await model.refetch()
      await client.invalidateQueries({ queryKey: ['astrorder', 'bootstrap'] })
    } catch (err) {
      setError(err instanceof Error ? err.message : '重试失败，请稍后再试')
    } finally {
      setRetrying(false)
    }
  }
  return <div className="session-composer-gate" role="status">
    <IconLock size={18} aria-hidden="true" />
    <div className="session-composer-gate-copy">
      <strong>已在另一个应用中打开</strong>
      <span>请先在那边关闭会话，才能在这里继续。</span>
      {error && <span role="alert">{error}</span>}
    </div>
    <div className="session-composer-gate-actions">
      <Button variant="subtle" color="gray" size="compact-sm" loading={retrying} onClick={() => void retry()}>重试</Button>
      <Button variant="subtle" color="gray" size="compact-sm" disabled={!onFork || retrying} onClick={onFork}>创建聊天分叉</Button>
    </div>
  </div>
}
