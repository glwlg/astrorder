import type { SessionStatus } from '../domain/types'
import { formatRelativeTime } from './sessionRailModel'
import { StatusDot } from './Status'

export function SessionRowMeta({ status, updatedAt, unread }: {
  status: SessionStatus
  updatedAt: string
  unread?: boolean
}) {
  return (
    <div className="session-row-info">
      {status === 'running' ? (
        <span className="session-row-wave status-running" role="img" aria-label="运行中">
          <i /><i /><i />
        </span>
      ) : (
        <>
          <span className="session-row-time">{formatRelativeTime(updatedAt)}</span>
          <StatusDot status={unread && status === 'idle' ? 'unread' : status} />
        </>
      )}
    </div>
  )
}
