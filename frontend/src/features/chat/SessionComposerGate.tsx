import { useState, type ReactNode } from 'react'
import { Button } from '@mantine/core'
import { IconLock } from '@tabler/icons-react'
import { useQueryClient } from '@tanstack/react-query'
import { api } from '../../api/client'
import type { Session } from '../../domain/types'
import { useSessionModel } from '../../hooks/useSessionModel'
import { InlineNotice } from '../../components/InlineNotice'

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
  return <InlineNotice className="session-composer-gate" title="已在另一个应用中打开" icon={<IconLock size={18} />} actions={<>
    <Button variant="default" size="compact-sm" loading={retrying} onClick={() => void retry()}>重试</Button>
    <Button variant="default" size="compact-sm" disabled={!onFork || retrying} onClick={onFork}>创建聊天分叉</Button>
  </>}>
    请先在那边关闭会话，才能在这里继续。
    {error && <div role="alert">{error}</div>}
  </InlineNotice>
}
